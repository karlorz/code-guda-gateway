package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"code-guda-gateway/internal/cooldown"
	"code-guda-gateway/internal/providers"
)

const tavilyPlanLimitStatus = 432

// KeySelector selects provider keys and records upstream outcomes.
type KeySelector interface {
	SelectKey(provider string) (keyID int64, rawKey string, err error)
	MarkSuccess(keyID int64) error
	MarkFailureWithCooldown(keyID int64, status int, redactedMsg string, until *time.Time, reason *string) error
}

type Target struct {
	BaseURL  string
	Path     string
	Provider string
	Keys     KeySelector
}

type Options struct {
	Client *http.Client
}

type Proxy struct {
	client   *http.Client
	settings cooldown.Settings
}

type Result struct {
	Err          error
	StatusCode   int // final HTTP status written to client; 0 if none
	NetworkError bool
}

func New(opts Options) *Proxy {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &Proxy{client: client, settings: cooldown.DefaultSettings()}
}

// SetCooldownSettings configures retry limits and cooldown durations for forwarding.
func (p *Proxy) SetCooldownSettings(s cooldown.Settings) {
	p.settings = s
}

func (p *Proxy) Forward(w http.ResponseWriter, r *http.Request, target Target) Result {
	if strings.TrimSpace(target.BaseURL) == "" {
		http.Error(w, "upstream base URL is not configured", http.StatusBadGateway)
		return Result{Err: fmt.Errorf("upstream base URL is not configured"), StatusCode: http.StatusBadGateway}
	}
	if target.Keys == nil || strings.TrimSpace(target.Provider) == "" {
		http.Error(w, "upstream API keys are not configured", http.StatusBadGateway)
		return Result{Err: fmt.Errorf("upstream API keys are not configured"), StatusCode: http.StatusBadGateway}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return Result{Err: err, StatusCode: http.StatusBadRequest}
	}

	maxAttempts := p.settings.MaxRetries
	if maxAttempts < 1 {
		maxAttempts = cooldown.DefaultMaxRetries
	}

	var lastStatus int
	var lastBody []byte
	var lastHeader http.Header
	now := time.Now()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		keyID, key, selErr := target.Keys.SelectKey(target.Provider)
		if selErr != nil {
			if errors.Is(selErr, providers.ErrNoEnabledKey) {
				if lastStatus != 0 {
					writeResponse(w, lastStatus, lastHeader, lastBody)
					return Result{StatusCode: lastStatus}
				}
				http.Error(w, "upstream API keys are not configured", http.StatusBadGateway)
				return Result{Err: selErr, StatusCode: http.StatusBadGateway}
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return Result{Err: selErr, StatusCode: http.StatusInternalServerError}
		}

		resp, err := p.do(r, target, key, body)
		if err != nil {
			coolDur := p.settings.Transient
			if coolDur <= 0 {
				coolDur = cooldown.DefaultTransientCooldown
			}
			until := now.Add(coolDur)
			reason := "network_error"
			_ = target.Keys.MarkFailureWithCooldown(keyID, 0, "network_error", &until, &reason)
			if attempt == maxAttempts-1 {
				http.Error(w, "upstream request failed", http.StatusBadGateway)
				return Result{Err: err, StatusCode: http.StatusBadGateway, NetworkError: true}
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			http.Error(w, "failed to read upstream response", http.StatusBadGateway)
			return Result{Err: readErr, StatusCode: http.StatusBadGateway}
		}

		lastStatus = resp.StatusCode
		lastBody = respBody
		lastHeader = resp.Header.Clone()
		if isTavilyPlanLimit(target.Provider, resp.StatusCode) {
			lastStatus, lastHeader, lastBody = tavilyPlanLimitClientResponse(resp.Header)
		}

		if cooldown.ShouldMarkSuccess(resp.StatusCode) {
			_ = target.Keys.MarkSuccess(keyID)
			writeResponse(w, resp.StatusCode, lastHeader, lastBody)
			return Result{StatusCode: resp.StatusCode}
		}

		coolDur, reason, applyCooldown, retryAcrossKeys := cooldown.PolicyForStatus(resp.StatusCode, p.settings)
		if isTavilyPlanLimit(target.Provider, resp.StatusCode) {
			coolDur = p.settings.RateLimit
			if coolDur <= 0 {
				coolDur = cooldown.DefaultRateLimitCooldown
			}
			reason = "plan_limit_exceeded"
			applyCooldown = true
			retryAcrossKeys = true
		}
		if applyCooldown {
			if resp.StatusCode == http.StatusTooManyRequests {
				if ra, ok := cooldown.ParseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
					coolDur = ra
				}
			}
			until := now.Add(coolDur)
			_ = target.Keys.MarkFailureWithCooldown(keyID, resp.StatusCode, string(respBody), &until, &reason)
		} else {
			_ = target.Keys.MarkFailureWithCooldown(keyID, resp.StatusCode, string(respBody), nil, nil)
		}

		if !retryAcrossKeys || attempt >= maxAttempts-1 {
			writeResponse(w, lastStatus, lastHeader, lastBody)
			return Result{StatusCode: lastStatus}
		}
	}

	if lastStatus != 0 {
		writeResponse(w, lastStatus, lastHeader, lastBody)
		return Result{StatusCode: lastStatus}
	}

	http.Error(w, "upstream request failed", http.StatusBadGateway)
	return Result{Err: fmt.Errorf("upstream request failed"), StatusCode: http.StatusBadGateway}
}

func (p *Proxy) do(r *http.Request, target Target, key string, body []byte) (*http.Response, error) {
	url := strings.TrimRight(target.BaseURL, "/") + target.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Authorization", "Bearer "+key)
	req.Host = ""
	return p.client.Do(req)
}

func isTavilyPlanLimit(provider string, status int) bool {
	return provider == providers.ProviderTavily && status == tavilyPlanLimitStatus
}

func tavilyPlanLimitClientResponse(header http.Header) (int, http.Header, []byte) {
	h := header.Clone()
	h.Del("Content-Length")
	h.Set("Content-Type", "application/json")
	body := []byte(`{"error":{"code":"tavily_plan_limit_exceeded","message":"Tavily plan usage limit exceeded"}}`)
	return http.StatusTooManyRequests, h, body
}

func writeResponse(w http.ResponseWriter, status int, header http.Header, body []byte) {
	for key, values := range header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

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
	Err error
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
		return Result{Err: fmt.Errorf("upstream base URL is not configured")}
	}
	if target.Keys == nil || strings.TrimSpace(target.Provider) == "" {
		http.Error(w, "upstream API keys are not configured", http.StatusBadGateway)
		return Result{Err: fmt.Errorf("upstream API keys are not configured")}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return Result{Err: err}
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
					return Result{}
				}
				http.Error(w, "upstream API keys are not configured", http.StatusBadGateway)
				return Result{Err: selErr}
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return Result{Err: selErr}
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
				return Result{Err: err}
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			http.Error(w, "failed to read upstream response", http.StatusBadGateway)
			return Result{Err: readErr}
		}

		lastStatus = resp.StatusCode
		lastBody = respBody
		lastHeader = resp.Header.Clone()

		if cooldown.ShouldMarkSuccess(resp.StatusCode) {
			_ = target.Keys.MarkSuccess(keyID)
			writeResponse(w, resp.StatusCode, lastHeader, lastBody)
			return Result{}
		}

		coolDur, reason, applyCooldown, retryAcrossKeys := cooldown.PolicyForStatus(resp.StatusCode, p.settings)
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
			return Result{}
		}
	}

	if lastStatus != 0 {
		writeResponse(w, lastStatus, lastHeader, lastBody)
		return Result{}
	}

	http.Error(w, "upstream request failed", http.StatusBadGateway)
	return Result{Err: fmt.Errorf("upstream request failed")}
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

func writeResponse(w http.ResponseWriter, status int, header http.Header, body []byte) {
	for key, values := range header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
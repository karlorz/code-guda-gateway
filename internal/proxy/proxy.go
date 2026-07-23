package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code-guda-gateway/internal/cooldown"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/usage"
)

const tavilyPlanLimitStatus = 432

// KeySelector selects atomic provider endpoints and records upstream outcomes.
type KeySelector interface {
	SelectEndpoint(provider string) (providers.SelectedEndpoint, error)
	MarkSuccess(keyID int64) error
	MarkFailureWithCooldown(keyID int64, status int, redactedMsg string, until *time.Time, reason *string) error
}

// AttemptRecorder is an optional best-effort sink for per-attempt debug rows.
// When nil or disabled, Forward behavior is unchanged.
type AttemptRecorder interface {
	Enabled() bool
	Record(AttemptLog) error
}

// Target describes a gateway route to forward. Upstream base URL comes from the
// selected endpoint row (not a provider-wide field).
type Target struct {
	Path     string
	Provider string
	Keys     KeySelector
}

// Options configures a Proxy. Client and AttemptRecorder are both optional;
// AttemptRecorder is disabled by default (nil).
type Options struct {
	Client          *http.Client
	AttemptRecorder AttemptRecorder
}

type Proxy struct {
	client   *http.Client
	settings cooldown.Settings
	attempts AttemptRecorder
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
	return &Proxy{client: client, settings: cooldown.DefaultSettings(), attempts: opts.AttemptRecorder}
}

// SetCooldownSettings configures retry limits and cooldown durations for forwarding.
func (p *Proxy) SetCooldownSettings(s cooldown.Settings) {
	p.settings = s
}

func (p *Proxy) Forward(w http.ResponseWriter, r *http.Request, target Target) Result {
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

	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	routeFamily := usage.RouteFamilyFromPath(r.URL.Path)
	path := r.URL.Path

	var lastStatus int
	var lastBody []byte
	var lastHeader http.Header
	now := time.Now()
	attemptLogging := p.attempts != nil && p.attempts.Enabled()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		endpoint, selErr := target.Keys.SelectEndpoint(target.Provider)
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

		if strings.TrimSpace(endpoint.BaseURL) == "" {
			http.Error(w, "upstream base URL is not configured", http.StatusBadGateway)
			return Result{Err: fmt.Errorf("upstream base URL is not configured"), StatusCode: http.StatusBadGateway}
		}

		// Minimal surface: record numeric key id only. Name/fingerprint stay nil;
		// the admin pool endpoint maps id -> display metadata for the UI.
		keyID := endpoint.ID
		keyIDCopy := keyID

		resp, err := p.do(r, target, endpoint, body)
		if err != nil {
			coolDur := p.settings.Transient
			if coolDur <= 0 {
				coolDur = cooldown.DefaultTransientCooldown
			}
			until := now.Add(coolDur)
			reason := "network_error"
			_ = target.Keys.MarkFailureWithCooldown(keyID, 0, "network_error", &until, &reason)
			terminal := attempt == maxAttempts-1
			untilStr := until.UTC().Format(time.RFC3339Nano)
			p.recordAttempt(AttemptLog{
				RequestID:     requestID,
				Provider:      target.Provider,
				RouteFamily:   routeFamily,
				Path:          path,
				AttemptIndex:  attempt + 1,
				ProviderKeyID: &keyIDCopy,
				StatusClass:   "network_error",
				Reason:        &reason,
				CooldownUntil: &untilStr,
				Terminal:      terminal,
			}, attemptLogging)
			if terminal {
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
			status := resp.StatusCode
			p.recordAttempt(AttemptLog{
				RequestID:      requestID,
				Provider:       target.Provider,
				RouteFamily:    routeFamily,
				Path:           path,
				AttemptIndex:   attempt + 1,
				ProviderKeyID:  &keyIDCopy,
				UpstreamStatus: &status,
				StatusClass:    usage.StatusClassFromHTTP(status),
				Terminal:       true,
			}, attemptLogging)
			writeResponse(w, resp.StatusCode, lastHeader, lastBody)
			return Result{StatusCode: resp.StatusCode}
		}

		coolDur, reason, applyCooldown, retryAcrossKeys := policyForResponse(
			target.Provider,
			resp.StatusCode,
			respBody,
			p.settings,
		)
		if isTavilyPlanLimit(target.Provider, resp.StatusCode) {
			coolDur = p.settings.RateLimit
			if coolDur <= 0 {
				coolDur = cooldown.DefaultRateLimitCooldown
			}
			reason = "plan_limit_exceeded"
			applyCooldown = true
			retryAcrossKeys = true
		}

		var untilPtr *time.Time
		var reasonPtr *string
		if applyCooldown {
			if resp.StatusCode == http.StatusTooManyRequests {
				if ra, ok := cooldown.ParseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
					coolDur = ra
				}
			}
			until := now.Add(coolDur)
			untilPtr = &until
			reasonPtr = &reason
			_ = target.Keys.MarkFailureWithCooldown(keyID, resp.StatusCode, string(respBody), &until, &reason)
		} else {
			_ = target.Keys.MarkFailureWithCooldown(keyID, resp.StatusCode, string(respBody), nil, nil)
		}

		terminal := !retryAcrossKeys || attempt >= maxAttempts-1
		status := resp.StatusCode
		row := AttemptLog{
			RequestID:      requestID,
			Provider:       target.Provider,
			RouteFamily:    routeFamily,
			Path:           path,
			AttemptIndex:   attempt + 1,
			ProviderKeyID:  &keyIDCopy,
			UpstreamStatus: &status,
			StatusClass:    usage.StatusClassFromHTTP(status),
			Terminal:       terminal,
		}
		if reasonPtr != nil {
			row.Reason = reasonPtr
		}
		if untilPtr != nil {
			untilStr := untilPtr.UTC().Format(time.RFC3339Nano)
			row.CooldownUntil = &untilStr
		}
		p.recordAttempt(row, attemptLogging)

		if terminal {
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

// recordAttempt is best-effort: nil/disabled recorder and Record errors are ignored.
func (p *Proxy) recordAttempt(row AttemptLog, enabled bool) {
	if !enabled || p.attempts == nil {
		return
	}
	_ = p.attempts.Record(row)
}

func (p *Proxy) do(r *http.Request, target Target, endpoint providers.SelectedEndpoint, body []byte) (*http.Response, error) {
	url, err := providers.JoinEndpointURL(endpoint.BaseURL, target.Path, r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	req.Host = ""
	return p.client.Do(req)
}

func isTavilyPlanLimit(provider string, status int) bool {
	return provider == providers.ProviderTavily && status == tavilyPlanLimitStatus
}

func policyForResponse(provider string, status int, body []byte, settings cooldown.Settings) (time.Duration, string, bool, bool) {
	if provider == providers.ProviderFirecrawl &&
		status == http.StatusPaymentRequired &&
		isFirecrawlCreditExhausted(body) {
		duration := settings.RateLimit
		if duration <= 0 {
			duration = cooldown.DefaultRateLimitCooldown
		}
		return duration, "credit_exhausted", true, true
	}
	return cooldown.PolicyForStatus(status, settings)
}

func isFirecrawlCreditExhausted(body []byte) bool {
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Code    string          `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}

	signals := []string{payload.Message, payload.Code}
	if len(payload.Error) > 0 {
		var errorText string
		if err := json.Unmarshal(payload.Error, &errorText); err == nil {
			signals = append(signals, errorText)
		} else {
			var errorObject struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(payload.Error, &errorObject); err == nil {
				signals = append(signals, errorObject.Message, errorObject.Code, errorObject.Error)
			}
		}
	}

	for _, signal := range signals {
		normalized := strings.ToLower(strings.TrimSpace(signal))
		if strings.Contains(normalized, "insufficient credit") ||
			strings.Contains(normalized, "credit_exhausted") {
			return true
		}
	}
	return false
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

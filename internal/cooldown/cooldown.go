package cooldown

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRateLimitCooldown  = 60 * time.Second
	DefaultTransientCooldown  = 30 * time.Second
	DefaultCredentialCooldown = time.Hour
	DefaultMaxRetries         = 3
	MaxRetryAfter             = 10 * time.Minute
)

// Settings holds service-wide cooldown and retry configuration.
type Settings struct {
	RateLimit  time.Duration
	Transient  time.Duration
	Credential time.Duration
	MaxRetries int
}

// DefaultSettings returns built-in defaults when SQLite settings are unset.
func DefaultSettings() Settings {
	return Settings{
		RateLimit:  DefaultRateLimitCooldown,
		Transient:  DefaultTransientCooldown,
		Credential: DefaultCredentialCooldown,
		MaxRetries: DefaultMaxRetries,
	}
}

// ParseRetryAfter interprets the Retry-After header (seconds or HTTP-date).
// Durations are capped at MaxRetryAfter. now is used for HTTP-date parsing.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		return capRetryAfter(d), true
	}
	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return capRetryAfter(d), true
	}
	return 0, false
}

func capRetryAfter(d time.Duration) time.Duration {
	if d > MaxRetryAfter {
		return MaxRetryAfter
	}
	return d
}

// PolicyForStatus returns cooldown duration (when applyCooldown), reason, and whether
// the proxy should attempt another provider key after marking failure.
// For 429, cooldownDur is the default when Retry-After is absent; proxy may override from header.
func PolicyForStatus(status int, s Settings) (cooldownDur time.Duration, reason string, applyCooldown bool, retryAcrossKeys bool) {
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return 0, "", false, false
	case http.StatusTooManyRequests:
		return s.RateLimit, "rate_limited", true, true
	case http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return s.Transient, "transient", true, true
	case http.StatusUnauthorized, http.StatusForbidden:
		return s.Credential, "credential_error", true, true
	default:
		if status >= 200 && status < 300 {
			return 0, "", false, false
		}
		return 0, "", false, false
	}
}

// ShouldMarkSuccess reports whether a status is treated as upstream success for key health.
func ShouldMarkSuccess(status int) bool {
	return status >= 200 && status < 300
}

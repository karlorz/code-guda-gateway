package providers

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeBaseURL validates and normalizes an operator-provided endpoint base.
// Provider-specific path prefixes are preserved; only trailing slashes are removed.
// Validation failures wrap ErrInvalidBaseURL so callers can map them to HTTP 400.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: base URL is required", ErrInvalidBaseURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse base URL: %v", ErrInvalidBaseURL, err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: base URL scheme must be http or https", ErrInvalidBaseURL)
	}
	if u.Hostname() == "" || u.Opaque != "" {
		return "", fmt.Errorf("%w: base URL must include a host", ErrInvalidBaseURL)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: base URL must not include user information", ErrInvalidBaseURL)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("%w: base URL must not include a query string", ErrInvalidBaseURL)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("%w: base URL must not include a fragment", ErrInvalidBaseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	return u.String(), nil
}

// JoinEndpointURL appends a gateway-controlled relative route to an endpoint
// base while preserving any configured upstream path prefix.
func JoinEndpointURL(baseURL, suffix, rawQuery string) (string, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	if suffix == "" || !strings.HasPrefix(suffix, "/") {
		return "", fmt.Errorf("endpoint path must start with /")
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("parse normalized base URL: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	u.RawPath = ""
	u.RawQuery = rawQuery
	return u.String(), nil
}

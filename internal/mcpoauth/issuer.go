package mcpoauth

import (
	"net/url"
	"strings"
)

// ValidateIssuer validates the public issuer origin:
// - no path (or empty/slash only)
// - no query
// - no fragment
// - scheme must be https, or http only for loopback hosts (127.0.0.1, localhost, ::1)
// Returns cleaned normalized issuer (e.g. "https://example.com" or "http://127.0.0.1:8080") or empty string if invalid.
func ValidateIssuer(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Path != "" && u.Path != "/" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}

	hostname := u.Hostname()
	switch u.Scheme {
	case "https":
		// Allowed for any host
	case "http":
		if !isLoopbackHost(hostname) {
			return "", false
		}
	default:
		return "", false
	}

	// Canonical origin format: scheme://host (without trailing slash)
	origin := u.Scheme + "://" + u.Host
	return origin, true
}

// ValidateRedirectURI checks that a redirect URI is https or loopback http.
func ValidateRedirectURI(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	hostname := u.Hostname()
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return isLoopbackHost(hostname)
	default:
		return false
	}
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || host == "[::1]"
}

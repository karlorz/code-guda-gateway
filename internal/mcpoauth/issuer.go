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
	if u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(u.Host, "*") {
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
	if u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(u.Host, "*") {
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

// RFC 8252 permits a varying port for a registered IP-literal loopback
// redirect, while scheme, IP, path and query still match exactly.
func redirectMatches(registered, actual string) bool {
	if registered == actual {
		return true
	}
	a, e1 := url.Parse(registered)
	b, e2 := url.Parse(actual)
	if e1 != nil || e2 != nil || !ValidateRedirectURI(actual) {
		return false
	}
	return a.Scheme == "http" && b.Scheme == "http" &&
		(a.Hostname() == "127.0.0.1" || a.Hostname() == "::1") &&
		a.Hostname() == b.Hostname() && a.EscapedPath() == b.EscapedPath() && a.RawQuery == b.RawQuery
}

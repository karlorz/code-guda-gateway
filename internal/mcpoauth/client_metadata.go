package mcpoauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Metadata is fetched without ambient proxies or cookies. DNS answers are
// validated and pinned at dial time, including IPv4-mapped IPv6 addresses.
func newMetadataClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
				if err != nil || len(ips) == 0 {
					return nil, errors.New("metadata DNS lookup failed")
				}
				for _, ip := range ips {
					if !publicMetadataIP(ip) {
						return nil, errors.New("metadata address is not public")
					}
				}
				d := net.Dialer{Timeout: 3 * time.Second}
				return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          16,
		},
	}
}

func publicMetadataIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2002::/16"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func validMetadataURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path == "" || u.Path == "/" || u.User != nil || u.Fragment != "" {
		return false
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicMetadataIP(ip) {
		return false
	}
	return true
}

func (s *Server) fetchMetadataClient(clientID string) (*oauthClient, error) {
	if !validMetadataURL(clientID) {
		return nil, nil
	}
	req, err := http.NewRequest(http.MethodGet, clientID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.metadataClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || (media != "application/json" && !strings.HasSuffix(media, "+json")) {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(body) > 65536 {
		return nil, nil
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		AuthMethods  []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil
	}
	if doc.ClientID != clientID || strings.TrimSpace(doc.ClientName) == "" || len(doc.ClientName) > 256 || len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > 16 || !metadataAuthMethodAllowed(doc.AuthMethod, doc.AuthMethods) {
		return nil, nil
	}
	for _, uri := range doc.RedirectURIs {
		if !ValidateRedirectURI(uri) {
			return nil, nil
		}
	}
	return &oauthClient{ClientID: clientID, ClientName: doc.ClientName, RedirectURIs: doc.RedirectURIs}, nil
}

// ChatGPT CIMD documents prefer private_key_jwt and also advertise none.
// Token exchange still uses PKCE (code_verifier). Reject confidential-client
// methods this gateway does not implement.
func metadataAuthMethodAllowed(method string, _ []string) bool {
	switch strings.TrimSpace(method) {
	case "", "none", "private_key_jwt":
		return true
	default:
		return false
	}
}

package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"code-guda-gateway/internal/keypool"
)

type Target struct {
	BaseURL string
	Path    string
	Keys    *keypool.Pool
}

type Options struct {
	Client        *http.Client
	RetryStatuses map[int]bool
}

type Proxy struct {
	client        *http.Client
	retryStatuses map[int]bool
}

type Result struct {
	Err error
}

func New(opts Options) *Proxy {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	retryStatuses := opts.RetryStatuses
	if retryStatuses == nil {
		retryStatuses = DefaultRetryStatuses()
	}
	return &Proxy{client: client, retryStatuses: retryStatuses}
}

func DefaultRetryStatuses() map[int]bool {
	return map[int]bool{
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	}
}

func (p *Proxy) Forward(w http.ResponseWriter, r *http.Request, target Target) Result {
	if strings.TrimSpace(target.BaseURL) == "" {
		http.Error(w, "upstream base URL is not configured", http.StatusBadGateway)
		return Result{Err: fmt.Errorf("upstream base URL is not configured")}
	}
	if target.Keys == nil || target.Keys.Len() == 0 {
		http.Error(w, "upstream API keys are not configured", http.StatusBadGateway)
		return Result{Err: fmt.Errorf("upstream API keys are not configured")}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return Result{Err: err}
	}

	attempts := target.Keys.Len()
	var lastStatus int
	var lastBody []byte
	var lastHeader http.Header

	for i := 0; i < attempts; i++ {
		key, ok := target.Keys.Next()
		if !ok {
			break
		}

		resp, err := p.do(r, target, key, body)
		if err != nil {
			if i == attempts-1 {
				http.Error(w, err.Error(), http.StatusBadGateway)
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
		if p.retryStatuses[resp.StatusCode] && i < attempts-1 {
			continue
		}
		writeResponse(w, resp.StatusCode, lastHeader, lastBody)
		return Result{}
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

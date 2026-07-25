package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"code-guda-gateway/internal/providers"
)

const (
	maxTavilyDiagnosticBodyBytes = 4096
	tavilyQueryTooLongReason     = "query_too_long"
	tavilyQueryTooLongMessage    = "Query is too long. Max query length is 400 characters."
)

func tavilyAttemptDiagnostic(provider string, status int, body []byte) (reason, message string, ok bool) {
	if provider != providers.ProviderTavily || status != http.StatusBadRequest ||
		len(body) == 0 || len(body) > maxTavilyDiagnosticBodyBytes {
		return "", "", false
	}

	var payload struct {
		Detail struct {
			Error string `json:"error"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", false
	}

	normalized := strings.ToLower(strings.Join(strings.Fields(payload.Detail.Error), " "))
	if normalized != strings.ToLower(tavilyQueryTooLongMessage) &&
		normalized != "query exceeds the max query length" {
		return "", "", false
	}
	return tavilyQueryTooLongReason, tavilyQueryTooLongMessage, true
}

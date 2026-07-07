package adminweb

import "net/http"

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

type listResponse[T any] struct {
	Items  []T               `json:"items"`
	Page   map[string]int    `json:"page,omitempty"`
	Filter map[string]string `json:"filter,omitempty"`
}

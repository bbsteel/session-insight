package server

import (
	"encoding/json"
	"net/http"
)

// apiError is the stable error contract consumed by the localized frontend.
// Code is safe to map to a translation key; Detail is diagnostic context and
// must not be treated as display copy.
type apiError struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func writeAPIError(w http.ResponseWriter, status int, code string, detail ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := apiError{Code: code}
	if len(detail) > 0 {
		response.Detail = detail[0]
	}
	_ = json.NewEncoder(w).Encode(response)
}

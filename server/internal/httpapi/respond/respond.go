// Package respond centralizes HTTP response writing so the router and the
// middleware share one JSON encoder and the OAuth-style error envelope
// (detailed §9.1): {"error":"code","error_description":"..."}.
package respond

import (
	"encoding/json"
	"net/http"
)

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes the OAuth-style error envelope.
func Error(w http.ResponseWriter, status int, code, desc string) {
	JSON(w, status, map[string]string{"error": code, "error_description": desc})
}

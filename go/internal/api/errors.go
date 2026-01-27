package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorResponse represents a structured error message.
type ErrorResponse struct {
	Error string `json:"error"`          // High-level error message
	Code  string `json:"code,omitempty"` // Machine-readable code (optional)
}

// JSONError sends a structured JSON error response.
func JSONError(w http.ResponseWriter, logger *slog.Logger, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := ErrorResponse{
		Error: msg,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("failed to encode error response", "error", err)
	}
}

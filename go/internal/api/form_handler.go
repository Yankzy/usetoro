package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Yankzy/usetoro/internal/database"
)

// HandleCaptureForm handles lead form submissions from any website.
func (h *Handler) HandleCaptureForm(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path values
	website := r.PathValue("website")
	if website == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing website parameter")
		return
	}

	// Read the request body as raw JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Validate JSON
	if !json.Valid(body) {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	h.Logger.Info("Received lead form submission", "website", website, "form_data", string(body))

	// Persist to database
	lead, err := h.DB.CreateLeadForm(r.Context(), database.CreateLeadFormParams{
		Website:  website,
		FormData: body,
	})
	if err != nil {
		h.Logger.Error("Failed to store lead form", "error", err, "website", website)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"id":     lead.ID,
	}); err != nil {
		h.Logger.Error("Failed to encode success response", "error", err)
	}
}

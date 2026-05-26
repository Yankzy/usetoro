package api

import (
	"encoding/json"
	"net/http"

	"github.com/Yankzy/usetoro/internal/connectors"
)

// HandleCreateStripeSession handles POST /api/v1/financial-connections/sessions.
func (h *Handler) HandleCreateStripeSession(w http.ResponseWriter, r *http.Request) {
	if h.Stripe == nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Stripe connector not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req connectors.CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			JSONError(w, h.Logger, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	resp, err := h.Stripe.CreateSession(r.Context(), req)
	if err != nil {
		h.writeStripeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.Logger.Error("Failed to encode response", "error", err)
	}
}

// HandleGetStripeAccount handles GET /api/v1/financial-connections/accounts/{account_id}.
func (h *Handler) HandleGetStripeAccount(w http.ResponseWriter, r *http.Request) {
	if h.Stripe == nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Stripe connector not configured")
		return
	}

	accountID := r.PathValue("account_id")

	resp, err := h.Stripe.GetAccount(r.Context(), accountID)
	if err != nil {
		h.writeStripeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.Logger.Error("Failed to encode response", "error", err)
	}
}

func (h *Handler) writeStripeServiceError(w http.ResponseWriter, err error) {
	if serr, ok := err.(*connectors.StripeServiceError); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(serr.StatusCode)
		if encErr := json.NewEncoder(w).Encode(map[string]string{
			"error": serr.Message,
			"code":  serr.Code,
		}); encErr != nil {
			h.Logger.Error("Failed to encode error response", "error", encErr)
		}
		return
	}
	h.Logger.Error("Unexpected Stripe error", "error", err)
	JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
}

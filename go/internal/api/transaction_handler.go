package api

import (
	"encoding/json"
	"net/http"

	"github.com/Yankzy/usetoro/internal/erp"
)

// HandleGetUnifiedTransactions retrieves all unified transactions for a given realm.
func (h *Handler) HandleGetUnifiedTransactions(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	if realmID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing path parameter: realmId")
		return
	}

	// Optional query params
	// status := r.URL.Query().Get("status")
	// For now, FetchUnifiedTransactions just pulls everything, we can pass status later

	txns, err := h.TransactionService.FetchUnifiedTransactions(r.Context(), realmID, nil)
	if err != nil {
		h.Logger.Error("Failed to fetch unified transactions", "realm", realmID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to retrieve transactions")
		return
	}

	// Always return an array, avoid null JSON in response
	if txns == nil {
		txns = []erp.Transaction{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(txns); err != nil {
		h.Logger.Error("Failed to encode transaction response", "error", err)
	}
}

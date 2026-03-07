package api

import (
	"encoding/json"
	"net/http"

	"github.com/Yankzy/usetoro/internal/erp"
)

// HandleGetAccounts retrieves all accounts for a given realm.
func (h *Handler) HandleGetAccounts(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	if realmID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing path parameter: realmId")
		return
	}

	accounts, err := h.EntityService.FetchAccounts(r.Context(), realmID)
	if err != nil {
		h.Logger.Error("Failed to fetch accounts", "realm", realmID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to retrieve accounts")
		return
	}

	if accounts == nil {
		accounts = []erp.Account{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(accounts); err != nil {
		h.Logger.Error("Failed to encode account response", "error", err)
	}
}

// HandleGetVendors retrieves all vendors for a given realm.
func (h *Handler) HandleGetVendors(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	if realmID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing path parameter: realmId")
		return
	}

	vendors, err := h.EntityService.FetchVendors(r.Context(), realmID)
	if err != nil {
		h.Logger.Error("Failed to fetch vendors", "realm", realmID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to retrieve vendors")
		return
	}

	if vendors == nil {
		vendors = []erp.Vendor{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(vendors); err != nil {
		h.Logger.Error("Failed to encode vendor response", "error", err)
	}
}

// HandleGetCustomers retrieves all customers for a given realm.
func (h *Handler) HandleGetCustomers(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	if realmID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing path parameter: realmId")
		return
	}

	customers, err := h.EntityService.FetchCustomers(r.Context(), realmID)
	if err != nil {
		h.Logger.Error("Failed to fetch customers", "realm", realmID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to retrieve customers")
		return
	}

	if customers == nil {
		customers = []erp.Customer{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(customers); err != nil {
		h.Logger.Error("Failed to encode customer response", "error", err)
	}
}

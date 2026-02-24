package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type CorrectionPayload struct {
	AccountID string `json:"account_id"`
	VendorID  string `json:"vendor_id,omitempty"`
}

// HandleApproveTransaction allows a CPA to approve or correct an AI-proposed transaction.
func (h *Handler) HandleApproveTransaction(w http.ResponseWriter, r *http.Request) {
	txIDStr := r.PathValue("id")
	if txIDStr == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing transaction id")
		return
	}

	txUUID, err := uuid.Parse(txIDStr)
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid transaction id format")
		return
	}

	var payload CorrectionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid request body")
		return
	}

	if payload.AccountID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "account_id is required")
		return
	}

	accountUUID, err := uuid.Parse(payload.AccountID)
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid account_id format")
		return
	}

	txPgID := pgtype.UUID{Bytes: txUUID, Valid: true}

	ctx := r.Context()
	if _, err := h.Approver.GetProposedTransactionByID(ctx, txPgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			JSONError(w, h.Logger, http.StatusNotFound, "Transaction not found")
		} else {
			h.Logger.Error("Failed to fetch proposed transaction", "id", txIDStr, "error", err)
			JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	arg := database.ApproveProposedTransactionParams{
		ID:                 txPgID,
		PredictedAccountID: pgtype.UUID{Bytes: accountUUID, Valid: true},
	}

	if payload.VendorID != "" {
		vendorUUID, err := uuid.Parse(payload.VendorID)
		if err != nil {
			JSONError(w, h.Logger, http.StatusBadRequest, "Invalid vendor_id format")
			return
		}
		arg.PredictedVendorID = pgtype.UUID{Bytes: vendorUUID, Valid: true}
	}

	result, err := h.Approver.ApproveProposedTransaction(ctx, arg)
	if err != nil {
		h.Logger.Error("Failed to approve transaction", "id", txIDStr, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		return
	}

	h.Logger.Info("Transaction approved", "transaction_id", txIDStr, "new_account_id", payload.AccountID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// HandleReconcileMonth triggers the month-end reconciliation for a specific realm.
// Optional query params: start_date and end_date (YYYY-MM-DD) scope the QBO report period.
func (h *Handler) HandleReconcileMonth(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	if realmID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing realmId")
		return
	}

	q := r.URL.Query()
	startDate := q.Get("start_date")
	endDate := q.Get("end_date")

	h.Logger.Info("Triggering reconciliation", "realm_id", realmID, "start_date", startDate, "end_date", endDate)

	report, err := h.Reconciler.ReconcileMonth(r.Context(), realmID, startDate, endDate)
	if err != nil {
		h.Logger.Error("Reconciliation failed", "realm_id", realmID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Reconciliation failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(report)
}

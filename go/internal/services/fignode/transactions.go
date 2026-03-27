package fignode

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

func (h *Handler) HandleGetTransactionsBatch(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		writeError(w, 401, "UNAUTHORIZED", "Missing user context")
		return
	}

	txns, err := h.db.GetPendingFignodeTransactions(r.Context())
	if err != nil {
		h.logger.Error("Failed to get pending transactions", "error", err)
		writeError(w, 500, "INTERNAL", "Failed to get transactions")
		return
	}

	var resp []TransactionResponse
	for _, t := range txns {
		var vendor string
		if t.PredictedVendorName.Valid {
			vendor = t.PredictedVendorName.String
		}

		var dateStr string
		if t.RawDate.Valid {
			dateStr = t.RawDate.Time.Format("2006-01-02")
		}

		var aiConf float64
		if t.ConfidenceScore.Valid {
			conf, _ := t.ConfidenceScore.Float64Value()
			aiConf = conf.Float64
		}

		// Amount in DB is raw text, converting to float64
		amountVal := 0.0
		if amt, err := strconv.ParseFloat(t.RawAmount, 64); err == nil {
			amountVal = amt
		}

		resp = append(resp, TransactionResponse{
			ID:                uuid.UUID(t.ID.Bytes).String(),
			RawDescription:    t.RawDescription.String,
			Vendor:            vendor,
			Industry:          "Unknown",
			IndustryIcon:      "question",
			VendorDescription: "",
			VendorUrl:         nil,
			Location:          "Unknown",
			IsRecurring:       t.IsRecurring,
			ClientContext:     ClientContext{},
			Amount:            amountVal,
			Date:              dateStr,
			AiSuggestion:      t.PlaidCategory.String,
			AiConfidence:      aiConf,
			Status:            t.Status,
			AccountType:       "Credit Card",
			Timestamp:         dateStr,
		})
	}

	if resp == nil {
		resp = []TransactionResponse{}
	}

	writeJSON(w, 200, resp)
}

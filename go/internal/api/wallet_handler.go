package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/google/uuid"
)

type TopUpRequest struct {
	AgentDID      string `json:"agent_did"`
	MicrionAmount int64  `json:"micrion_amount"`
	ReturnURL     string `json:"return_url"`
	CustomerEmail string `json:"customer_email,omitempty"`
	Email         string `json:"email,omitempty"`
}

func (h *Handler) HandleGetWalletBalance(w http.ResponseWriter, r *http.Request) {
	entityID, ok := r.Context().Value(auth.EntityIDKey).(uuid.UUID)
	if !ok {
		JSONError(w, h.Logger, http.StatusUnauthorized, "Unauthorized: Missing or invalid token")
		return
	}

	agentDID := r.URL.Query().Get("agent_did")
	if agentDID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing agent_did query param")
		return
	}

	balance, err := h.WalletManager.GetAggregateBalance(r.Context(), agentDID)
	if err != nil {
		h.Logger.Error("Failed to get Micrion balance", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to retrieve balance")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entity_id": entityID,
		"agent_did": agentDID,
		"balance":   balance,
	})
}

func (h *Handler) HandleCreateWalletTopUp(w http.ResponseWriter, r *http.Request) {
	entityID, ok := r.Context().Value(auth.EntityIDKey).(uuid.UUID)
	if !ok {
		JSONError(w, h.Logger, http.StatusUnauthorized, "Unauthorized: Missing or invalid token")
		return
	}

	var req TopUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if req.MicrionAmount < 100000 {
		JSONError(w, h.Logger, http.StatusBadRequest, "Minimum top-up is 100,000 Micrions ($10.00 USD)")
		return
	}

	// 10,000 Micrions = $1.00 USD
	amountInDollars := float64(req.MicrionAmount) / 10000.0

	email := req.CustomerEmail
	if email == "" {
		email = req.Email
	}

	payload := map[string]interface{}{
		"user_id":           entityID.String(),
		"amount_in_dollars": amountInDollars,
		"product_name":      "Micrion Execution Tokens",
		"return_url":        req.ReturnURL,
	}
	
	if email != "" {
		payload["customer_email"] = email
	}

	body, err := json.Marshal(payload)
	if err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		return
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://python-worker:8000/v1/payments/checkout/session", bytes.NewReader(body))
	if err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		h.Logger.Error("Failed to reach Python stripe worker", "error", err)
		JSONError(w, h.Logger, http.StatusBadGateway, "Stripe service unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		h.Logger.Error("Python worker returned error", "status", resp.StatusCode, "body", string(respBody))
		JSONError(w, h.Logger, resp.StatusCode, "Stripe service error")
		return
	}

	var result struct {
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to parse response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"client_secret":  result.ClientSecret,
		"publishable_key": config.GetGlobal().StripePublishableKey,
	})
}


func (h *Handler) HandleCreatePaymentSheet(w http.ResponseWriter, r *http.Request) {
	entityID, ok := r.Context().Value(auth.EntityIDKey).(uuid.UUID)
	if !ok {
		JSONError(w, h.Logger, http.StatusUnauthorized, "Unauthorized: Missing or invalid token")
		return
	}

	var req TopUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if req.MicrionAmount < 100000 {
		JSONError(w, h.Logger, http.StatusBadRequest, "Minimum top-up is 100,000 Micrions ($10.00 USD)")
		return
	}

	amountInDollars := float64(req.MicrionAmount) / 10000.0

	payload := map[string]interface{}{
		"user_id":           entityID.String(),
		"amount_in_dollars": amountInDollars,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		return
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://python-worker:8000/v1/payments/payment-sheet", bytes.NewReader(body))
	if err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal server error")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		h.Logger.Error("Failed to reach Python stripe worker", "error", err)
		JSONError(w, h.Logger, http.StatusBadGateway, "Stripe service unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		h.Logger.Error("Python worker returned error", "status", resp.StatusCode, "body", string(respBody))
		JSONError(w, h.Logger, resp.StatusCode, "Stripe service error")
		return
	}

	var result struct {
		ClientSecret string `json:"clientSecret"`
		EphemeralKey string `json:"ephemeralKey"`
		Customer     string `json:"customer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to parse response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"clientSecret":   result.ClientSecret,
		"ephemeralKey":   result.EphemeralKey,
		"customer":       result.Customer,
		"publishableKey": config.GetGlobal().StripePublishableKey,
	})
}

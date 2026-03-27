package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
)

type TopUpRequest struct {
	AgentDID      string `json:"agent_did"`
	MicrionAmount int64  `json:"micrion_amount"`
	SuccessURL    string `json:"success_url"`
	CancelURL     string `json:"cancel_url"`
}

func (h *Handler) HandleGetWalletBalance(w http.ResponseWriter, r *http.Request) {
	entityID, ok := r.Context().Value(auth.EntityIDKey).(uuid.UUID)
	if !ok {
		JSONError(w, h.Logger, http.StatusUnauthorized, "Extracting entity_id failed")
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
		JSONError(w, h.Logger, http.StatusUnauthorized, "Extracting entity_id failed")
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

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")

	// 10,000 Micrions = $1.00 USD
	// Calculate cost in cents
	costCents := req.MicrionAmount / 100

	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Mode:               stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String("usd"),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name:        stripe.String("Micrion Execution Tokens"),
						Description: stripe.String("High-Frequency Compute Tokens for Toro Agents"),
					},
					UnitAmount: stripe.Int64(costCents),
				},
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL:        stripe.String(req.SuccessURL),
		CancelURL:         stripe.String(req.CancelURL),
		ClientReferenceID: stripe.String(entityID.String()),
	}

	// Safely inject the metadata required for the Stripe Webhook to process the purchase
	params.AddMetadata("entity_id", entityID.String())
	params.AddMetadata("agent_did", req.AgentDID)
	params.AddMetadata("micrion_amount", string(rune(req.MicrionAmount))) 

	// Create session
	s, err := session.New(params)
	if err != nil {
		h.Logger.Error("Stripe Checkout failed", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to create payment session")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"session_url": s.URL,
	})
}

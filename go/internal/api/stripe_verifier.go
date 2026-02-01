package api

import (
	"fmt"
	"net/http"

	"github.com/stripe/stripe-go/v76/webhook"
)

// StripeVerifier implements WebhookVerifier for Stripe webhooks.
type StripeVerifier struct{}

// NewStripeVerifier creates a new Stripe webhook verifier.
func NewStripeVerifier() *StripeVerifier {
	return &StripeVerifier{}
}

// ProviderName returns the provider name.
func (s *StripeVerifier) ProviderName() string {
	return "stripe"
}

// Verify validates the Stripe webhook signature and parses the event.
func (s *StripeVerifier) Verify(headers http.Header, body []byte, secret string) (*WebhookEvent, error) {
	signature := headers.Get("Stripe-Signature")
	if signature == "" {
		return nil, fmt.Errorf("missing Stripe-Signature header")
	}

	// Verify the signature using Stripe's library
	event, err := webhook.ConstructEvent(body, signature, secret)
	if err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	// Convert to generic WebhookEvent
	webhookEvent := &WebhookEvent{
		Provider: "stripe",
		ID:       event.ID,
		Type:     string(event.Type),
		RawBody:  body,
		Data:     event, // Keep the full Stripe event for backward compatibility
	}

	return webhookEvent, nil
}

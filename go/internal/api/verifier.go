package api

import (
	"fmt"
	"net/http"
	"sync"
)

// WebhookEvent represents a provider-agnostic webhook event.
type WebhookEvent struct {
	// Provider is the name of the webhook provider (e.g., "stripe", "qbo")
	Provider string
	// ID is the provider's event ID
	ID string
	// Type is the event type from the provider
	Type string
	// RawBody is the original webhook payload
	RawBody []byte
	// Data contains provider-specific event data
	Data interface{}
}

// WebhookVerifier defines the interface for provider-specific webhook verification.
type WebhookVerifier interface {
	// Verify validates the webhook signature and parses the event.
	// It takes the HTTP headers, request body, and webhook secret.
	// Returns a WebhookEvent if verification succeeds, or an error.
	Verify(headers http.Header, body []byte, secret string) (*WebhookEvent, error)

	// ProviderName returns the name of this provider (e.g., "stripe", "qbo")
	ProviderName() string
}

// VerifierRegistry manages webhook verifiers for different providers.
type VerifierRegistry struct {
	mu        sync.RWMutex
	verifiers map[string]WebhookVerifier
}

// NewVerifierRegistry creates a new verifier registry.
func NewVerifierRegistry() *VerifierRegistry {
	return &VerifierRegistry{
		verifiers: make(map[string]WebhookVerifier),
	}
}

// Register adds a verifier for a specific provider.
func (r *VerifierRegistry) Register(verifier WebhookVerifier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verifiers[verifier.ProviderName()] = verifier
}

// Get retrieves a verifier for the given provider.
func (r *VerifierRegistry) Get(provider string) (WebhookVerifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	verifier, ok := r.verifiers[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported webhook provider: %s", provider)
	}

	return verifier, nil
}

// SupportedProviders returns a list of all registered providers.
func (r *VerifierRegistry) SupportedProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]string, 0, len(r.verifiers))
	for provider := range r.verifiers {
		providers = append(providers, provider)
	}
	return providers
}

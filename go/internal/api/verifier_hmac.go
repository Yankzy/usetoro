package api

import (
	"crypto"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// HMACVerifier implements WebhookVerifier for HMAC-based webhooks (QBO, Plaid, etc.).
// This is a generic, configurable verifier that scales across multiple integrations.
type HMACVerifier struct {
	providerName    string
	signatureHeader string
	hashAlgorithm   crypto.Hash
}

// NewHMACVerifier creates a new HMAC webhook verifier.
// Example: NewHMACVerifier("qbo", "intuit-signature", crypto.SHA256)
func NewHMACVerifier(providerName, signatureHeader string, hashAlgorithm crypto.Hash) *HMACVerifier {
	return &HMACVerifier{
		providerName:    providerName,
		signatureHeader: signatureHeader,
		hashAlgorithm:   hashAlgorithm,
	}
}

// ProviderName returns the provider name.
func (h *HMACVerifier) ProviderName() string {
	return h.providerName
}

// Verify validates the HMAC webhook signature and parses the event.
func (h *HMACVerifier) Verify(headers http.Header, body []byte, secret string) (*WebhookEvent, error) {
	signature := headers.Get(h.signatureHeader)
	if signature == "" {
		return nil, fmt.Errorf("missing %s header", h.signatureHeader)
	}

	// Compute expected signature
	mac := hmac.New(h.hashAlgorithm.New, []byte(secret))
	mac.Write(body)
	expectedSignature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		return nil, fmt.Errorf("signature verification failed")
	}

	// Parse payload - handle both CloudEvents array format and legacy object format
	// QBO CloudEvents: [{"type":"qbo.account.created.v1", ...}]
	// Legacy format: {"eventNotifications": [...]}
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse payload: %w", err)
	}

	// Determine format and extract first event
	var eventMap map[string]interface{}
	switch v := payload.(type) {
	case []interface{}:
		// CloudEvents array format - use first event
		if len(v) > 0 {
			if firstEvent, ok := v[0].(map[string]interface{}); ok {
				eventMap = firstEvent
			} else {
				return nil, fmt.Errorf("invalid CloudEvents format: first element is not an object")
			}
		} else {
			return nil, fmt.Errorf("empty CloudEvents array")
		}
	case map[string]interface{}:
		// Legacy object format
		eventMap = v
	default:
		return nil, fmt.Errorf("unsupported payload format: expected array or object")
	}

	// Extract event ID and type (provider-specific logic can be added later)
	eventID := extractString(eventMap, "id", "eventId", "event_id")
	eventType := extractString(eventMap, "type", "eventType", "event_type", "name")

	// For QBO, we might not have a specific event ID in the payload
	// The webhook just says "something changed"
	if eventID == "" {
		eventID = fmt.Sprintf("%s-event-%d", h.providerName, len(body))
	}
	if eventType == "" {
		eventType = "data_change"
	}

	webhookEvent := &WebhookEvent{
		Provider: h.providerName,
		ID:       eventID,
		Type:     eventType,
		RawBody:  body,
		Data:     eventMap, // Always return the first event as a map
	}

	return webhookEvent, nil
}

// extractString tries to extract a string value from multiple possible keys.
func extractString(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := m[key]; ok {
			if str, ok := val.(string); ok {
				return str
			}
		}
	}
	return ""
}

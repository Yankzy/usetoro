package api

import (
	"crypto"
	"net/http"
	"testing"
)

func TestHMACVerifier_Verify(t *testing.T) {
	tests := []struct {
		name          string
		secret        string
		body          []byte
		signature     string
		wantErr       bool
		wantEventType string
	}{
		{
			name:      "valid QBO signature",
			secret:    "test-secret-key",
			body:      []byte(`{"eventNotifications":[{"realmId":"123456","dataChangeEvent":{"entities":[{"name":"Customer","id":"1","operation":"Create"}]}}]}`),
			signature: "Z4nW8Pf8RTZ0UAs1umqJT2AhaK+BmTZ9oUxNluw0o+w=", // Pre-computed HMAC-SHA256
			wantErr:   false,
		},
		{
			name:      "invalid signature",
			secret:    "test-secret-key",
			body:      []byte(`{"test":"data"}`),
			signature: "invalid-signature",
			wantErr:   true,
		},
		{
			name:      "missing signature",
			secret:    "test-secret-key",
			body:      []byte(`{"test":"data"}`),
			signature: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := NewHMACVerifier("qbo", "intuit-signature", crypto.SHA256)

			headers := http.Header{}
			if tt.signature != "" {
				headers.Set("intuit-signature", tt.signature)
			}

			event, err := verifier.Verify(headers, tt.body, tt.secret)

			if (err != nil) != tt.wantErr {
				t.Errorf("Verify() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if event.Provider != "qbo" {
					t.Errorf("Expected provider 'qbo', got '%s'", event.Provider)
				}
			}
		})
	}
}

func TestHMACVerifier_ProviderName(t *testing.T) {
	verifier := NewHMACVerifier("test-provider", "x-signature", crypto.SHA256)
	if verifier.ProviderName() != "test-provider" {
		t.Errorf("Expected provider name 'test-provider', got '%s'", verifier.ProviderName())
	}
}

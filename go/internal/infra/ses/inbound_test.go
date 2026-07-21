package ses

import (
	"encoding/json"
	"testing"
)

func TestExtractAliasAndDomain(t *testing.T) {
	tests := []struct {
		name           string
		email          string
		expectedAlias  string
		expectedDomain string
	}{
		{
			name:           "Standard Email",
			email:          "mark@agents.acme.com",
			expectedAlias:  "mark",
			expectedDomain: "agents.acme.com",
		},
		{
			name:           "Email with Name Prefix",
			email:          "Mark Smith <mark@agents.acme.com>",
			expectedAlias:  "mark",
			expectedDomain: "agents.acme.com",
		},
		{
			name:           "Email with Quotes and Name",
			email:          `"Mark Smith" <MARK@agents.acme.com>`,
			expectedAlias:  "mark",
			expectedDomain: "agents.acme.com",
		},
		{
			name:           "Invalid Email No At",
			email:          "markagents.acme.com",
			expectedAlias:  "",
			expectedDomain: "",
		},
		{
			name:           "Empty String",
			email:          "",
			expectedAlias:  "",
			expectedDomain: "",
		},
		{
			name:           "Only At",
			email:          "@",
			expectedAlias:  "",
			expectedDomain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, domain := ExtractAliasAndDomain(tt.email)
			if alias != tt.expectedAlias {
				t.Errorf("ExtractAliasAndDomain() alias = %v, want %v", alias, tt.expectedAlias)
			}
			if domain != tt.expectedDomain {
				t.Errorf("ExtractAliasAndDomain() domain = %v, want %v", domain, tt.expectedDomain)
			}
		})
	}
}

func TestParseInboundPayload_DirectSES(t *testing.T) {
	// A raw SES JSON payload (bypassing SNS wrapper)
	rawJSON := `{
		"notificationType": "Received",
		"mail": {
			"timestamp": "2026-07-20T10:00:00Z",
			"source": "vendor@example.com",
			"messageId": "xyz123",
			"destination": ["mark@agents.acme.com"],
			"commonHeaders": {
				"from": ["Vendor <vendor@example.com>"],
				"to": ["mark@agents.acme.com"],
				"subject": "Invoice 123",
				"date": "Mon, 20 Jul 2026 10:00:00 +0000"
			}
		}
	}`

	svc := &sesService{}
	email, err := svc.ParseInboundPayload([]byte(rawJSON))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if email.MessageID != "xyz123" {
		t.Errorf("expected messageID xyz123, got %s", email.MessageID)
	}
	if email.From != "Vendor <vendor@example.com>" {
		t.Errorf("expected from Vendor <vendor@example.com>, got %s", email.From)
	}
	if email.To != "mark@agents.acme.com" {
		t.Errorf("expected to mark@agents.acme.com, got %s", email.To)
	}
	if email.Subject != "Invoice 123" {
		t.Errorf("expected subject Invoice 123, got %s", email.Subject)
	}
}

func TestParseInboundPayload_SNSWrapped(t *testing.T) {
	// An SES JSON payload wrapped inside an SNS notification
	innerJSON := `{
		"notificationType": "Received",
		"mail": {
			"timestamp": "2026-07-20T10:00:00Z",
			"source": "vendor@example.com",
			"messageId": "xyz123",
			"destination": ["mark@agents.acme.com"],
			"commonHeaders": {
				"from": ["Vendor <vendor@example.com>"],
				"to": ["mark@agents.acme.com"],
				"subject": "Invoice 123",
				"date": "Mon, 20 Jul 2026 10:00:00 +0000"
			}
		}
	}`

	snsWrapper := SESNotification{
		Type:      "Notification",
		MessageId: "sns-123",
		Message:   innerJSON,
	}

	payload, _ := json.Marshal(snsWrapper)

	svc := &sesService{}
	email, err := svc.ParseInboundPayload(payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if email.MessageID != "xyz123" {
		t.Errorf("expected messageID xyz123, got %s", email.MessageID)
	}
}

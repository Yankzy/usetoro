package ses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SESNotification models the top-level SNS wrapper if we use SNS->HTTPS
type SESNotification struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
}

// SESMessage represents the actual SES inbound email payload within the SNS Message
type SESMessage struct {
	NotificationType string `json:"notificationType"`
	Mail             struct {
		Timestamp        time.Time `json:"timestamp"`
		Source           string    `json:"source"`
		MessageId        string    `json:"messageId"`
		Destination      []string  `json:"destination"`
		HeadersTruncated bool      `json:"headersTruncated"`
		Headers          []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		CommonHeaders struct {
			ReturnPath string   `json:"returnPath"`
			From       []string `json:"from"`
			Date       string   `json:"date"`
			To         []string `json:"to"`
			MessageId  string   `json:"messageId"`
			Subject    string   `json:"subject"`
		} `json:"commonHeaders"`
	} `json:"mail"`
	Content string `json:"content"` // Base64 encoded raw MIME, if configured
}

// ParseInboundPayload parses the incoming AWS SES webhook/SNS payload
func (s *sesService) ParseInboundPayload(payload []byte) (*InboundEmail, error) {
	var sns SESNotification
	if err := json.Unmarshal(payload, &sns); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SNS wrapper: %w", err)
	}

	// For raw webhooks, the payload might just be SESMessage directly. Let's handle both.
	var msg SESMessage
	if sns.Message != "" {
		if err := json.Unmarshal([]byte(sns.Message), &msg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal SES message from SNS: %w", err)
		}
	} else {
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal direct SES message: %w", err)
		}
	}

	if len(msg.Mail.Destination) == 0 {
		return nil, fmt.Errorf("no destination found in inbound payload")
	}

	var from string
	if len(msg.Mail.CommonHeaders.From) > 0 {
		from = msg.Mail.CommonHeaders.From[0]
	} else {
		from = msg.Mail.Source
	}

	// We'd typically parse the raw MIME content here if we requested it from SES.
	// For now, we will extract the basic headers.
	email := &InboundEmail{
		MessageID: msg.Mail.MessageId,
		From:      from,
		To:        msg.Mail.Destination[0],
		Subject:   msg.Mail.CommonHeaders.Subject,
		Date:      msg.Mail.Timestamp,
	}

	// A fully featured parser like 'enmime' would be used to parse msg.Content into TextBody/HtmlBody

	return email, nil
}

// ExtractAliasAndDomain splits an email like "mark@agents.acme.com" into "mark" and "agents.acme.com"
func ExtractAliasAndDomain(emailAddress string) (alias, domain string) {
	emailAddress = strings.TrimSpace(emailAddress)
	if idx := strings.LastIndex(emailAddress, "<"); idx >= 0 {
		emailAddress = strings.TrimSuffix(strings.TrimSpace(emailAddress[idx+1:]), ">")
	}

	parts := strings.SplitN(emailAddress, "@", 2)
	if len(parts) != 2 {
		return "", ""
	}

	return strings.ToLower(parts[0]), strings.ToLower(parts[1])
}

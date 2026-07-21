package ses

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SendAgentReply constructs a MIME payload and dispatches it via SES
func (s *sesService) SendAgentReply(ctx context.Context, req SendAgentReplyRequest) error {
	fromAddress := fmt.Sprintf("%s@%s", req.FromAlias, req.DomainName)
	returnPath := "bounces.usetoro.io" // Our globally verified Return-Path domain for SPF

	// Create a simple Raw email with headers
	// In production, we'd use a robust MIME generation library like 'gomail' or 'enmime'
	// to properly handle multipart/alternative (text/html) and attachments.

	var rawEmail bytes.Buffer
	
	// Headers
	rawEmail.WriteString(fmt.Sprintf("From: %s\r\n", fromAddress))
	rawEmail.WriteString(fmt.Sprintf("To: %s\r\n", req.To))
	rawEmail.WriteString(fmt.Sprintf("Subject: %s\r\n", req.Subject))
	
	if req.InReplyTo != "" {
		rawEmail.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", req.InReplyTo))
	}
	if req.References != "" {
		rawEmail.WriteString(fmt.Sprintf("References: %s\r\n", req.References))
	}
	
	rawEmail.WriteString("MIME-Version: 1.0\r\n")
	
	// If both HTML and Text exist, we'd use multipart.
	// For simplicity in this implementation plan, we assume Text.
	if req.HtmlBody != "" {
		rawEmail.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		rawEmail.WriteString(req.HtmlBody)
	} else {
		rawEmail.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		rawEmail.WriteString(req.TextBody)
	}

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(fromAddress),
		Destination: &types.Destination{
			ToAddresses: []string{req.To},
		},
		FeedbackForwardingEmailAddress: aws.String(returnPath), // SES uses this for Return-Path
		Content: &types.EmailContent{
			Raw: &types.RawMessage{
				Data: rawEmail.Bytes(),
			},
		},
	}

	_, err := s.client.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send SES email from %s: %w", fromAddress, err)
	}

	return nil
}

// cleanMessageID strips brackets and domain from an SMTP Message-ID
func cleanMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}

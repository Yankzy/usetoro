package ses

import (
	"time"
)

type InboundEmail struct {
	MessageID   string
	From        string
	To          string
	Subject     string
	TextBody    string
	HtmlBody    string
	Date        time.Time
}

type SendAgentReplyRequest struct {
	FromAlias    string // e.g. "mark"
	DomainName   string // e.g. "agents.acme.com"
	To           string
	Subject      string
	TextBody     string
	HtmlBody     string
	InReplyTo    string
	References   string
}

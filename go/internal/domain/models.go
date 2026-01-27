package domain

// Secret represents a sensitive value (e.g., Stripe Signing Secret).
type Secret string

// WebhookPayload represents the raw body of an incoming webhook.
type WebhookPayload []byte

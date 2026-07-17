package mailpool

import (
	"context"
	"net/http"
)

// Mailpool is the typed HTTP client for the Mailpool API
type Mailpool struct {
	*ClientWithResponses
}

// WithAuthorization returns a ClientOption that injects the authorization header.
func WithAuthorization(token string) ClientOption {
	return func(c *Client) error {
		c.RequestEditors = append(c.RequestEditors, func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Api-Authorization", token)
			return nil
		})
		return nil
	}
}

// NewMailpool initializes a new Mailpool API client wrapping the generated ClientWithResponses
func NewMailpool(baseURL, apiKey string) (*Mailpool, error) {
	if baseURL == "" {
		baseURL = "https://app.mailpool.io/v1/api"
	}

	c, err := NewClientWithResponses(
		baseURL,
		WithAuthorization(apiKey),
	)
	if err != nil {
		return nil, err
	}

	return &Mailpool{
		ClientWithResponses: c,
	}, nil
}

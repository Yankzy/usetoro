package store

import (
	"context"
	"time"
)

// GetWebhookSecret returns the webhook secret for a connection.
// It uses a Cache-Aside pattern (Cache -> DB -> Cache).
func (s *Store) GetWebhookSecret(ctx context.Context, connID string) (string, error) {
	cacheKey := "secret:" + connID

	// L1: Check Ristretto Cache
	if val, found := s.Cache.Get(cacheKey); found {
		return val.(string), nil
	}

	// L2: Check Database (Cold Path)
	secret, err := s.Queries.GetWebhookSecret(ctx, connID)
	if err != nil {
		return "", err
	}

	// Cache for 5 minutes.
	s.Cache.SetWithTTL(cacheKey, secret, 1, 5*time.Minute)
	return secret, nil
}

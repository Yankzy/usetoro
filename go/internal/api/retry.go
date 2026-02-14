package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// WebhookRetryData holds webhook data for retry attempts
type WebhookRetryData struct {
	Provider          string
	ConnID            string
	ToroEventID       string
	ProviderEventID   string
	ProviderEventType string
	Body              []byte
	RequestID         string
}

// retryPublishWithBackoff attempts to publish a webhook with exponential backoff.
// This runs asynchronously in a goroutine to avoid blocking the HTTP response.
// If all retries fail, the webhook is saved to Redis for manual recovery.
func (h *Handler) retryPublishWithBackoff(data WebhookRetryData) {
	logger := h.Logger.With(
		"request_id", data.RequestID,
		"provider", data.Provider,
		"conn_id", data.ConnID,
		"toro_event_id", data.ToroEventID,
	)

	maxRetries := 5

	// Backoff schedule: 10s, 15s, 25s, 35s, 35s (total: 120s = 2 minutes)
	// This gives NATS server time to restart if needed
	backoffSchedule := []time.Duration{
		10 * time.Second, // Attempt 1
		15 * time.Second, // Attempt 2
		25 * time.Second, // Attempt 3
		35 * time.Second, // Attempt 4
		35 * time.Second, // Attempt 5
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		delay := backoffSchedule[attempt-1]

		logger.Info("Retrying webhook publish",
			"attempt", attempt,
			"max_retries", maxRetries,
			"delay_seconds", delay.Seconds(),
		)

		time.Sleep(delay)

		// Attempt publish with 2-second timeout
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		publishStart := time.Now()

		err := h.Pub.PublishWebhookEvent(
			ctx,
			data.Provider,
			data.ConnID,
			data.ToroEventID,
			data.ProviderEventID,
			data.ProviderEventType,
			data.Body,
		)

		publishLatency := time.Since(publishStart)
		cancel()

		if err == nil {
			logger.Info("Webhook retry succeeded",
				"attempt", attempt,
				"publish_latency_ms", publishLatency.Milliseconds(),
			)
			return
		}

		logger.Warn("Webhook retry failed",
			"attempt", attempt,
			"error", err,
			"publish_latency_ms", publishLatency.Milliseconds(),
		)
	}

	// All retries exhausted - save to Redis for manual recovery
	logger.Error("Webhook publish failed after max retries - saving to Redis",
		"max_retries", maxRetries,
		"provider_event_id", data.ProviderEventID,
	)

	if err := h.saveFailedWebhookToRedis(data); err != nil {
		logger.Error("Failed to save webhook to Redis dead-letter queue",
			"error", err,
			"provider_event_id", data.ProviderEventID,
		)
	} else {
		logger.Info("Webhook saved to Redis dead-letter queue",
			"provider_event_id", data.ProviderEventID,
			"redis_key", fmt.Sprintf("webhook:failed:%s:%s", data.Provider, data.ToroEventID),
		)
	}
}

// saveFailedWebhookToRedis saves a failed webhook to Redis for manual recovery.
// Key format: webhook:failed:{provider}:{toro_event_id}
// Expires after 7 days to prevent unbounded growth.
func (h *Handler) saveFailedWebhookToRedis(data WebhookRetryData) error {
	if h.Redis == nil {
		return fmt.Errorf("redis client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("webhook:failed:%s:%s", data.Provider, data.ToroEventID)

	// Create JSON payload with metadata
	payload := map[string]interface{}{
		"provider":            data.Provider,
		"conn_id":             data.ConnID,
		"toro_event_id":       data.ToroEventID,
		"provider_event_id":   data.ProviderEventID,
		"provider_event_type": data.ProviderEventType,
		"request_id":          data.RequestID,
		"body":                string(data.Body),
		"failed_at":           time.Now().Unix(),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	// Save with 7-day expiration
	if err := h.Redis.Set(ctx, key, jsonData, 7*24*time.Hour).Err(); err != nil {
		return fmt.Errorf("failed to save to redis: %w", err)
	}

	// Add to sorted set for easy querying (score = timestamp)
	scoreKey := fmt.Sprintf("webhook:failed:%s:index", data.Provider)
	if err := h.Redis.ZAdd(ctx, scoreKey, redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: key,
	}).Err(); err != nil {
		return fmt.Errorf("failed to add to sorted set: %w", err)
	}

	return nil
}

package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

// WebhooksWorker executes external HTTP webhooks driven by the Orchestrator.
type WebhooksWorker struct {
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
	client *http.Client
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewWebhooksWorker(deps.Queue, deps.Logger, deps.Config)
	})
}

// NewWebhooksWorker creates a new worker for executing HTTP requests.
func NewWebhooksWorker(nc *nats.Conn, logger *slog.Logger, cfg *config.Config) (*WebhooksWorker, error) {
	return &WebhooksWorker{
		nc:     nc,
		logger: logger,
		cfg:    cfg,
		client: &http.Client{
			Timeout: 10 * time.Second, // Implements the reasonable 10s timeout requirement
		},
	}, nil
}

func (w *WebhooksWorker) Init(ctx context.Context) error {
	return nil
}

func (w *WebhooksWorker) Subscriptions() []SubscriptionConfig {
	if w.cfg == nil {
		w.logger.Error("webhooks worker: missing config, cannot derive subject")
		return nil
	}

	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	subject := workerCfg.Subject

	if subject == "" {
		if activityType == "" {
			w.logger.Error("webhooks worker: no subject or activity_type configured")
			return nil
		}
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err != nil {
			w.logger.Error("webhooks worker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		} else {
			subject = derived
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// WebhookData defines the payload expected to configure the HTTP call
type WebhookData struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Payload json.RawMessage   `json:"payload"`
	Headers map[string]string `json:"headers"`
}

func (w *WebhooksWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [DEBUG] webhooks_worker received JetStream TAP message", "topic", msg.Subject, "data_length", len(msg.Data))

	// Poison-pill guard pattern
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("webhooks worker: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// 1. Unmarshal TAP Envelope
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Warn("webhooks worker: bad proof envelope", "error", err)
		return nil
	}

	perfStr, ok := env["perf"].(string)
	if !ok {
		w.logger.Warn("webhooks worker: dropping message, missing performative")
		return nil
	}
	perf := core.Performative(perfStr)

	if !core.IsValidPerformative(perf) {
		w.logger.Warn("webhooks worker: dropping message, invalid performative", "perf_val", perfStr)
		return nil
	}

	if perf != core.ACCEPT_PROPOSAL && perf != core.INFORM {
		w.logger.Debug("webhooks worker: ignoring unsupported performative", "performative", perfStr)
		return nil
	}

	bodyBytes, _ := json.Marshal(env["body"])
	var whData WebhookData
	var foundData bool

	// Parse TaskDefinition for ACCEPT_PROPOSAL
	var taskDef core.TaskDefinition
	if err := json.Unmarshal(bodyBytes, &taskDef); err == nil && len(taskDef.Payload) > 0 {
		if unmarshalErr := json.Unmarshal(taskDef.Payload, &whData); unmarshalErr == nil {
			foundData = true
		}
	}

	// Fallback to direct proof parsing (INFORM)
	if !foundData {
		var proof struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &proof); err == nil && len(proof.Data) > 0 {
			if unmarshalErr := json.Unmarshal(proof.Data, &whData); unmarshalErr == nil {
				foundData = true
			}
		} else {
			// Fallback: Body is the exact webhook config payload
			if err := json.Unmarshal(bodyBytes, &whData); err == nil {
				foundData = true
			}
		}
	}

	if !foundData || whData.URL == "" {
		w.logger.Warn("webhooks worker: failed to extract valid webhook data or url is empty")
		return nil // terminal failure
	}

	method := whData.Method
	if method == "" {
		method = http.MethodPost
	}

	var reqBody io.Reader
	if len(whData.Payload) > 0 && string(whData.Payload) != "null" {
		reqBody = bytes.NewReader(whData.Payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, whData.URL, reqBody)
	if err != nil {
		w.logger.Error("webhooks worker: failed to create http request", "error", err)
		return nil // client-side error, terminal
	}

	for k, v := range whData.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" && reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute side effect
	resp, err := w.client.Do(req)
	if err != nil {
		w.logger.Error("webhooks worker: transient network error", "error", err)
		// Transient network failures like dial timeouts are retried via NAK
		return fmt.Errorf("transient network error during webhook execution: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	statusCode := resp.StatusCode

	// Handle transient API statuses
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable || statusCode >= 500 {
		w.logger.Warn("webhooks worker: retryable http status from external api", "status", statusCode)
		// Returning error triggers Manager Nak()
		return fmt.Errorf("retryable http status: %d", statusCode)
	}

	// Any other statuses (e.g. 200, 400, 404) are terminal from the retry perspective
	if statusCode >= 400 && statusCode < 500 {
		w.logger.Warn("webhooks worker: terminal client http error", "status", statusCode)
	}

	// Publish Completion Signaling back to Orchestrator
	cid, hasCid := env["cid"].(string)
	if hasCid && cid != "" {
		js, jsErr := w.nc.JetStream()
		if jsErr != nil {
			w.logger.Error("webhooks worker: failed to get jetstream context", "error", jsErr)
			return nil
		}

		snippet := string(respBytes)
		if len(snippet) > 2048 {
			snippet = snippet[:2048] + "... (truncated)"
		}

		proofData := map[string]interface{}{
			"status_code": statusCode,
			"response":    snippet,
		}
		proofBytes, _ := json.Marshal(proofData)

		replyEnv := map[string]interface{}{
			"id":   uuid.New().String(),
			"ts":   time.Now().UTC(),
			"src":  "did:toro:webhooks-worker",
			"dst":  workflows.OrchestratorDID,
			"perf": core.INFORM,
			"cid":  cid,
			"body": map[string]interface{}{
				"type": "proof.webhook.executed",
				"data": proofBytes,
			},
			"sig": "worker-sig",
		}
		replyBytes, _ := json.Marshal(replyEnv)

		if _, pubErr := js.Publish(workflows.OrchestratorInbox, replyBytes); pubErr != nil {
			w.logger.Error("webhooks worker: failed to notify orchestrator", "error", pubErr)
			// Return error so we retry the Orchestrator notification (will not re-fire webhook if idempotent)
			return fmt.Errorf("transient nats error publishing inform envelope: %w", pubErr)
		}

		w.logger.Info("webhooks worker: sent explicit INFORM back to Orchestrator", "cid", cid, "status", statusCode)
	}

	return nil // Clean success -> AckExplicit
}

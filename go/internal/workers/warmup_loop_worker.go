// Package workers provides the background daemons and task processors for the Toro system.
// This file implements the warmup loop which autonomously orchestrates email conversations
// to warm up domain sender reputations.
package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/nats-io/nats.go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// WarmupLoopWorker is a background service that periodically triggers organic email exchanges
// between Toro's marketing domains (managed via Mailpool) and Toro's own SES-hosted virtual agents.
// This automated exchange builds organic sender reputation and deliverability metrics for the marketing domains.
type WarmupLoopWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	ticker *time.Ticker
	stopCh chan struct{}
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &WarmupLoopWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			stopCh: make(chan struct{}),
		}, nil
	})
}

// Init configures the time ticker based on the WarmupLoopIntervalHours config
// and launches the background loop goroutine.
func (w *WarmupLoopWorker) Init(ctx context.Context) error {
	interval := time.Duration(w.cfg.WarmupLoopIntervalHours) * time.Hour
	if interval == 0 {
		interval = 6 * time.Hour
	}
	w.ticker = time.NewTicker(interval)
	go w.loop(ctx)
	w.logger.Info("warmup loop worker started", "interval", interval)
	return nil
}

// Subscriptions returns the NATS subscriptions for this worker.
// WarmupLoopWorker is entirely timer-driven and does not subscribe to any NATS subjects.
func (w *WarmupLoopWorker) Subscriptions() []SubscriptionConfig {
	return nil // No subscriptions, runs on a ticker
}

// Handle processes incoming NATS messages.
// Since Subscriptions returns nil, this method is a no-op.
func (w *WarmupLoopWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	return nil
}

// Stop gracefully terminates the background loop and cleans up the ticker.
func (w *WarmupLoopWorker) Stop() error {
	close(w.stopCh)
	if w.ticker != nil {
		w.ticker.Stop()
	}
	return nil
}

// loop runs indefinitely until the context is canceled or Stop() is called.
// It executes the warmup payload generation immediately on startup (after a brief delay),
// and then periodically according to the ticker interval.
func (w *WarmupLoopWorker) loop(ctx context.Context) {
	// Wait a bit before first execution
	time.Sleep(10 * time.Second)
	w.run(ctx)

	for {
		select {
		case <-w.ticker.C:
			w.run(ctx)
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// run performs the core warmup logic:
// 1. Selects a random marketing sender domain.
// 2. Selects a random SES-hosted agent.
// 3. Generates an organic business inquiry using the OpenAI LLM client.
// 4. Dispatches the email to the agent using the Mailpool API.
func (w *WarmupLoopWorker) run(ctx context.Context) {
	if len(w.cfg.MailpoolWarmupSenders) == 0 {
		w.logger.Warn("warmup_loop: no mailpool warmup senders configured, skipping")
		return
	}

	if w.cfg.MailpoolAPIKey == "" {
		w.logger.Warn("warmup_loop: no mailpool api key configured, skipping")
		return
	}

	// 1. Pick a random Mailpool sender
	senderEmail := w.cfg.MailpoolWarmupSenders[rand.Intn(len(w.cfg.MailpoolWarmupSenders))]

	// 2. Pick a random SES agent
	agent, err := w.db.GetRandomEnterpriseAgentAlias(ctx)
	if err != nil {
		w.logger.Error("warmup_loop: failed to get random enterprise agent", "err", err)
		return
	}
	recipientEmail := fmt.Sprintf("%s@%s", agent.AgentAlias, agent.DomainName)

	// 3. Generate a realistic email body and subject using OpenAI
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		w.logger.Warn("warmup_loop: OPENAI_API_KEY not set, skipping generation")
		return
	}
	llmClient := openai.NewClient(option.WithAPIKey(apiKey))

	prompt := fmt.Sprintf("You are an employee inquiring about accounting services. Send a short, highly realistic, organic 1-3 sentence business inquiry to %s. Return exactly a valid JSON object with 'subject' and 'body' keys and no other text.", recipientEmail)
	resp, err := llmClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("gpt-4o-mini"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(prompt),
		},
	})
	if err != nil || len(resp.Choices) == 0 {
		w.logger.Error("warmup_loop: failed to generate LLM email", "err", err)
		return
	}

	var generated struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &generated); err != nil {
		w.logger.Error("warmup_loop: failed to parse LLM json", "err", err)
		return
	}

	// 4. Send via Mailpool API
	payload := map[string]interface{}{
		"from": map[string]string{"email": senderEmail},
		"to": []map[string]string{
			{"email": recipientEmail},
		},
		"subject": generated.Subject,
		"html":    fmt.Sprintf("<p>%s</p>", generated.Body),
	}
	jsonPayload, _ := json.Marshal(payload)

	endpoint := w.cfg.MailpoolEndpoint
	if endpoint == "" {
		endpoint = "https://app.mailpool.io/v1/api"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/emails", bytes.NewBuffer(jsonPayload))
	if err != nil {
		w.logger.Error("warmup_loop: failed to create mailpool request", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.MailpoolAPIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	mailResp, err := httpClient.Do(req)
	if err != nil {
		w.logger.Error("warmup_loop: mailpool API error", "err", err)
		return
	}
	defer mailResp.Body.Close()

	if mailResp.StatusCode >= 400 {
		w.logger.Error("warmup_loop: mailpool API failed", "status", mailResp.StatusCode)
		return
	}

	w.logger.Info("warmup_loop: successfully dispatched warmup email", "from", senderEmail, "to", recipientEmail, "subject", generated.Subject)
}

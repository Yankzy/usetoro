package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
)

// OmniChatWorker handles the persistence and dispatching of outgoing chat responses.
// It implements multi-channel support (Email, WhatsApp, Slack, Discord).
type OmniChatWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	client *http.Client
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &OmniChatWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			client: &http.Client{Timeout: 10 * time.Second},
		}, nil
	})
}

func (w *OmniChatWorker) Init(ctx context.Context) error {
	return nil
}

func (w *OmniChatWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject = "proof.outgoing.chat"
	}

	group := workerCfg.Group
	if group == "" {
		group = "omni-chat-worker-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable("omni-chat-worker"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *OmniChatWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return fmt.Errorf("failed to unmarshal outgoing chat envelope: %w", err)
	}

	// We only handle INFORM performatives (completed work/proofs)
	if env.Performative != core.INFORM {
		return nil
	}

	var proof core.Proof
	if err := json.Unmarshal(env.Body, &proof); err != nil {
		return fmt.Errorf("failed to unmarshal chat proof: %w", err)
	}

	// Extract the structured response from the agent
	var response struct {
		BodyText   string `json:"body_text"`
		FromHandle string `json:"from_handle"`
		ToHandle   string `json:"to_handle"`
		Source     string `json:"source"`
		Subject    string `json:"subject"`
	}

	if err := json.Unmarshal(proof.Data, &response); err != nil {
		return fmt.Errorf("failed to unmarshal agent response data: %w", err)
	}

	// 1. Persist to the database (Read-only agents policy)
	err := w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		Source:       response.Source,
		ExternalID:   fmt.Sprintf("agent-res-%s-%d", env.ID, time.Now().Unix()),
		FromHandle:   response.FromHandle,
		ToHandle:     response.ToHandle,
		BodyText:     pgtype.Text{String: response.BodyText, Valid: true},
		StrippedText: pgtype.Text{String: response.BodyText, Valid: true},
	})

	if err != nil {
		w.logger.Error("failed to persist agent chat response", "error", err)
		// We proceed to send even if DB save fails to ensure user gets response
	}

	// 2. Dispatch to the appropriate channel
	switch response.Source {
	case "email":
		return w.sendEmail(ctx, response.ToHandle, response.FromHandle, response.Subject, response.BodyText)
	case "whatsapp":
		return w.sendWhatsApp(ctx, response.ToHandle, response.BodyText)
	case "slack":
		return w.sendSlack(ctx, response.ToHandle, response.BodyText)
	case "discord":
		return w.sendDiscord(ctx, response.ToHandle, response.BodyText)
	default:
		w.logger.Warn("unknown source for outgoing chat", "source", response.Source)
		return nil
	}
}

// sendEmail sends an outbound email using the Postmark API.
func (w *OmniChatWorker) sendEmail(ctx context.Context, to, from, subject, body string) error {
	if w.cfg.PostmarkServerToken == "" {
		return fmt.Errorf("postmark server token not configured")
	}

	if subject == "" {
		subject = "Response from Toro AI"
	}

	payload := map[string]interface{}{
		"From":          from,
		"To":            to,
		"Subject":       subject,
		"TextBody":      body,
		"MessageStream": "outbound",
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create postmark request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", w.cfg.PostmarkServerToken)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("postmark request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("postmark API error (status %d): %v", resp.StatusCode, errResp)
	}

	w.logger.Info("successfully sent outbound email via Postmark", "to", to)
	return nil
}

func (w *OmniChatWorker) sendWhatsApp(ctx context.Context, to, body string) error {
	w.logger.Info("WhatsApp channel not yet implemented", "to", to)
	return nil
}

func (w *OmniChatWorker) sendSlack(ctx context.Context, channel, body string) error {
	w.logger.Info("Slack channel not yet implemented", "channel", channel)
	return nil
}

func (w *OmniChatWorker) sendDiscord(ctx context.Context, channel, body string) error {
	w.logger.Info("Discord channel not yet implemented", "channel", channel)
	return nil
}

package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
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
	case "sms":
		return w.sendSMS(ctx, response.ToHandle, response.BodyText)
	case "slack":
		return w.sendSlack(ctx, response.ToHandle, response.BodyText)
	case "telegram":
		return w.sendTelegram(ctx, response.ToHandle, response.BodyText)
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

// sendSMS sends an outbound SMS via the Twilio Programmable SMS API.
func (w *OmniChatWorker) sendSMS(ctx context.Context, to, body string) error {
	if w.cfg.TwilioAccountSID == "" || w.cfg.TwilioAuthToken == "" {
		w.logger.Warn("SMS channel not configured (missing Twilio credentials)")
		return nil
	}
	if w.cfg.TwilioSMSNumber == "" {
		w.logger.Warn("SMS channel not configured (missing twilio_sms_number)")
		return nil
	}

	payload := map[string]interface{}{
		"From": w.cfg.TwilioSMSNumber,
		"To":   to,
		"Body": body,
	}
	return w.twilioAPIRequest(ctx, "Messages.json", payload)
}

// sendWhatsApp sends an outbound WhatsApp message via the Twilio API.
func (w *OmniChatWorker) sendWhatsApp(ctx context.Context, to, body string) error {
	if w.cfg.TwilioAccountSID == "" || w.cfg.TwilioAuthToken == "" {
		w.logger.Warn("WhatsApp channel not configured (missing Twilio credentials)")
		return nil
	}
	if w.cfg.TwilioWANumber == "" {
		w.logger.Warn("WhatsApp channel not configured (missing twilio_wa_number)")
		return nil
	}

	payload := map[string]interface{}{
		"From": fmt.Sprintf("whatsapp:%s", w.cfg.TwilioWANumber),
		"To":   fmt.Sprintf("whatsapp:%s", to),
		"Body": body,
	}
	return w.twilioAPIRequest(ctx, "Messages.json", payload)
}

// sendTelegram sends an outbound message via the Telegram Bot API.
func (w *OmniChatWorker) sendTelegram(ctx context.Context, chatID, body string) error {
	if w.cfg.TelegramBotToken == "" {
		w.logger.Warn("Telegram channel not configured (missing telegram_bot_token)")
		return nil
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", w.cfg.TelegramBotToken)
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    body,
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("telegram: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("telegram: API error (status %d): %v", resp.StatusCode, errResp)
	}

	w.logger.Info("successfully sent outbound Telegram message", "chat_id", chatID)
	return nil
}

// sendSlack sends an outbound message via the Slack Web API.
func (w *OmniChatWorker) sendSlack(ctx context.Context, channel, body string) error {
	if w.cfg.SlackBotToken == "" {
		w.logger.Warn("Slack channel not configured (missing slack_bot_token)")
		return nil
	}

	payload := map[string]interface{}{
		"channel": channel,
		"text":    body,
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("slack: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", w.cfg.SlackBotToken))

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("slack: API error (status %d): %v", resp.StatusCode, errResp)
	}

	w.logger.Info("successfully sent outbound Slack message", "channel", channel)
	return nil
}

// twilioAPIRequest is a helper for making Twilio API calls (SMS + WhatsApp).
func (w *OmniChatWorker) twilioAPIRequest(ctx context.Context, path string, payload map[string]interface{}) error {
	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/%s", w.cfg.TwilioAccountSID, path)

	formData := make(url.Values)
	for k, v := range payload {
		formData.Set(k, fmt.Sprintf("%v", v))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("twilio: create request: %w", err)
	}
	req.SetBasicAuth(w.cfg.TwilioAccountSID, w.cfg.TwilioAuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("twilio: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("twilio: API error (status %d): %v", resp.StatusCode, errResp)
	}

	w.logger.Info("successfully sent outbound Twilio message", "to", payload["To"])
	return nil
}

func (w *OmniChatWorker) sendDiscord(ctx context.Context, channel, body string) error {
	w.logger.Info("Discord channel not yet implemented", "channel", channel)
	return nil
}

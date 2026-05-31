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

	"github.com/Yankzy/usetoro/internal/agents"
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
		BodyText       string `json:"body_text"`
		FromHandle     string `json:"from_handle"`
		ToHandle       string `json:"to_handle"`
		Source         string `json:"source"`
		Subject        string `json:"subject"`
		InReplyTo      string `json:"in_reply_to"`
		SessionID      string `json:"session_id"`
		EntityID       string `json:"entity_id"`
		SlackChannelID string `json:"slack_channel_id"`
		SlackThreadTS  string `json:"slack_thread_ts"`
	}

	if err := json.Unmarshal(proof.Data, &response); err != nil {
		return fmt.Errorf("failed to unmarshal agent response data: %w", err)
	}

	// 1. Persist to the database (Read-only agents policy)
	tempExternalID := fmt.Sprintf("agent-res-%s-%d", env.ID, time.Now().Unix())
	var entityUUID pgtype.UUID
	var sessionUUID pgtype.UUID
	if response.EntityID != "" {
		_ = entityUUID.Scan(response.EntityID)
	}
	if response.SessionID != "" {
		_ = sessionUUID.Scan(response.SessionID)
	}
	err := w.db.SaveConversationSessionMessage(ctx, database.SaveConversationSessionMessageParams{
		EntityID:   entityUUID,
		Source:     response.Source,
		ExternalID: tempExternalID,
		FromHandle: response.FromHandle,
		ToHandle:   response.ToHandle,
		BodyText:   pgtype.Text{String: response.BodyText, Valid: true},
		SessionID:  sessionUUID,
		Role:       "assistant",
	})

	if err != nil {
		w.logger.Error("failed to persist agent chat response", "error", err)
		// We proceed to send even if DB save fails to ensure user gets response
	}

	// 2. Dispatch to the appropriate channel
	switch response.Source {
	case "email":
		msgID, err := w.sendEmail(ctx, response.ToHandle, response.FromHandle, response.Subject, response.BodyText, response.InReplyTo, response.SlackChannelID, response.SlackThreadTS)
		if err != nil {
			return err
		}
		if msgID != "" {
			updateErr := w.db.UpdateConversationExternalID(ctx, database.UpdateConversationExternalIDParams{
				ExternalID:   msgID,
				ExternalID_2: tempExternalID,
			})
			if updateErr != nil {
				w.logger.Error("failed to update conversation external ID with Postmark MessageID", "error", updateErr, "temp_id", tempExternalID, "real_id", msgID)
			}
		}
		return nil
	case "whatsapp":
		return w.sendWhatsApp(ctx, response.ToHandle, response.BodyText)
	case "sms":
		return w.sendSMS(ctx, response.ToHandle, response.BodyText)
	case "slack":
		return w.sendSlack(ctx, response.SlackChannelID, response.ToHandle, response.BodyText, response.SlackThreadTS, response.EntityID, response.SessionID)
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
// When slackChannelID and slackParentTs are provided, this email is part of
// a bridged Slack thread — the Postmark MessageID is used to update
// toro_threads_mappings.email_latest_message_id on success.
func (w *OmniChatWorker) sendEmail(ctx context.Context, to, from, subject, body, inReplyTo, slackChannelID, slackParentTs string) (string, error) {
	if w.cfg.PostmarkServerToken == "" {
		return "", fmt.Errorf("postmark server token not configured")
	}

	if subject == "" {
		subject = "Response from Toro AI"
	}

	if inReplyTo != "" && !strings.HasPrefix(subject, "Re:") {
		subject = "Re: " + subject
	}

	fromAddr := from
	replyTo := from

	alias, _ := parseAgentEmail(from)
	if cfg := agents.Lookup(alias); cfg != nil && cfg.Email != "" {
		if !strings.Contains(from, "@") {
			fromAddr = fmt.Sprintf(`"%s" <%s>`, from, cfg.Email)
		} else {
			fromAddr = cfg.Email
		}
		if cfg.ReplyTo != "" {
			replyTo = cfg.ReplyTo
		} else {
			if strings.Contains(cfg.Email, "@") && !strings.Contains(cfg.Email, "@cpa.") {
				replyTo = strings.Replace(cfg.Email, "@", "@cpa.", 1)
			} else {
				replyTo = cfg.Email
			}
		}
	} else if w.cfg.PostmarkSenderSignature != "" {
		if !strings.Contains(from, "@") {
			fromAddr = fmt.Sprintf(`"%s" <%s>`, from, w.cfg.PostmarkSenderSignature)
		} else {
			fromAddr = w.cfg.PostmarkSenderSignature
		}
		replyTo = w.cfg.PostmarkSenderSignature
	} else {
		fallbackEmail := "notifications@usetoro.io"
		if !strings.Contains(from, "@") {
			fromAddr = fmt.Sprintf(`"%s" <%s>`, from, fallbackEmail)
		} else {
			fromAddr = fallbackEmail
		}
		replyTo = fallbackEmail
	}

	payload := map[string]interface{}{
		"From":          fromAddr,
		"To":            to,
		"ReplyTo":       replyTo,
		"Subject":       subject,
		"TextBody":      body,
		"TrackOpens":    true,
		"TrackLinks":    "HtmlAndText",
		"MessageStream": "outbound",
	}

	if inReplyTo != "" {
		payload["Headers"] = []map[string]string{
			{"Name": "In-Reply-To", "Value": inReplyTo},
			{"Name": "References", "Value": inReplyTo},
		}
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create postmark request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", w.cfg.PostmarkServerToken)

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("postmark request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("postmark API error (status %d): %v", resp.StatusCode, errResp)
	}

	var successResp struct {
		MessageID string `json:"MessageID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&successResp); err != nil {
		return "", fmt.Errorf("failed to decode postmark success response: %w", err)
	}

	// If this email is part of a bridged Slack thread, advance the
	// email pointer so the next reply cycle can find the mapping.
	if slackChannelID != "" && slackParentTs != "" {
		_ = w.db.UpdateThreadMappingEmailMessageID(ctx, database.UpdateThreadMappingEmailMessageIDParams{
			EmailLatestMessageID: successResp.MessageID,
			SlackChannelID:       slackChannelID,
			SlackParentTs:        slackParentTs,
		})
		w.logger.Info("updated toro_threads_mappings email pointer for bridged thread",
			"slack_channel", slackChannelID,
			"new_email_message_id", successResp.MessageID,
		)
	}

	w.logger.Info("successfully sent outbound email via Postmark", "to", to, "message_id", successResp.MessageID)
	return successResp.MessageID, nil
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
// When threadTs is non-empty, the message is posted as a threaded reply.
// When threadTs is empty and slackChannelID is provided, this is a new
// top-level message — the Slack ts is parsed from the response and used
// to create a toro_threads_mappings row linking the email and Slack threads.
func (w *OmniChatWorker) sendSlack(ctx context.Context, slackChannelID, channel, body, threadTs, entityIDStr, sessionIDStr string) error {
	// Default to the global bot token
	botToken := w.cfg.SlackBotToken

	// If we have an entity ID (TenantID), try to get the mapped OAuth token
	if entityIDStr != "" {
		var entityUUID pgtype.UUID
		if err := entityUUID.Scan(entityIDStr); err == nil {
			mapping, err := w.db.GetSlackTenantMappingByTenantID(ctx, entityUUID)
			if err == nil && mapping.SlackAccessToken != "" {
				botToken = mapping.SlackAccessToken
			}
		}
	}

	if botToken == "" {
		w.logger.Warn("Slack channel not configured (no slack token available)")
		return nil
	}

	if strings.HasPrefix(channel, "slack-channel:") {
		channel = strings.TrimPrefix(channel, "slack-channel:")
	} else if strings.HasPrefix(channel, "slack-user:") {
		channel = strings.TrimPrefix(channel, "slack-user:")
	}

	payload := map[string]interface{}{
		"channel": channel,
		"text":    body,
	}
	if threadTs != "" {
		payload["thread_ts"] = threadTs
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("slack: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", botToken))

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

	// Parse the Slack response to get the message ts.
	// If this was a new top-level message (no incoming threadTs) and we have
	// entity/session info, create the thread mapping for future bridging.
	var slackResp struct {
		OK      bool   `json:"ok"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slackResp); err == nil && slackResp.TS != "" {
		if threadTs == "" && slackChannelID != "" && entityIDStr != "" {
			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(entityIDStr)
			var sessionUUID pgtype.UUID
			_ = sessionUUID.Scan(sessionIDStr)
			_ = w.db.CreateThreadMapping(ctx, database.CreateThreadMappingParams{
				ConversationID:       sessionUUID,
				TenantID:             entityUUID,
				SlackChannelID:       slackChannelID,
				SlackParentTs:        slackResp.TS,
				EmailLatestMessageID: "", // will be updated when email reply arrives
			})
			w.logger.Info("created toro_threads_mappings row for new Slack thread",
				"slack_channel", slackChannelID,
				"slack_ts", slackResp.TS,
			)
		}
	} else {
		w.logger.Warn("slack: could not parse ts from chat.postMessage response", "error", err)
	}

	w.logger.Info("successfully sent outbound Slack message", "channel", channel, "thread_ts", threadTs)
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

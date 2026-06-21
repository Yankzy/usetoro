package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// SlackEventWorker receives inbound messages from the Slack Events API
// and routes them to the general agent ingress or bridges them to email.
type SlackEventWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.Config.SlackBotToken == "" && deps.Config.SlackClientID == "" {
			return nil, nil // Slack not configured
		}
		return &SlackEventWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *SlackEventWorker) Init(ctx context.Context) error { return nil }

func (w *SlackEventWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("SlackEventWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("SlackEventWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{
			nats.Durable(durableFromSubject(subject)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		},
	}}
}

// --- Slack event structures ---

type slackPayload struct {
	Token     string          `json:"token"`
	Challenge string          `json:"challenge"`
	Type      string          `json:"type"`
	TeamID    string          `json:"team_id"`
	Event     json.RawMessage `json:"event"`
	EventID   string          `json:"event_id"`
	EventTime int64           `json:"event_time"`
}

type slackMessageEvent struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	Channel  string `json:"channel"`
	User     string `json:"user"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	EventTS  string `json:"event_ts"`
	BotID    string `json:"bot_id"`
}

func (w *SlackEventWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("slack: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload slackPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("slack: failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	// URL verification is handled synchronously by the HTTP handler; ack here.
	if payload.Type == "url_verification" {
		w.logger.Debug("slack: url_verification challenge (already handled by HTTP)")
		msg.Ack()
		return nil
	}

	// Handle uninstall/revoke events to remove mapping
	if payload.Type == "app_uninstalled" || payload.Type == "tokens_revoked" {
		w.logger.Info("slack: received uninstall/revoke event", "team_id", payload.TeamID)
		_ = w.db.DeleteSlackTenantMappingByTeamID(ctx, payload.TeamID)
		msg.Ack()
		return nil
	}

	// Only process event callbacks
	if payload.Type != "event_callback" {
		w.logger.Debug("slack: skipping non-event payload", "type", payload.Type)
		msg.Ack()
		return nil
	}

	var event slackMessageEvent
	if err := json.Unmarshal(payload.Event, &event); err != nil {
		w.logger.Error("slack: failed to unmarshal inner event", "error", err)
		msg.Ack()
		return nil
	}

	// Skip non-message events and bot messages (including our own)
	if event.Type != "message" && event.Type != "app_mention" {
		msg.Ack()
		return nil
	}
	if event.Subtype == "bot_message" || event.BotID != "" {
		msg.Ack()
		return nil
	}
	if event.Text == "" {
		msg.Ack()
		return nil
	}

	fromHandle := fmt.Sprintf("slack-user:%s", event.User)
	toHandle := fmt.Sprintf("slack-channel:%s", event.Channel)
	externalID := fmt.Sprintf("slack-%s-%s", event.Channel, event.TS)

	// If this is a threaded reply to an existing Slack thread, check whether
	// the thread is bridged to an email conversation (Flow 2).
	if event.ThreadTS != "" {
		mapping, err := w.db.GetThreadMappingBySlackTS(ctx, database.GetThreadMappingBySlackTSParams{
			SlackChannelID: event.Channel,
			SlackParentTs:  event.ThreadTS,
		})
		if err == nil && mapping.SlackChannelID != "" {
			return w.handleBridgedReply(ctx, msg, event, mapping)
		}
	}

	// Standalone Slack message — persist and route to the general agent.
	// We need to resolve the Tenant ID from the Slack Team ID mapping.
	var entityID pgtype.UUID
	tenantMap, err := w.db.GetSlackTenantMappingByTeamID(ctx, payload.TeamID)
	if err != nil {
		w.logger.Error("slack: could not find tenant mapping for team_id", "team_id", payload.TeamID, "error", err)
		msg.Ack() // Drop the message if it comes from an unmapped/unauthorized workspace
		return nil
	}
	entityID = tenantMap.TenantID

	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   entityID,
		Source:     "slack",
		ExternalID: externalID,
		FromHandle: fromHandle,
		ToHandle:   toHandle,
		BodyText:   pgtype.Text{String: event.Text, Valid: true},
	})

	w.logger.Info("slack: saved inbound message", "channel", event.Channel, "user", event.User, "tenant_id", entityID)

	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("slack: failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      event.Text,
		"entity_id":   entityID,
		"from_handle": fromHandle,
		"to_handle":   toHandle,
		"source":      "slack",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("slack: failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("slack: routed to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}

// handleBridgedReply processes a Slack message that is a reply inside an
// already-bridged thread (PRD Flow 2). It sends an outbound email to the
// original email participant with proper threading headers.
func (w *SlackEventWorker) handleBridgedReply(
	ctx context.Context,
	msg *nats.Msg,
	event slackMessageEvent,
	mapping database.ToroCoreToroThreadsMapping,
) error {
	externalID := fmt.Sprintf("slack-%s-%s", event.Channel, event.TS)

	// Look up the original conversation to get the email participant and subject.
	// The conversations row linked at thread-creation time has the email metadata.
	conversationRows, err := w.db.GetSessionConversations(ctx, mapping.ConversationID)
	var entityID pgtype.UUID
	entityID = mapping.TenantID

	if err == nil {
		for _, conv := range conversationRows {
			if conv.Source == "email" {
				// Found the originating email conversation row
				fromHandle := fmt.Sprintf("slack-user:%s", event.User)
				toHandle := fmt.Sprintf("slack-channel:%s", event.Channel)

				// Save the inbound Slack message first
				_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
					EntityID:   entityID,
					Source:     "slack",
					ExternalID: externalID,
					FromHandle: fromHandle,
					ToHandle:   toHandle,
					BodyText:   pgtype.Text{String: event.Text, Valid: true},
				})

				// Route to OmniChatWorker to send the outbound email with
				// proper In-Reply-To/References headers for email threading.
				outProof := core.Proof{
					Type:      core.ProofAPI,
					Timestamp: time.Now().Unix(),
					Data: mustMarshalRaw(map[string]interface{}{
						"body_text":        event.Text,
						"source":           "email",
						"from_handle":      conv.ToHandle,   // from our agent email
						"to_handle":        conv.FromHandle, // reply to the original email sender
						"subject":          conv.Subject.String,
						"in_reply_to":      mapping.EmailLatestMessageID,
						"entity_id":        entityID,
						"slack_channel_id": mapping.SlackChannelID,
						"slack_parent_ts":  mapping.SlackParentTs,
					}),
				}
				proofBytes, _ := json.Marshal(outProof)

				outEnv := core.Envelope{
					ID:           uuid.New().String(),
					Timestamp:    time.Now(),
					SenderDID:    "did:toro:worker:slack_inbound",
					ReceiverDID:  "did:toro:worker:omni_chat",
					Performative: core.INFORM,
					Body:         proofBytes,
				}
				outgoingBytes, _ := json.Marshal(outEnv)
				if err := w.nc.Publish("proof.outgoing.chat", outgoingBytes); err != nil {
					w.logger.Error("slack: failed to publish bridged email to omni chat", "error", err)
					msg.Nak()
					return err
				}

				w.logger.Info("slack: bridged reply routed to email",
					"slack_channel", event.Channel,
					"email_to", conv.FromHandle,
					"in_reply_to", mapping.EmailLatestMessageID,
				)
				msg.Ack()
				return nil
			}
		}
	}

	// Fallback: conversation not found or no email row — save and route to agent
	w.logger.Warn("slack: bridged reply could not find original email conversation, routing to agent",
		"conversation_id", mapping.ConversationID,
	)
	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   entityID,
		Source:     "slack",
		ExternalID: externalID,
		FromHandle: fmt.Sprintf("slack-user:%s", event.User),
		ToHandle:   fmt.Sprintf("slack-channel:%s", event.Channel),
		BodyText:   pgtype.Text{String: event.Text, Valid: true},
	})

	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("slack: failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      event.Text,
		"entity_id":   entityID,
		"from_handle": fmt.Sprintf("slack-user:%s", event.User),
		"to_handle":   fmt.Sprintf("slack-channel:%s", event.Channel),
		"source":      "slack",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("slack: failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("slack: routed fallback to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}

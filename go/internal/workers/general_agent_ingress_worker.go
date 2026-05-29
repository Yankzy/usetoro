package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/agents"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// GeneralAgentIngressWorker handles inbound requests from the HTTP ingress
// endpoint (POST /ingress?domain=general) and from multi-channel webhook workers.
// It resolves the general-purpose agent, loads conversation session context,
// assembles full message history, and publishes the agent's response for
// outbound dispatch by the OmniChatWorker.
type GeneralAgentIngressWorker struct {
	db              *database.Queries
	logger          *slog.Logger
	cfg             *config.Config
	nc              *nats.Conn
	sessionManager  *conversation.SessionManager
	contextBuilder  *conversation.ContextBuilder
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &GeneralAgentIngressWorker{
			db:             deps.Store.Queries,
			logger:         deps.Logger,
			cfg:            deps.Config,
			nc:             deps.Queue,
			sessionManager: conversation.NewSessionManager(deps.Store.Queries, deps.Logger),
			contextBuilder: conversation.NewContextBuilder(),
		}, nil
	})
}

func (w *GeneralAgentIngressWorker) Init(ctx context.Context) error { return nil }

func (w *GeneralAgentIngressWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("GeneralAgentIngressWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("GeneralAgentIngressWorker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (w *GeneralAgentIngressWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// 1. Parse the raw body
	var req struct {
		Prompt       string `json:"prompt"`
		BodyText     string `json:"body_text"`
		EntityID     string `json:"entity_id"`
		SystemPrompt string `json:"system_prompt,omitempty"`
		AgentAlias   string `json:"agent_alias,omitempty"`
		AgentName    string `json:"agent_name,omitempty"`
		SessionID    string `json:"session_id,omitempty"`
		InReplyTo    string `json:"in_reply_to,omitempty"`
		MessageID    string `json:"message_id,omitempty"`
		FromHandle   string `json:"from_handle,omitempty"`
		ToHandle     string `json:"to_handle,omitempty"`
		Source       string `json:"source,omitempty"`
		Subject      string `json:"subject,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		w.logger.Error("ingress(general): bad payload", "error", err)
		return nil
	}
	if req.Prompt == "" {
		req.Prompt = req.BodyText
	}
	if req.Prompt == "" {
		w.logger.Error("ingress(general): missing prompt")
		return nil
	}
	if req.EntityID == "" {
		w.logger.Error("ingress(general): missing entity_id")
		return nil
	}
	if req.Source == "" {
		req.Source = "api"
	}
	if req.FromHandle == "" {
		req.FromHandle = "api-user"
	}
	if req.ToHandle == "" {
		req.ToHandle = "general-agent"
	}

	// Resolve agent config from the alias registry.
	// The caller (e.g. email worker) passes agent_alias; we map it to the
	// correct system prompt here so channel workers stay dumb pipes.
	if req.AgentAlias != "" && req.SystemPrompt == "" {
		if cfg := agents.Lookup(req.AgentAlias); cfg != nil {
			req.SystemPrompt = cfg.SystemPrompt
			req.AgentName = cfg.Name
		} else {
			w.logger.Warn("ingress(general): unknown agent alias", "alias", req.AgentAlias)
		}
	}

	var entityUUID pgtype.UUID
	_ = entityUUID.Scan(req.EntityID)

	// 2. Resolve the conversation session.
	// For email: use In-Reply-To to find the existing thread. Each new email
	// (without In-Reply-To) starts a fresh session so different topics don't
	// get merged into one thread.
	// For other channels (SMS, etc.): match by participant handle since those
	// channels don't have email-style threading headers.
	var sess database.ToroCoreConversationSession
	var isNew bool
	var err error

	if req.Source == "email" && req.InReplyTo != "" {
		cleanInReplyTo := cleanMessageID(req.InReplyTo)
		refSessionID, lookupErr := w.db.GetConversationByExternalID(ctx, cleanInReplyTo)
		if lookupErr == nil && refSessionID.Valid {
			sess, err = w.sessionManager.GetSession(ctx, refSessionID)
			if err != nil {
				w.logger.Warn("ingress(general): referenced session not found, creating new",
					"in_reply_to", req.InReplyTo,
					"session_id", uuid.UUID(refSessionID.Bytes).String(),
				)
			}
		}
	}

	if !sess.ID.Valid {
		// Email without In-Reply-To is a new thread — always create a fresh
		// session. Other channels (SMS, etc.) match by participant since they
		// don't have threading headers.
		if req.Source == "email" {
			sess, err = w.sessionManager.CreateSession(ctx, conversation.FindOrCreateParams{
				EntityID:          entityUUID,
				Source:            req.Source,
				ParticipantHandle: req.FromHandle,
				ToroHandle:        req.ToHandle,
				Subject:           req.Subject,
				SystemPrompt:      req.SystemPrompt,
			})
			isNew = true
		} else {
			sess, isNew, err = w.sessionManager.FindOrCreateSession(ctx, conversation.FindOrCreateParams{
				EntityID:          entityUUID,
				Source:            req.Source,
				ParticipantHandle: req.FromHandle,
				ToroHandle:        req.ToHandle,
				Subject:           req.Subject,
				SystemPrompt:      req.SystemPrompt,
			})
		}
		if err != nil {
			w.logger.Error("ingress(general): session lookup failed", "error", err)
		}
	}

	if isNew {
		w.logger.Info("ingress(general): created new conversation session",
			"session_id", uuidFromPG(sess.ID),
			"participant", req.FromHandle,
			"source", req.Source,
		)
	}

	// 3. Persist inbound message linked to the session
	var subj pgtype.Text
	if req.Subject != "" {
		subj = pgtype.Text{String: req.Subject, Valid: true}
	}
	var sessionID pgtype.UUID
	if sess.ID.Valid {
		sessionID = sess.ID
	}
	_ = w.db.SaveConversationSessionMessage(ctx, database.SaveConversationSessionMessageParams{
		EntityID:     entityUUID,
		Source:       req.Source,
		ExternalID:   fmt.Sprintf("ingress-%s", uuid.New().String()),
		FromHandle:   req.FromHandle,
		ToHandle:     req.ToHandle,
		Subject:      subj,
		BodyText:     pgtype.Text{String: req.Prompt, Valid: true},
		Metadata:     mustMarshalBytes(map[string]any{"session_id": req.SessionID}),
		SessionID:    sessionID,
		Role:         "user",
	})

	// 4. Assemble the full conversational context
	var promptForAgent string
	var systemPromptForAgent string
	var structuredMessages []conversation.ConversationMessage

	if sess.ID.Valid {
		history, err := w.sessionManager.GetSessionHistory(ctx, sess.ID)
		if err != nil {
			w.logger.Warn("ingress(general): failed to load session history", "error", err)
		}

		// Pass empty string for newMessage since req.Prompt is already in the loaded history
		result := w.contextBuilder.BuildContext(sess, history, "")
		promptForAgent = result.Prompt
		systemPromptForAgent = result.SystemPrompt
		structuredMessages = result.Messages

		w.logger.Info("ingress(general): assembled conversation context",
			"session_id", uuid.UUID(sess.ID.Bytes).String(),
			"history_messages", len(history),
			"structured_messages", len(structuredMessages),
			"prompt_len", len(promptForAgent),
		)
	} else {
		promptForAgent = req.Prompt
		systemPromptForAgent = req.SystemPrompt
	}

	// 5. Resolve the general agent via Almanac
	agentInbox, err := w.resolveAgentInbox()
	if err != nil {
		w.logger.Error("ingress(general): agent resolution failed", "error", err)
		return nil
	}

	// 6. Build REQUEST envelope with assembled context
	payload := map[string]any{
		"prompt":      promptForAgent,
		"entity_id":   req.EntityID,
		"from_handle": req.FromHandle,
		"to_handle":   req.ToHandle,
		"source":      req.Source,
	}
	// Include structured messages for proper multi-turn LLM input
	if len(structuredMessages) > 0 {
		payload["messages"] = structuredMessages
	}
	if req.SessionID != "" {
		payload["session_id"] = req.SessionID
	}
	if req.Subject != "" {
		payload["subject"] = req.Subject
	}
	if req.InReplyTo != "" {
		payload["in_reply_to"] = req.InReplyTo
	}

	taskDef := core.TaskDefinition{
		ID:         uuid.New().String(),
		Domain:     "general",
		Complexity: core.ComplexityEntry,
		Payload:    mustMarshalRaw(payload),
	}
	if systemPromptForAgent != "" {
		taskDef.SystemPrompt = systemPromptForAgent
	}
	taskBytes, _ := json.Marshal(taskDef)

	envlp := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:ingress",
		ReceiverDID:  "did:toro:agent:general_purpose_1",
		Performative: core.REQUEST,
		Body:         taskBytes,
	}
	envlpBytes, _ := json.Marshal(envlp)

	// 7. Publish to general agent asynchronously.
	// A NATS reply inbox lets the agent take as long as needed without
	// blocking this worker or triggering JetStream redeliveries.
	replyInbox := nats.NewInbox()
	sub, err := w.nc.Subscribe(replyInbox, func(msg *nats.Msg) {
		w.handleAgentResponse(msg.Data, entityUUID, req.Source, req.ToHandle, req.FromHandle, req.Subject, req.MessageID, sess.ID)
	})
	if err != nil {
		w.logger.Error("ingress(general): failed to subscribe to reply inbox", "error", err)
		return fmt.Errorf("subscribe reply inbox: %w", err)
	}
	sub.SetPendingLimits(1, 1024*1024)

	msgMsg := &nats.Msg{
		Subject: agentInbox,
		Reply:   replyInbox,
		Data:    envlpBytes,
		Header:  make(nats.Header),
	}
	msgMsg.Header.Set("Toro-Reply-To", replyInbox)

	if err := w.nc.PublishMsg(msgMsg); err != nil {
		sub.Unsubscribe()
		w.logger.Error("ingress(general): failed to publish to agent", "error", err)
		return fmt.Errorf("publish to agent: %w", err)
	}

	w.logger.Info("ingress(general): dispatched to agent", "reply_inbox", replyInbox)
	return nil
}

// handleAgentResponse processes the general agent's async response.
func (w *GeneralAgentIngressWorker) handleAgentResponse(data []byte, entityUUID pgtype.UUID, source, fromHandle, toHandle, subject, inReplyTo string, sessionID pgtype.UUID) {
	if len(data) == 0 {
		return // ignore empty messages on the reply inbox
	}

	var replyEnv core.Envelope
	if err := json.Unmarshal(data, &replyEnv); err != nil {
		// This can happen from internal NATS protocol messages on the inbox.
		// Silently skip — the real agent response will arrive separately.
		return
	}

	// Only process INFORM performatives (the agent's actual response)
	if replyEnv.Performative != core.INFORM {
		return
	}

	var proof core.Proof
	if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
		w.logger.Error("ingress(general): agent response is not a proof", "error", err)
		return
	}

	var output struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(proof.Data, &output)
	replyText := output.Output
	if replyText == "" {
		replyText = string(proof.Data)
	}

	ctx := context.Background()

	// 1. Mark session as awaiting reply
	if sessionID.Valid {
		if err := w.sessionManager.UpdateSession(ctx, sessionID, "", "", nil); err != nil {
			w.logger.Error("ingress(general): failed to update session", "error", err)
		}
	}

	// Publish to proof.outgoing.chat so OmniChatWorker dispatches
	outProof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data: mustMarshalRaw(map[string]any{
			"body_text":   replyText,
			"source":      source,
			"from_handle": fromHandle,
			"to_handle":   toHandle,
			"subject":     subject,
			"in_reply_to": inReplyTo,
			"session_id":  uuidFromPG(sessionID),
			"entity_id":   uuidFromPG(entityUUID),
		}),
	}
	proofBytes, _ := json.Marshal(outProof)

	outEnv := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    replyEnv.SenderDID,
		ReceiverDID:  "did:toro:worker:omni_chat",
		Performative: core.INFORM,
		Body:         proofBytes,
	}
	outBytes, _ := json.Marshal(outEnv)

	if err := w.nc.Publish("proof.outgoing.chat", outBytes); err != nil {
		w.logger.Error("ingress(general): failed to publish to proof.outgoing.chat", "error", err)
	}

	w.logger.Info("ingress(general): processed", "reply_len", len(replyText))
}

func (w *GeneralAgentIngressWorker) resolveAgentInbox() (string, error) {
	query := lookup.AlmanacQuery{
		CallerDID:      "did:toro:ingress",
		CapabilityType: "agents.general.purpose",
	}
	queryBytes, _ := json.Marshal(query)
	resp, err := w.nc.Request(core.SubjectAlmanacQuery, queryBytes, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("almanac query: %w", err)
	}
	var entries []lookup.AlmanacEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return "", fmt.Errorf("parse almanac: %w", err)
	}
	if len(entries) == 0 || len(entries[0].Endpoints) == 0 {
		return "", fmt.Errorf("general agent not found in Almanac")
	}
	return entries[0].Endpoints[0], nil
}

func mustMarshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func mustMarshalBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/agents"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"time"

	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GeneralAgentIngressWorker handles inbound requests from the HTTP ingress
// endpoint (POST /ingress?domain=general) and from multi-channel webhook workers.
// It resolves the general-purpose agent, loads conversation session context,
// assembles full message history, and publishes the agent's response for
// outbound dispatch by the OmniChatWorker.
type GeneralAgentIngressWorker struct {
	db             *database.Queries
	pool           *pgxpool.Pool
	logger         *slog.Logger
	cfg            *config.Config
	nc             *nats.Conn
	sessionManager *conversation.SessionManager
	contextBuilder *conversation.ContextBuilder
	interceptors   []conversation.IngressInterceptor
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		interceptors := []conversation.IngressInterceptor{
			ase.NewAseIngressInterceptor(deps.DBPool, deps.Queue, deps.Logger, func(domain string) ase.StatePersister {
				dt := domain_tools.Get(domain)
				if dt == nil {
					return nil
				}
				td := domain_tools.ToolDependencies{
					DBPool:  deps.DBPool,
					Redis:   deps.Redis,
					Logger:  deps.Logger,
					NC:      deps.Queue,
					Runtime: deps.Runtime,
				}
				return dt.GetStatePersister(td)
			}),
		}

		return &GeneralAgentIngressWorker{
			db:             deps.Store.Queries,
			pool:           deps.Store.Pool,
			logger:         deps.Logger,
			cfg:            deps.Config,
			nc:             deps.Queue,
			sessionManager: conversation.NewSessionManager(deps.Store.Queries, deps.Logger),
			contextBuilder: conversation.NewContextBuilder(),
			interceptors:   interceptors,
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
	// 1. Parse the raw body: Extract the prompt, handles, and identifiers from the JSON payload.
	var req conversation.IngressRequest
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

	// 2. Resolve the conversation session: Match by In-Reply-To (for email) or participant handle, creating a new session if needed.
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
		w.logger.Info("DEBUG InReplyTo matching", "raw", req.InReplyTo, "clean", cleanInReplyTo)

		// Try clean first (matches outbound emails sent via Postmark)
		refSessionID, lookupErr := w.db.GetConversationByExternalID(ctx, cleanInReplyTo)

		// Fallback to exact raw string (matches original inbound emails with brackets and domain)
		if lookupErr != nil || !refSessionID.Valid {
			w.logger.Info("DEBUG clean InReplyTo failed, trying raw string")
			refSessionID, lookupErr = w.db.GetConversationByExternalID(ctx, req.InReplyTo)
		}

		if lookupErr == nil && refSessionID.Valid {
			w.logger.Info("DEBUG InReplyTo match SUCCESS", "session_id", uuid.UUID(refSessionID.Bytes).String())
			sess, err = w.sessionManager.GetSession(ctx, refSessionID)
			if err != nil {
				w.logger.Warn("ingress(general): referenced session not found, creating new",
					"in_reply_to", req.InReplyTo,
					"session_id", uuid.UUID(refSessionID.Bytes).String(),
				)
			}
		} else {
			w.logger.Warn("DEBUG InReplyTo match FAILED", "lookupErr", lookupErr, "refSessionID.Valid", refSessionID.Valid)
		}
	}

	if !sess.ID.Valid && req.SessionID != "" {
		var parsed pgtype.UUID
		if err := parsed.Scan(req.SessionID); err == nil {
			sess, err = w.sessionManager.GetSession(ctx, parsed)
			if err != nil {
				w.logger.Warn("ingress(general): explicitly provided session_id not found, falling back", "session_id", req.SessionID)
				sess = database.ToroCoreConversationSession{} // Reset if not found
			} else {
				w.logger.Info("ingress(general): using explicitly provided session_id", "session_id", req.SessionID)
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

	// 3. Persist inbound message: Save the message in the database linked to the resolved session.
	var subj pgtype.Text
	if req.Subject != "" {
		subj = pgtype.Text{String: req.Subject, Valid: true}
	}
	var sessionID pgtype.UUID
	if sess.ID.Valid {
		sessionID = sess.ID
	}

	if req.MessageID == "" {
		_ = w.db.SaveConversationSessionMessage(ctx, database.SaveConversationSessionMessageParams{
			EntityID:   entityUUID,
			Source:     req.Source,
			ExternalID: req.MessageID,
			FromHandle: req.FromHandle,
			ToHandle:   req.ToHandle,
			Subject:    subj,
			BodyText:   pgtype.Text{String: req.Prompt, Valid: true},
			Metadata:   mustMarshalBytes(map[string]any{"session_id": req.SessionID}),
			SessionID:  sessionID,
			Role:       "user",
		})
	}

	// 3.5 Update the raw inbound message with the resolved session_id
	// Raw inbound messages (saved by channel workers) initially have session_id = NULL.
	// This makes GetConversationByExternalID fail when the user replies to their own email
	// because GetConversationByExternalID strictly looks for rows where session_id IS NOT NULL.
	if req.MessageID != "" && sessionID.Valid && w.pool != nil {
		_, updateErr := w.pool.Exec(ctx, "UPDATE toro_core.conversations SET session_id = $1, role = 'user' WHERE external_id = $2", sessionID, req.MessageID)
		if updateErr != nil {
			w.logger.Warn("ingress(general): failed to update raw inbound message session_id", "error", updateErr, "message_id", req.MessageID)
		} else {
			w.logger.Info("ingress(general): linked raw inbound message to session", "message_id", req.MessageID, "session_id", uuid.UUID(sessionID.Bytes).String())
		}
	}

	// 4. Assemble conversational context: Build the prompt using the session history and system prompt.
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

	// 5. Handle special instructions: Use interceptors and handle email reply context.
	isIntercepted := false
	for _, interceptor := range w.interceptors {
		bypassed, extraPrompt, err := interceptor.Intercept(ctx, &req)
		if err != nil {
			w.logger.Error("ingress(general): interceptor failed", "error", err)
			continue
		}
		if bypassed {
			return nil // Bypass AI completely
		}
		if extraPrompt != "" {
			systemPromptForAgent += extraPrompt
			isIntercepted = true
		}
	}

	if !isIntercepted && req.Source == "email" && req.InReplyTo != "" {
		systemPromptForAgent += "\n\n[SYSTEM NOTE: This message is a direct reply to a previous email in this conversation.]"
	}

	// 6. Route to General Agent: Dispatch the task to the generic agent via NATS and await the response.
	agentInbox, err := w.resolveAgentInbox()
	if err != nil {
		w.logger.Error("ingress(general): agent resolution failed", "error", err)
		return nil
	}

	payload := map[string]any{
		"prompt":      promptForAgent,
		"entity_id":   req.EntityID,
		"from_handle": req.FromHandle,
		"to_handle":   req.ToHandle,
		"source":      req.Source,
	}
	if len(structuredMessages) > 0 {
		payload["messages"] = structuredMessages
	}
	if sess.ID.Valid {
		payload["session_id"] = uuidFromPG(sess.ID)
	} else if req.SessionID != "" {
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
		return
	}

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

	if sessionID.Valid {
		if err := w.sessionManager.UpdateSession(ctx, sessionID, "", "", nil); err != nil {
			w.logger.Error("ingress(general): failed to update session", "error", err)
		}
	}

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
	client := lookup.New(w.nc, "did:toro:ingress")
	entries, err := client.FindAgents("agents.general.purpose", 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("almanac query: %w", err)
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

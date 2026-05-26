package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
		SessionID    string `json:"session_id,omitempty"`
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

	var entityUUID pgtype.UUID
	_ = entityUUID.Scan(req.EntityID)

	// 2. Find or create a conversation session for this participant
	sess, isNew, err := w.sessionManager.FindOrCreateSession(ctx, conversation.FindOrCreateParams{
		EntityID:          entityUUID,
		Source:            req.Source,
		ParticipantHandle: req.FromHandle,
		ToroHandle:        req.ToHandle,
		Subject:           req.Subject,
		SystemPrompt:      req.SystemPrompt,
	})
	if err != nil {
		w.logger.Error("ingress(general): session lookup failed", "error", err)
		// Fall back to stateless behavior — persist and proceed
	} else if isNew {
		w.logger.Info("ingress(general): created new conversation session",
			"session_id", uuid.UUID(sess.ID.Bytes).String(),
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
	})

	// 4. Assemble the full conversational context
	var promptForAgent string
	var systemPromptForAgent string

	if sess.ID.Valid {
		history, err := w.sessionManager.GetSessionHistory(ctx, sess.ID)
		if err != nil {
			w.logger.Warn("ingress(general): failed to load session history", "error", err)
		}

		result := w.contextBuilder.BuildContext(sess, history, req.Prompt)
		promptForAgent = result.Prompt
		systemPromptForAgent = result.SystemPrompt

		w.logger.Info("ingress(general): assembled conversation context",
			"session_id", uuid.UUID(sess.ID.Bytes).String(),
			"history_messages", len(history),
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
	if req.SessionID != "" {
		payload["session_id"] = req.SessionID
	}
	if req.Subject != "" {
		payload["subject"] = req.Subject
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

	// 7. Send to general agent (NATS request-reply, 5 min timeout)
	resp, err := w.nc.Request(agentInbox, envlpBytes, 5*time.Minute)
	if err != nil {
		w.logger.Error("ingress(general): agent request failed", "error", err)
		return fmt.Errorf("agent request: %w", err)
	}

	// 8. Parse INFORM response
	var replyEnv core.Envelope
	if err := json.Unmarshal(resp.Data, &replyEnv); err != nil {
		w.logger.Error("ingress(general): unparseable agent response", "error", err)
		return nil
	}

	var proof core.Proof
	if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
		w.logger.Error("ingress(general): agent response is not a proof", "error", err)
		return nil
	}

	// 9. Extract response text
	var output struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(proof.Data, &output)
	replyText := output.Output
	if replyText == "" {
		replyText = string(proof.Data)
	}

	// 10. Persist agent response linked to the session
	_ = w.db.SaveConversationSessionMessage(ctx, database.SaveConversationSessionMessageParams{
		EntityID:   entityUUID,
		Source:     req.Source,
		ExternalID: fmt.Sprintf("agent-res-%s", uuid.New().String()),
		FromHandle: req.ToHandle,
		ToHandle:   req.FromHandle,
		BodyText:   pgtype.Text{String: replyText, Valid: true},
		Metadata:   mustMarshalBytes(map[string]any{"session_id": req.SessionID}),
		SessionID:  sessionID,
	})

	// 11. Update session last_activity_at (and optionally status)
	if sess.ID.Valid {
		_ = w.sessionManager.UpdateSession(ctx, sess.ID, "", "", nil)
	}

	// 12. Publish to proof.outgoing.chat so OmniChatWorker dispatches
	outProof := core.Proof{
		TaskID:    taskDef.ID,
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      mustMarshalRaw(map[string]any{"body_text": replyText, "source": req.Source, "from_handle": req.ToHandle, "to_handle": req.FromHandle}),
	}
	proofBytes, _ := json.Marshal(outProof)

	outEnv := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:agent:general_purpose_1",
		ReceiverDID:  "did:toro:worker:omni_chat",
		Performative: core.INFORM,
		Body:         proofBytes,
	}
	outBytes, _ := json.Marshal(outEnv)

	if err := w.nc.Publish("proof.outgoing.chat", outBytes); err != nil {
		w.logger.Error("ingress(general): failed to publish to proof.outgoing.chat", "error", err)
	}

	w.logger.Info("ingress(general): processed", "reply_len", len(replyText), "session_id", uuid.UUID(sess.ID.Bytes).String())
	return nil
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

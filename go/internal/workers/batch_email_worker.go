package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type BatchEmailWorker struct {
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	db      *database.Queries
	runtime *agent.Runtime
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		runtime := agent.NewRuntime(deps.Logger, nil, core.AgentConfig{})
		return &BatchEmailWorker{
			logger:  deps.Logger,
			cfg:     deps.Config,
			nc:      deps.Queue,
			db:      deps.Store.Queries,
			runtime: runtime,
		}, nil
	})
}

func (w *BatchEmailWorker) Init(ctx context.Context) error {
	return nil
}

func (w *BatchEmailWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)

	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.batch_email_generation"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "batch-email-worker-group"
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

func (w *BatchEmailWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return err
	}

	if env.Performative != core.REQUEST {
		return nil
	}

	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil {
		w.logger.Error("batch_email_worker: failed to unmarshal payload", "error", err)
		return nil
	}

	if payload.SessionID == "" {
		w.logger.Error("batch_email_worker: missing session_id")
		return nil
	}

	var sUUID pgtype.UUID
	_ = sUUID.Scan(payload.SessionID)

	session, err := w.db.GetCleanupSession(ctx, sUUID)
	if err != nil {
		w.logger.Error("batch_email_worker: failed to fetch session", "error", err)
		return nil
	}

	var eUUID pgtype.UUID
	if session.RealmID.Valid {
		conn, err := w.db.GetERPConnectionByRealm(ctx, database.GetERPConnectionByRealmParams{
			ErpSystem: "QBO",
			RealmID:   session.RealmID.String,
		})
		if err == nil && conn.EntityID.Valid {
			eUUID = conn.EntityID
			w.logger.Info("batch_email_worker: found entity ID", "entity_id", uuid.UUID(eUUID.Bytes).String())
		} else {
			w.logger.Warn("batch_email_worker: failed to find ERP connection for firm entity ID lookup", "realm_id", session.RealmID.String, "error", err)
		}
	}

	holds, err := w.db.GetHeldTransactionsBySession(ctx, sUUID)
	if err != nil {
		w.logger.Error("batch_email_worker: failed to get held transactions", "error", err)
		return nil
	}

	if len(holds) == 0 {
		w.logger.Info("batch_email_worker: no held transactions for session, skipping email", "session_id", payload.SessionID)
		w.sendSuccessReply(msg, env)
		return nil
	}

	// Format prompt
	prompt := "Transactions requiring clarification:\n\n"
	for _, h := range holds {
		prompt += fmt.Sprintf("Transaction ID: %s\nDescription: %s\nAmount: %s\nReasoning: %s\n\n",
			uuid.UUID(h.TransactionID.Bytes).String(),
			h.RawDescription.String,
			h.RawAmount,
			string(h.AseExecutionTrace),
		)
	}

	systemPrompt := "You are an accounting assistant. Your task is to draft a single, professional email to the client summarizing the transactions that require clarification. For each transaction, clearly state what information or document is needed based on the reasoning provided. Return ONLY the text of the email body. Do not include any JSON or markdown code blocks."

	w.logger.Info("batch_email_worker: executing runtime for batch email", "session_id", payload.SessionID, "transactions", len(holds))
	emailBody, err := w.runtime.Exec(ctx, prompt, systemPrompt)
	if err != nil {
		w.logger.Error("batch_email_worker: failed to generate email via LLM", "error", err)
		return err
	}

	w.logger.Info("batch_email_worker: successfully generated email body")
	w.dispatchEmail(ctx, payload.SessionID, sUUID, eUUID, emailBody)

	w.sendSuccessReply(msg, env)
	return nil
}

func (w *BatchEmailWorker) dispatchEmail(ctx context.Context, sessionID string, sessionUUID pgtype.UUID, entityUUID pgtype.UUID, bodyText string) {
	toEmail := ""

	sessDB, err := w.db.GetCleanupSession(ctx, sessionUUID)
	if err == nil && sessDB.RealmID.Valid {
		companyInfo, err := w.db.GetCompanyInfo(ctx, sessDB.RealmID.String)
		if err == nil && companyInfo.Email.Valid {
			toEmail = companyInfo.Email.String
		}
	}

	if toEmail == "" {
		w.logger.Warn("batch_email_worker: could not resolve client email", "session_id", sessionID)
		return
	}

	if !entityUUID.Valid {
		_ = entityUUID.Scan(uuid.New().String()) // Fallback
		w.logger.Warn("batch_email_worker: using fallback generated entity_id", "fallback_entity_id", uuid.UUID(entityUUID.Bytes).String())
	}

	sessionManager := conversation.NewSessionManager(w.db, w.logger)
	sess, err := sessionManager.CreateSession(ctx, conversation.FindOrCreateParams{
		EntityID:          entityUUID,
		Source:            "email",
		ParticipantHandle: toEmail,
		ToroHandle:        "sarah@usetoro.io",
		Subject:           "Action Required: Clarification Needed for Recent Transactions",
		SystemPrompt:      "",
	})

	convoSessionID := sessionID
	if err == nil && sess.ID.Valid {
		convoSessionID = uuid.UUID(sess.ID.Bytes).String()
	}

	response := map[string]string{
		"body_text":     bodyText,
		"from_handle":   "sarah@usetoro.io",
		"to_handle":     toEmail,
		"source":        "email",
		"subject":       "Action Required: Clarification Needed for Recent Transactions",
		"session_id":    convoSessionID,
		"entity_id":     uuid.UUID(entityUUID.Bytes).String(),
		"custom_msg_id": fmt.Sprintf("ase:batch:%s:bookkeeping", sessionID),
	}

	proofData, _ := json.Marshal(response)
	proof := core.Proof{
		Type: "outgoing_chat",
		Data: proofData,
	}
	proofBytes, _ := json.Marshal(proof)

	envelope := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.INFORM,
		Body:         proofBytes,
	}
	envelopeBytes, _ := json.Marshal(envelope)

	if w.nc != nil {
		_ = w.nc.Publish("proof.outgoing.chat", envelopeBytes)
	}
}

func (w *BatchEmailWorker) sendSuccessReply(msg *nats.Msg, reqEnv core.Envelope) {
	if msg.Reply == "" {
		return
	}

	resp := map[string]string{
		"status": "success",
	}
	respBytes, _ := json.Marshal(resp)

	replyEnv := core.Envelope{
		ID:             uuid.New().String(),
		ConversationID: reqEnv.ConversationID,
		Performative:   core.INFORM,
		Body:           respBytes,
	}
	replyBytes, _ := json.Marshal(replyEnv)

	_ = msg.Respond(replyBytes)
}

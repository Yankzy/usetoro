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
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type PcmExportWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PcmExportWorker{
			logger: deps.Logger.With("worker", "pcm_export"),
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
		}, nil
	})
}

func (w *PcmExportWorker) Init(ctx context.Context) error {
	w.logger.Info("PcmExportWorker initialized")
	return nil
}

func (w *PcmExportWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.pcm_export"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			subject = "worker.inbox.pcm_export"
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "worker-inbox-pcm_export-group"
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

func (w *PcmExportWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error("PcmExportWorker: failed to unmarshal envelope", "error", err)
		return err
	}

	if env.Performative != core.REQUEST {
		return nil
	}

	var payload struct {
		SessionID   string                   `json:"session_id"`
		FromHandle  string                   `json:"from_handle"`
		ToHandle    string                   `json:"to_handle"`
		Attachments []map[string]interface{} `json:"attachments"`
	}

	if err := json.Unmarshal(env.Body, &payload); err != nil {
		w.logger.Error("PcmExportWorker: failed to unmarshal body", "error", err)
		return err
	}

	if payload.SessionID == "" {
		w.logger.Error("PcmExportWorker: missing session_id")
		return fmt.Errorf("missing session_id")
	}

	w.logger.Info("PcmExportWorker: processing export", "session_id", payload.SessionID)

	toHandle := payload.ToHandle
	fromHandle := payload.FromHandle

	// Resolve missing handles from the conversation session if not explicitly provided
	if (toHandle == "" || fromHandle == "") && payload.SessionID != "" {
		if sessionUUID, err := uuid.Parse(payload.SessionID); err == nil {
			if sess, err := w.db.GetConversationSession(ctx, pgtype.UUID{Bytes: sessionUUID, Valid: true}); err == nil {
				if toHandle == "" {
					toHandle = sess.ParticipantHandle
				}
				if fromHandle == "" {
					fromHandle = sess.ToroHandle
				}
			}
		}
	}

	response := map[string]interface{}{
		"body_text":     "Hello,\n\nPlease find the Sage 100 (.PNM) Moroccan Bank Reconciliation import file attached.\n\nBest regards,\nAccounting Bot",
		"subject":       "Sage 100 PNM Export - Bank Reconciliation",
		"session_id":    payload.SessionID,
		"from_handle":   fromHandle,
		"to_handle":     toHandle,
		"source":        "email",
		"custom_msg_id": fmt.Sprintf("ase:pcm_export:%s", payload.SessionID),
	}

	if len(payload.Attachments) == 0 {
		w.logger.Error("pcm_export: no attachments/data found for export payload, skipping email dispatch", "session_id", payload.SessionID)
		return nil
	}
	response["attachments"] = payload.Attachments


	proofData, _ := json.Marshal(response)
	proof := core.Proof{
		Type: "outgoing_chat",
		Data: proofData,
	}

	proofBytes, _ := json.Marshal(proof)

	envelope := core.Envelope{
		ID:             uuid.New().String(),
		ConversationID: env.ConversationID,
		Performative:   core.INFORM,
		Body:           proofBytes,
	}
	envelopeBytes, _ := json.Marshal(envelope)

	if w.nc != nil {
		if err := w.nc.Publish("proof.outgoing.chat", envelopeBytes); err != nil {
			w.logger.Error("PcmExportWorker: failed to publish to proof.outgoing.chat", "error", err)
			return err
		}
	}

	w.logger.Info("PcmExportWorker: successfully triggered email dispatch")
	return nil
}

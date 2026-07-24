package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type PcmOcrWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PcmOcrWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger.With("worker", "pcm_ocr"),
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *PcmOcrWorker) Init(ctx context.Context) error {
	w.logger.Info("PcmOcrWorker initialized")
	return nil
}

func (w *PcmOcrWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "worker.inbox.pcm_ocr",
			Group:   "pcm-ocr-worker-group",
			Options: []nats.SubOpt{
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *PcmOcrWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var inboundEmail PostmarkInboundEmail
	if err := json.Unmarshal(msg.Data, &inboundEmail); err != nil {
		w.logger.Error("PcmOcrWorker: failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	w.logger.Info("PcmOcrWorker: processing email", "from", inboundEmail.From)

	// Resolve the tenant/realm
	entityID, err := w.db.GetEntityIDByEmail(ctx, inboundEmail.From)
	if err != nil || !entityID.Valid {
		w.logger.Warn("PcmOcrWorker: ignoring email from unknown user", "from", inboundEmail.From)
		msg.Ack()
		return nil
	}

	// This is where we would normally fetch the S3 attachments
	// and run OCR (e.g., AWS Textract / OpenAI Vision).
	// We simulate the OCR output matching the PRD expectations.
	w.logger.Info("PcmOcrWorker: simulated OCR extraction of attachments", "count", len(inboundEmail.Attachments))

	// Construct the PcmPayload
	ocrPayload := pcm.PcmPayload{
		EntityID: fmt.Sprintf("%x-%x-%x-%x-%x", entityID.Bytes[0:4], entityID.Bytes[4:6], entityID.Bytes[6:8], entityID.Bytes[8:10], entityID.Bytes[10:16]),
		RealmID:  "dummy_realm", // In reality, fetch from user's active tenant
	}

	// Simulated document extraction (BC, BL, Facture, Bank Settlement)
	// We'd map the OCR results into ocrPayload.Documents here
	ocrPayload.Documents = []struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}{
		{
			Type: "Facture",
			Data: map[string]interface{}{
				"vendor_name": "Iam Casablanca",
				"total_mad":   100.0,
			},
		},
	}

	// Build Envelope
	env := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:worker:pcm_ocr",
		Performative: core.REQUEST,
	}

	taskConfig := map[string]interface{}{
		"dag_name":    "pcm_bank_reconciliation",
		"domain_tool": "pcm_bank_reconciliation",
	}

	bodyData := map[string]interface{}{
		"config":  taskConfig,
		"payload": ocrPayload,
	}
	env.Body, _ = json.Marshal(bodyData)

	envBytes, _ := json.Marshal(env)

	if err := w.nc.Publish("worker.inbox.ase_bridge", envBytes); err != nil {
		w.logger.Error("PcmOcrWorker: failed to publish to ase_bridge", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("PcmOcrWorker: successfully published OCR payload to DAG")
	msg.Ack()
	return nil
}

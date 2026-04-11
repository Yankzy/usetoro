package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// CSVMappingWorker subscribes to NATS CDC events and enriches staging rows
// using the existing EntityResolver + CoAMapper AI services.
type CSVMappingWorker struct {
	db             *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	nc             *nats.Conn
	dedup          *cleanup.Deduplicator
	logger         *slog.Logger
}

// NewCSVMappingWorker creates a new worker for csv mapping ingestion and enrichment.
func NewCSVMappingWorker(
	db *database.Queries,
	entityResolver *ai.EntityResolver,
	coaMapper *ai.CoAMapper,
	nc *nats.Conn,
	logger *slog.Logger,
) (*CSVMappingWorker, error) {
	return &CSVMappingWorker{
		db:             db,
		entityResolver: entityResolver,
		coaMapper:      coaMapper,
		nc:             nc,
		dedup:          cleanup.NewDeduplicator(),
		logger:         logger,
	}, nil
}

func (e *CSVMappingWorker) Init(ctx context.Context) error {
	return nil
}

func (e *CSVMappingWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "worker.inbox.csv-mapping-worker",
			Group:   "csv-mapping-worker-group",
			Options: []nats.SubOpt{nats.Durable("csv-mapping-worker-durable-v6"), nats.DeliverAll(), nats.AckExplicit()},
		},
	}
}

func (e *CSVMappingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	e.logger.Info("📡 [DEBUG] csv_mapping_worker received JetStream TAP message", "topic", msg.Subject, "data_length", len(msg.Data))
	if err := e.handleProof(ctx, msg); err != nil {
		e.logger.Error("csv mapping worker transient error", "error", err)
		return err
	}
	return nil
}

// RawRow matches the struct output by the TAP Cleanup Agent
type RawRow struct {
	SessionID   string `json:"SessionID"`
	RealmID     string `json:"RealmID"`
	Date        string `json:"Date"`
	Description string `json:"Description"`
	Amount      string `json:"Amount"`
	Vendor      string `json:"Vendor"`
	Customer    string `json:"Customer"`
}

// handleProof processes the finalized mapping output from the AI TAP Agent.
func (e *CSVMappingWorker) handleProof(ctx context.Context, msg *nats.Msg) error {
	// 1. Unmarshal TAP Envelope
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		e.logger.Error("csv mapping worker: bad proof envelope", "error", err)
		return nil
	}

	// Check performative
	perfStr, ok := env["perf"].(string)
	perf := core.Performative(perfStr)
	if !ok || !core.IsValidPerformative(perf) {
		e.logger.Warn("csv mapping worker: dropping message, invalid performative", "perf_val", env["perf"])
		return nil
	}
	if perf != core.INFORM && perf != core.ACCEPT_PROPOSAL {
		e.logger.Warn("csv mapping worker: dropping message, perf mismatch", "perf_val", env["perf"])
		return nil
	}

	bodyBytes, _ := json.Marshal(env["body"])
	var proof struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	// First try interpreting it as a FIPA ACCEPT_PROPOSAL TaskDefinition payload
	var taskDef struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(bodyBytes, &taskDef); err == nil && len(taskDef.Payload) > 0 {
		if unmarshalErr := json.Unmarshal(taskDef.Payload, &proof); unmarshalErr != nil {
			e.logger.Error("csv mapping worker: task payload unmarshal error", "error", unmarshalErr)
			return nil
		}
	} else {
		// Fallback to direct proof parsing (INFORM)
		if err := json.Unmarshal(bodyBytes, &proof); err != nil {
			e.logger.Error("csv mapping worker: proof unmarshal error", "error", err)
			return nil
		}
	}

	// Ensure proof type is proof.api or API_CALL
	if proof.Type != "API_CALL" && proof.Type != string(core.ProofAPI) {
		e.logger.Warn("csv mapping worker: dropping message, type mismatch", "type_val", proof.Type)
		return nil // not meant for us
	}

	rows, err := ExtractRows(proof.Data)
	if err != nil {
		e.logger.Error("csv mapping worker: failed to extract rows", "error", err)
		return nil
	}

	if len(rows) == 0 {
		e.logger.Warn("csv mapping worker: proof contained 0 rows")
		return nil
	}

	// Get session/realm from the first row
	sessionID, _ := rows[0]["SessionID"].(string)
	realmID, _ := rows[0]["RealmID"].(string)

	if sessionID == "" {
		e.logger.Warn("csv mapping worker: missing sessionID in rows")
		return nil
	}

	var pgSessionID pgtype.UUID
	pgSessionID.Scan(sessionID)

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.logger.Error("csv mapping worker: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	e.logger.Info("csv mapping worker: inserting AI-mapped rows", "session_id", sessionID, "count", len(rows))

	// Update session to PROCESSING
	statusErr := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "PROCESSING",
	})
	if statusErr != nil {
		return fmt.Errorf("failed to update session status: %w", statusErr)
	}

	// Insert rows
	for _, r := range rows {
		rawDescription, _ := r["Description"].(string)
		rawAmount, _ := r["Amount"].(string)
		rawDateStr, _ := r["Date"].(string)
		vendorName, _ := r["Vendor"].(string)
		customerName, _ := r["Customer"].(string)

		var dDate pgtype.Date
		dDate.Scan(rawDateStr)

		_, err := e.db.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
			SessionID:             pgSessionID,
			RealmID:               pgtype.Text{String: realmID, Valid: realmID != ""},
			SourceType:            "CSV",
			RawDescription:        pgtype.Text{String: rawDescription, Valid: rawDescription != ""},
			RawAmount:             rawAmount,
			RawDate:               dDate,
			Status:                "PENDING", // Match EnrichmentWorker query
			PredictedVendorName:   pgtype.Text{String: vendorName, Valid: vendorName != ""},
			PredictedCustomerName: pgtype.Text{String: customerName, Valid: customerName != ""},
		})
		if err != nil {
			e.logger.Error("csv mapping worker: row insert failed", "error", err)
			return fmt.Errorf("transient db error inserting row: %w", err)
		}
	}

	e.logger.Info("csv mapping worker: fully completed TAP DB inserts. Waiting for Enrichment Agent.")

	js, jsErr := e.nc.JetStream()
	if jsErr != nil {
		e.logger.Error("csv mapping worker: failed to get jetstream context", "error", jsErr)
		return nil
	}

	// Check if this was dispatched by Orchestrator with a conversation ID
	cid, hasCid := env["cid"].(string)
	if hasCid && cid != "" {
		replyEnv := map[string]interface{}{
			"id":   uuid.New().String(),
			"ts":   time.Now().UTC(),
			"src":  "did:toro:csv-mapping-worker",
			"dst":  workflows.OrchestratorDID,
			"perf": core.INFORM,
			"cid":  cid,
			"body": map[string]interface{}{
				"type": "proof.accounting.cleanup.inserted",
				"data": msg.Data, // pass through original
			},
			"sig": "worker-sig",
		}
		replyBytes, _ := json.Marshal(replyEnv)

		if _, pubErr := js.Publish(workflows.OrchestratorInbox, replyBytes); pubErr != nil {
			e.logger.Error("csv mapping worker: failed to notify orchestrator", "error", pubErr)
		} else {
			e.logger.Info("csv mapping worker: sent explicit INFORM back to Orchestrator", "cid", cid)
		}
	} else {
		// Legacy global broadcast if not part of a guided conversation
		if _, pubErr := js.Publish("proof.accounting.cleanup.inserted", msg.Data); pubErr != nil {
			e.logger.Error("csv mapping worker: failed to publish inserted proof", "error", pubErr)
		}
	}

	return nil
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewCSVMappingWorker(deps.Store.Queries, deps.EntityResolver, deps.CoAMapper, deps.Queue, deps.Logger)
	})
}

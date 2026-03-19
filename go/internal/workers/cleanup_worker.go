package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// CleanupWorker subscribes to NATS CDC events and enriches staging rows
// using the existing EntityResolver + CoAMapper AI services.
type CleanupWorker struct {
	db             *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	nc             *nats.Conn
	dedup          *cleanup.Deduplicator
	logger         *slog.Logger
}

// NewCleanupWorker creates a new worker for cleanup ingestion and enrichment.
func NewCleanupWorker(
	db *database.Queries,
	entityResolver *ai.EntityResolver,
	coaMapper *ai.CoAMapper,
	nc *nats.Conn,
	logger *slog.Logger,
) (*CleanupWorker, error) {
	return &CleanupWorker{
		db:             db,
		entityResolver: entityResolver,
		coaMapper:      coaMapper,
		nc:             nc,
		dedup:          cleanup.NewDeduplicator(),
		logger:         logger,
	}, nil
}

func (e *CleanupWorker) Start(ctx context.Context) error {
	subject := "proof.accounting.cleanup.columns"
	e.logger.Info("🛫 CleanupWorker started listening to TAP proofs via JetStream", "subject", subject)

	js, err := e.nc.JetStream()
	if err != nil {
		return fmt.Errorf("cleanup worker failed to bind jetstream context: %w", err)
	}

	sub, err := js.QueueSubscribe(subject, "cleanup-worker-group", func(msg *nats.Msg) {
		e.logger.Info("📡 [DEBUG] cleanup_worker received JetStream TAP message", "topic", msg.Subject, "data_length", len(msg.Data))
		if err := e.handleProof(ctx, msg); err != nil {
			e.logger.Error("cleanup worker transient error", "error", err)
			msg.Nak()
			return
		}
		msg.Ack()
	}, nats.Durable("cleanup-worker-durable"), nats.DeliverAll(), nats.AckExplicit())

	if err != nil {
		return fmt.Errorf("cleanup worker TAP proof subscribe: %w", err)
	}

	<-ctx.Done()
	_ = sub.Unsubscribe()
	e.logger.Info("🛑 CleanupWorker stopped")
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
func (e *CleanupWorker) handleProof(ctx context.Context, msg *nats.Msg) error {
	// 1. Unmarshal TAP Envelope
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		e.logger.Error("cleanup worker: bad proof envelope", "error", err)
		return nil
	}

	// Double check this is an INFORM message
	perf, ok := env["perf"].(string)
	if !ok || (perf != "INFORM" && perf != "inform") {
		e.logger.Warn("cleanup worker: dropping message, perf mismatch", "perf_val", env["perf"])
		return nil
	}

	bodyBytes, _ := json.Marshal(env["body"])
	var proof struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &proof); err != nil {
		e.logger.Error("cleanup worker: proof unmarshal error", "error", err)
		return nil
	}

	// Ensure proof type is proof.api or API_CALL
	if proof.Type != "API_CALL" && proof.Type != "proof.api" {
		e.logger.Warn("cleanup worker: dropping message, type mismatch", "type_val", proof.Type)
		return nil // not meant for us
	}

	var rows []RawRow
	if err := json.Unmarshal(proof.Data, &rows); err != nil || len(rows) == 0 {
		return nil
	}

	if len(rows) == 0 {
		e.logger.Warn("cleanup worker: proof contained 0 rows")
		return nil
	}

	sessionID := rows[0].SessionID
	if sessionID == "" {
		return nil
	}
	realmID := rows[0].RealmID

	var pgSessionID pgtype.UUID
	pgSessionID.Scan(sessionID)

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.logger.Error("cleanup worker: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	e.logger.Info("cleanup worker: inserting AI-mapped rows", "session_id", sessionID, "count", len(rows))

	// Update session to PROCESSING
	err := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "PROCESSING",
	})
	if err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}

	// Insert rows
	for _, row := range rows {
		var dDate pgtype.Date
		dDate.Scan(row.Date)

		_, err := e.db.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
			SessionID:             pgSessionID,
			RealmID:               pgtype.Text{String: realmID, Valid: realmID != ""},
			SourceType:            "CSV",
			RawDescription:        pgtype.Text{String: row.Description, Valid: row.Description != ""},
			RawAmount:             row.Amount,
			RawDate:               dDate,
			PredictedVendorName:   pgtype.Text{String: row.Vendor, Valid: row.Vendor != ""},
			PredictedCustomerName: pgtype.Text{String: row.Customer, Valid: row.Customer != ""},
		})
		if err != nil {
			e.logger.Error("cleanup worker: row insert failed", "error", err)
			return fmt.Errorf("transient db error inserting row: %w", err)
		}
	}

	e.logger.Info("cleanup worker: fully completed TAP DB inserts. Waiting for Enrichment Agent.")
	return nil
}

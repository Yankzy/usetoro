// Package cleanup implements the Clean-Up Mode enrichment pipeline.
package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"
)

const (
	// enrichSubjectFilter matches "cleanup.enrich.>" for all realms.
	enrichSubjectFilter = "cleanup.enrich.>"
	enrichConsumer      = "toro-cleanup-enricher"
	enrichStream        = "CLEANUP"
	enrichBatchSize     = 50 // rows per concurrent batch
	enrichConcurrency   = 5  // parallel workers per session
)

// enrichMsg is the payload published to the cleanup.enrich.{realmID} subject.
type enrichMsg struct {
	SessionID string `json:"session_id"`
	RealmID   string `json:"realm_id"`
}

// CleanupEnricher subscribes to NATS cleanup.enrich.> and enriches staging rows
// using the existing EntityResolver + CoAMapper AI services.
type CleanupEnricher struct {
	db             *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	natsClient     *queue.Client
	dedup          *Deduplicator
	logger         *slog.Logger
	sub            *nats.Subscription
}

// NewCleanupEnricher creates a new enricher. AI services may be nil (enrichment
// will skip those layers and store a zero confidence score).
func NewCleanupEnricher(
	db *database.Queries,
	entityResolver *ai.EntityResolver,
	coaMapper *ai.CoAMapper,
	natsClient *queue.Client,
	logger *slog.Logger,
) *CleanupEnricher {
	return &CleanupEnricher{
		db:             db,
		entityResolver: entityResolver,
		coaMapper:      coaMapper,
		natsClient:     natsClient,
		dedup:          NewDeduplicator(),
		logger:         logger,
	}
}

// Start sets up the NATS JetStream stream + consumer and begins processing.
// It blocks until ctx is cancelled. Call in a goroutine.
func (e *CleanupEnricher) Start(ctx context.Context) error {
	if e.natsClient == nil {
		e.logger.Warn("cleanup enricher: NATS client is nil, enricher disabled")
		<-ctx.Done()
		return nil
	}

	// Ensure the stream exists for cleanup events.
	if err := e.natsClient.EnsureStream(&nats.StreamConfig{
		Name:     enrichStream,
		Subjects: []string{"cleanup.>"},
		MaxAge:   0, // retain indefinitely (managed by session lifecycle)
	}); err != nil {
		return fmt.Errorf("cleanup enricher: ensure stream: %w", err)
	}

	js := e.natsClient.JetStream()

	// Durable push consumer.
	sub, err := js.Subscribe(
		enrichSubjectFilter,
		func(msg *nats.Msg) { e.handleMsg(ctx, msg) },
		nats.Durable(enrichConsumer),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverNew(),
	)
	if err != nil {
		return fmt.Errorf("cleanup enricher: subscribe: %w", err)
	}
	e.sub = sub
	e.logger.Info("cleanup enricher: listening", "subject", enrichSubjectFilter)

	<-ctx.Done()
	_ = sub.Unsubscribe()
	e.logger.Info("cleanup enricher: stopped")
	return nil
}

// handleMsg processes a single NATS message: deserialises, enriches, acks.
func (e *CleanupEnricher) handleMsg(ctx context.Context, msg *nats.Msg) {
	var payload enrichMsg
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		e.logger.Error("cleanup enricher: bad message payload", "error", err)
		msg.Ack()
		return
	}

	if payload.SessionID == "" || payload.RealmID == "" {
		e.logger.Warn("cleanup enricher: message missing session_id or realm_id")
		msg.Ack()
		return
	}

	log := e.logger.With("session_id", payload.SessionID, "realm_id", payload.RealmID)
	log.Info("cleanup enricher: processing session")

	if err := e.enrichSession(ctx, payload.SessionID, payload.RealmID); err != nil {
		log.Error("cleanup enricher: enrichSession failed", "error", err)
		msg.Nak() // redeliver later
		return
	}

	msg.Ack()
	log.Info("cleanup enricher: session enrichment complete")
}

// enrichSession fetches pending rows, enriches them concurrently, then runs dedup.
func (e *CleanupEnricher) enrichSession(ctx context.Context, sessionID, realmID string) error {
	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(sessionID); err != nil {
		return fmt.Errorf("invalid session_id %q: %w", sessionID, err)
	}

	// Mark session as ENRICHING.
	if err := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHING",
	}); err != nil {
		return fmt.Errorf("update session status: %w", err)
	}

	rawRows, err := e.db.GetPendingSessionRows(ctx, pgSessionID)
	if err != nil {
		return fmt.Errorf("get pending rows: %w", err)
	}
	if len(rawRows) == 0 {
		// No pending rows — session may have been enriched already.
		return e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ENRICHED",
		})
	}

	enriched := make([]EnrichedRow, len(rawRows))

	// Process in batches of enrichBatchSize with up to enrichConcurrency goroutines.
	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, enrichConcurrency)

	for i := range rawRows {
		i := i
		row := rawRows[i]

		sem <- struct{}{}
		g.Go(func() error {
			defer func() { <-sem }()
			er, enrichErr := e.enrichRow(gCtx, realmID, row)
			if enrichErr != nil {
				// Non-fatal: log and store a zero-confidence row.
				e.logger.Warn("cleanup enricher: row enrichment failed",
					"row_id", row.ID, "error", enrichErr)
				er = zeroEnriched(row)
			}
			enriched[i] = er
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("enrichment workers: %w", err)
	}

	// Run in-memory dedup and recurring detection passes.
	e.dedup.AnnotateDuplicates(enriched)
	e.dedup.AnnotateRecurring(enriched)

	// Persist enrichment results back to DB.
	for _, er := range enriched {
		if dbErr := e.persistEnrichedRow(ctx, er); dbErr != nil {
			e.logger.Error("cleanup enricher: persist row failed",
				"row_id", er.ID, "error", dbErr)
			// Continue — best effort; remaining rows should still be saved.
		}
	}

	return e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHED",
	})
}

// enrichRow runs the full per-row pipeline:
// 1. Check ai_corrections (learning memory) → skip AI if already known.
// 2. EntityResolver  → vendor match (3-layer: DB → Vector → Fuzzy).
// 3. CoAMapper       → account match (Pinecone semantic).
func (e *CleanupEnricher) enrichRow(ctx context.Context, realmID string, row database.ShadowErpCleanupStaging) (EnrichedRow, error) {
	// Use the row's own realm_id (may be empty when QBO is not connected).
	rowRealm := realmID
	if rowRealm == "" && row.RealmID.Valid {
		rowRealm = row.RealmID.String
	}

	er := EnrichedRow{
		ID:             uuidStr(row.ID),
		SessionID:      uuidStr(row.SessionID),
		RealmID:        rowRealm,
		RawDescription: row.RawDescription.String,
		RawAmount:      numericToFloat(row.RawAmount),
		RawVendorName:  row.RawVendorName.String,
	}
	if row.RawDate.Valid {
		er.RawDate = row.RawDate.Time
	}

	// Determine the description to send to AI.
	aiInput := coalesce(row.RawDescription.String, row.RawVendorName.String)
	if aiInput == "" {
		return er, nil
	}

	vendorInput := coalesce(row.RawVendorName.String, row.RawDescription.String)

	// ── Layer 0: historical correction lookup ──────────────────────────────
	// Realm-scoped: skip if no realm (Excel-only session).
	knownVendorID := ""
	knownAccountID := ""
	if rowRealm != "" {
		knownVendorID = e.lookupCorrection(ctx, rowRealm, vendorInput, "vendor")
		knownAccountID = e.lookupCorrection(ctx, rowRealm, aiInput, "account")
	}

	vendorConfidence := 0.0
	accountConfidence := 0.0

	// ── Layer 1: Entity resolution (vendor) ───────────────────────────────
	if knownVendorID != "" {
		er.PredictedVendorID = knownVendorID
		er.NormalizedVendor = vendorInput
		vendorConfidence = 1.0
	} else if e.entityResolver != nil && rowRealm != "" && vendorInput != "" {
		match, err := e.entityResolver.ResolveEntity(ctx, rowRealm, "vendor", vendorInput)
		if err != nil {
			e.logger.Warn("cleanup enricher: entity resolution failed",
				"input", vendorInput, "error", err)
		} else if match != nil {
			er.PredictedVendorID = match.ID
			er.NormalizedVendor = match.Name
			vendorConfidence = match.Score
		}
	}

	// ── Layer 2: CoA mapping (account) ─────────────────────────────────────
	if knownAccountID != "" {
		er.PredictedAccountID = knownAccountID
		accountConfidence = 1.0
	} else if e.coaMapper != nil && rowRealm != "" {
		matches, err := e.coaMapper.MapDescriptionToAccount(ctx, rowRealm, aiInput, 1)
		if err != nil {
			e.logger.Warn("cleanup enricher: CoA mapping failed",
				"input", aiInput, "error", err)
		} else if len(matches) > 0 {
			er.PredictedAccountID = matches[0].AccountID
			accountConfidence = matches[0].Score
		}
	}

	// Combined confidence: average of vendor + account scores when both resolved,
	// or single score when only one resolved.
	switch {
	case er.PredictedVendorID != "" && er.PredictedAccountID != "":
		er.ConfidenceScore = (vendorConfidence + accountConfidence) / 2
	case er.PredictedAccountID != "":
		er.ConfidenceScore = accountConfidence
	case er.PredictedVendorID != "":
		er.ConfidenceScore = vendorConfidence
	}

	er.AIReasoning = buildReasoning(er.PredictedVendorID, er.NormalizedVendor,
		er.PredictedAccountID, er.ConfidenceScore)

	return er, nil
}

// lookupCorrection checks if there's a known user override for this raw input.
// Returns the corrected entity ID, or "" if not found.
func (e *CleanupEnricher) lookupCorrection(ctx context.Context, realmID, rawInput, correctionType string) string {
	if rawInput == "" {
		return ""
	}
	row, err := e.db.GetAiCorrectionByRawInput(ctx, database.GetAiCorrectionByRawInputParams{
		RealmID:        realmID,
		RawInput:       rawInput,
		CorrectionType: correctionType,
	})
	if err != nil {
		return "" // ErrNoRows or transient — silently skip
	}
	return row.UserCorrection
}

// persistEnrichedRow writes a single enriched result back to cleanup_staging.
func (e *CleanupEnricher) persistEnrichedRow(ctx context.Context, er EnrichedRow) error {
	var rowID, vendorID, accountID, dupOf pgtype.UUID
	_ = rowID.Scan(er.ID)
	_ = vendorID.Scan(er.PredictedVendorID)
	_ = accountID.Scan(er.PredictedAccountID)
	_ = dupOf.Scan(er.DuplicateOf)

	var confScore pgtype.Numeric
	_ = confScore.Scan(fmt.Sprintf("%.4f", er.ConfidenceScore))

	var splitJSON []byte
	if len(er.SplitSuggestion) > 0 {
		splitJSON, _ = json.Marshal(er.SplitSuggestion)
	}

	return e.db.UpdateRowEnrichment(ctx, database.UpdateRowEnrichmentParams{
		ID:                 rowID,
		PredictedVendorID:  vendorID,
		PredictedAccountID: accountID,
		NormalizedVendor:   pgtype.Text{String: er.NormalizedVendor, Valid: er.NormalizedVendor != ""},
		ConfidenceScore:    confScore,
		AiReasoning:        pgtype.Text{String: er.AIReasoning, Valid: er.AIReasoning != ""},
		DuplicateOf:        dupOf,
		IsRecurring:        er.IsRecurring,
		SplitSuggestion:    splitJSON,
	})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func zeroEnriched(row database.ShadowErpCleanupStaging) EnrichedRow {
	er := EnrichedRow{
		ID:             uuidStr(row.ID),
		SessionID:      uuidStr(row.SessionID),
		RawDescription: row.RawDescription.String,
		RawAmount:      numericToFloat(row.RawAmount),
		RawVendorName:  row.RawVendorName.String,
	}
	if row.RawDate.Valid {
		er.RawDate = row.RawDate.Time
	}
	return er
}

func buildReasoning(vendorID, vendorName, accountID string, confidence float64) string {
	if vendorID == "" && accountID == "" {
		return "No match found — review required."
	}
	if vendorID != "" && accountID != "" {
		return fmt.Sprintf("Vendor matched to %q; account mapped semantically. Combined confidence: %.0f%%.",
			vendorName, confidence*100)
	}
	if vendorID != "" {
		return fmt.Sprintf("Vendor matched to %q (confidence %.0f%%). No account resolved.", vendorName, confidence*100)
	}
	return fmt.Sprintf("Account resolved semantically (confidence %.0f%%). No vendor resolved.", confidence*100)
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

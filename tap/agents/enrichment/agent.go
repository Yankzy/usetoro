package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
)

type EnrichmentAgent struct {
	logger *slog.Logger
	bus    agent.EventBus
	cfg    agent.AgentConfig
	mem    agent.MemoryStore
	kp     *identity.KeyPair
	sub    *nats.Subscription

	db             *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	dedup          *cleanup.Deduplicator
}

func NewAgent(logger *slog.Logger, bus agent.EventBus, cfg agent.AgentConfig, mem agent.MemoryStore, db *database.Queries, er *ai.EntityResolver, coa *ai.CoAMapper) agent.Runnable {
	kp, _ := identity.GenerateKeyPair()
	cfg.DID = identity.CreateDID(kp.Public)
	return &EnrichmentAgent{
		logger:         logger,
		bus:            bus,
		cfg:            cfg,
		mem:            mem,
		kp:             kp,
		db:             db,
		entityResolver: er,
		coaMapper:      coa,
		dedup:          cleanup.NewDeduplicator(),
	}
}

func (e *EnrichmentAgent) Start() error {
	e.logger.Info("🤖 TAP Enrichment AI Agent Initializing...", "did", e.cfg.DID)

	// Register with Almanac
	inbox := core.BuildAgentInbox(e.cfg.DID)
	regPayload := map[string]interface{}{
		"did":       e.cfg.DID,
		"endpoints": []string{inbox},
		"capabilities": []map[string]interface{}{
			{"type": "accounting.enrichment"},
		},
		"expiry": time.Now().Add(24 * time.Hour).Unix(),
	}
	regBytes, _ := json.Marshal(regPayload)
	e.bus.Publish("almanac.register", regBytes)

	// Subscribe to Mapped CSV Proofs
	topic := "proof.accounting.cleanup.columns"
	sub, err := e.bus.QueueSubscribe(topic, "enrichment-group", func(msg *nats.Msg) {
		e.logger.Info("📡 [DEBUG] enrichment-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))
		if err := e.handleColumnsProof(context.Background(), msg); err != nil {
			e.logger.Error("enrichment agent transient error", "error", err)
			msg.Nak()
			return
		}
		msg.Ack()
	}, nats.Durable("enrichment-agent-durable"), nats.DeliverAll(), nats.AckExplicit())
	if err != nil {
		return err
	}
	e.sub = sub

	e.logger.Info("👂 Listening for Mapped CSV Proofs", "topic", topic)
	return nil
}

func (e *EnrichmentAgent) Stop() error {
	if e.sub != nil {
		return nil
	}
	return nil
}

func (e *EnrichmentAgent) handleColumnsProof(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return nil
	}

	if env.Performative != core.INFORM {
		return nil
	}

	var proof core.Proof
	if err := json.Unmarshal(env.Body, &proof); err != nil {
		return nil
	}

	if proof.Type != core.ProofAPI {
		return nil
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(proof.Data, &rows); err != nil || len(rows) == 0 {
		return nil
	}

	sessionID, _ := rows[0]["SessionID"].(string)
	if sessionID == "" {
		return nil
	}
	realmID, _ := rows[0]["RealmID"].(string)

	var pgSessionID pgtype.UUID
	pgSessionID.Scan(sessionID)

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.logger.Error("enrichment agent: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	e.logger.Info("enrichment agent: waiting for database insertion sync", "session", sessionID)

	var pendingRows []database.GetPendingSessionRowsRow
	var err error
	for retries := 0; retries < 10; retries++ {
		pendingRows, err = e.db.GetPendingSessionRows(ctx, pgSessionID)
		if err == nil && len(pendingRows) > 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if len(pendingRows) == 0 {
		e.logger.Error("enrichment agent: rows never appeared in DB, aborting.", "session", sessionID)
		return fmt.Errorf("timeout waiting for DB rows")
	}

	e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHING",
	})

	enriched := make([]cleanup.EnrichedRow, len(pendingRows))
	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, 5)

	for i, row := range pendingRows {
		i, row := i, row
		sem <- struct{}{}
		g.Go(func() error {
			defer func() { <-sem }()
			er, enrichErr := e.enrichRow(gCtx, realmID, row)
			if enrichErr != nil {
				e.logger.Warn("enrichment agent: matching failed, falling back to empty row", "row", row.ID, "err", enrichErr)
				er = cleanup.EnrichedRow{
					ID:             uuidStr(row.ID),
					SessionID:      uuidStr(row.SessionID),
					RealmID:        realmID,
					RawDescription: row.RawDescription.String,
					RawAmount:      ParseDirtyAmount(row.RawAmount),
				}
				if row.RawDate.Valid {
					er.RawDate = row.RawDate.Time
				}
			}
			enriched[i] = er
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		e.logger.Error("enrichment agent: error during concurrency phase", "err", err)
	}

	e.dedup.AnnotateDuplicates(enriched)
	e.dedup.AnnotateRecurring(enriched)

	for _, er := range enriched {
		if dbErr := e.persistEnrichedRow(ctx, er); dbErr != nil {
			e.logger.Error("enrichment agent: persist row failed", "err", dbErr)
		}
	}

	e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHED",
	})
	e.logger.Info("✅ semantic enrichment complete!", "session", sessionID)

	if err := e.broadcastEnrichmentProof(ctx, sessionID, env.ConversationID); err != nil {
		return fmt.Errorf("broadcast failed: %w", err)
	}

	return nil
}

func (e *EnrichmentAgent) broadcastEnrichmentProof(ctx context.Context, sessionID, cid string) error {
	proof := core.Proof{
		TaskID:    sessionID,
		Type:      core.ProofAPI,
		Data:      []byte(`{"status":"enriched"}`),
		Timestamp: time.Now().Unix(),
	}
	proof.Signature = e.kp.Sign(proof.Data)

	proofEnv, _ := core.NewEnvelope(uuid.New().String(), e.cfg.DID, "did:toro:hive", cid, core.INFORM, proof)
	proofEnv.Signature = e.kp.Sign(proofEnv.Body)

	finalBytes, _ := json.Marshal(proofEnv)
	targetTopic := "proof.accounting.cleanup.enrichment"
	e.logger.Info("🚀 [DEBUG] enrichment-agent sending message to JetStream", "topic", targetTopic, "data_length", len(finalBytes))
	return e.bus.Publish(targetTopic, finalBytes)
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func ParseDirtyAmount(amt string) float64 {
	// Provide a baseline fallback parser so the downstream Enrinchment module operates.
	// We extract simple numerical artifacts without discarding the DB textual safety.
	cleaned := strings.ReplaceAll(amt, "*", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(cleaned, 64)
	return v
}

func (e *EnrichmentAgent) persistEnrichedRow(ctx context.Context, er cleanup.EnrichedRow) error {
	var rowID, vendorID, customerID, accountID, dupOf pgtype.UUID
	_ = rowID.Scan(er.ID)
	_ = vendorID.Scan(er.PredictedVendorID)
	_ = customerID.Scan(er.PredictedCustomerID)
	_ = accountID.Scan(er.PredictedAccountID)
	_ = dupOf.Scan(er.DuplicateOf)

	var confScore pgtype.Numeric
	_ = confScore.Scan(fmt.Sprintf("%.4f", er.ConfidenceScore))

	var splitJSON []byte
	if len(er.SplitSuggestion) > 0 {
		splitJSON, _ = json.Marshal(er.SplitSuggestion)
	}

	return e.db.UpdateRowEnrichment(ctx, database.UpdateRowEnrichmentParams{
		ID:                    rowID,
		PredictedVendorID:     vendorID,
		PredictedVendorName:   pgtype.Text{String: er.NormalizedVendor, Valid: er.NormalizedVendor != ""},
		PredictedCustomerID:   customerID,
		PredictedCustomerName: pgtype.Text{String: er.NormalizedCustomer, Valid: er.NormalizedCustomer != ""},
		PredictedAccountID:    accountID,
		PredictedAccountName:  pgtype.Text{String: er.PredictedAccountName, Valid: er.PredictedAccountName != ""},
		ConfidenceScore:       confScore,
		AiReasoning:           pgtype.Text{String: er.AIReasoning, Valid: er.AIReasoning != ""},
		DuplicateOf:           dupOf,
		IsRecurring:           er.IsRecurring,
		SplitSuggestion:       splitJSON,
	})
}

func (e *EnrichmentAgent) enrichRow(ctx context.Context, realmID string, row database.GetPendingSessionRowsRow) (cleanup.EnrichedRow, error) {
	rowRealm := realmID
	if rowRealm == "" && row.RealmID.Valid {
		rowRealm = row.RealmID.String
	}

	er := cleanup.EnrichedRow{
		ID:              uuidStr(row.ID),
		SessionID:       uuidStr(row.SessionID),
		RealmID:         rowRealm,
		RawDescription:  row.RawDescription.String,
		RawAmount:       ParseDirtyAmount(row.RawAmount),
		RawVendorName:   row.PredictedVendorName.String,
		RawCustomerName: row.PredictedCustomerName.String,
	}
	if row.RawDate.Valid {
		er.RawDate = row.RawDate.Time
	}

	aiInput := row.RawDescription.String
	if aiInput == "" {
		return er, nil
	}

	entityInput := aiInput
	if er.RawAmount > 0 {
		if row.PredictedCustomerName.Valid && row.PredictedCustomerName.String != "" {
			entityInput = row.PredictedCustomerName.String
		}
	} else {
		if row.PredictedVendorName.Valid && row.PredictedVendorName.String != "" {
			entityInput = row.PredictedVendorName.String
		}
	}

	entityConfidence := 0.0
	accountConfidence := 0.0

	if e.entityResolver != nil && rowRealm != "" && entityInput != "" {
		if er.RawAmount > 0 {
			match, err := e.entityResolver.ResolveCustomer(ctx, rowRealm, entityInput)
			if err == nil && match != nil {
				er.PredictedCustomerID = match.ID
				er.NormalizedCustomer = match.Name
				entityConfidence = match.Score
			}
		} else {
			match, err := e.entityResolver.ResolveVendor(ctx, rowRealm, entityInput)
			if err == nil && match != nil {
				er.PredictedVendorID = match.ID
				er.NormalizedVendor = match.Name
				entityConfidence = match.Score
			}
		}
	}

	if e.entityResolver != nil && rowRealm != "" {
		transactionType := "money_out"
		resolvedName := er.NormalizedVendor
		if er.RawAmount > 0 {
			transactionType = "money_in"
			resolvedName = er.NormalizedCustomer
		}
		
		match, err := e.entityResolver.ResolveAccount(ctx, rowRealm, transactionType, aiInput, resolvedName)
		if err == nil && match != nil {
			er.PredictedAccountID = match.ID
			er.PredictedAccountName = match.Name
			er.AIReasoning = match.Name
			accountConfidence = match.Score
		}
	}

	er.ConfidenceScore = (entityConfidence + accountConfidence) / 2.0
	return er, nil
}

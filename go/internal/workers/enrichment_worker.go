package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
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
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

var cleanDescRegex = regexp.MustCompile(`(?i)[0-9]+|\b(?:ID|REF|POS)\b|[^a-zA-Z\s]`)

func normalizeDescription(raw string) string {
	cleaned := cleanDescRegex.ReplaceAllString(raw, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned)
}

var fixHashRegex = regexp.MustCompile(`([^#\s]+)#\s*([^#\s]+)`)

func FormatHashTags(raw string) string {
	return fixHashRegex.ReplaceAllString(raw, "$1 #$2")
}

// EnrichmentWorker acts as a purely deterministic data processing worker
// avoiding AI-based agent scaffolding.
type EnrichmentWorker struct {
	db     *database.Queries
	dedup  *cleanup.Deduplicator
	nc     *nats.Conn
	logger *slog.Logger
	llm    *ai.LLMClient
}

func NewEnrichmentWorker(
	db *database.Queries,
	nc *nats.Conn,
	logger *slog.Logger,
	llm *ai.LLMClient,
) (*EnrichmentWorker, error) {
	return &EnrichmentWorker{
		db:     db,
		nc:     nc,
		dedup:  cleanup.NewDeduplicator(),
		logger: logger,
		llm:    llm,
	}, nil
}

func (e *EnrichmentWorker) Init(ctx context.Context) error {
	return nil
}

func (e *EnrichmentWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "proof.accounting.cleanup.inserted",
			Group:   "enrichment-group",
			Options: []nats.SubOpt{nats.Durable("enrichment-inserted-durable"), nats.DeliverAll(), nats.AckExplicit()},
		},
	}
}

func (e *EnrichmentWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	e.logger.Info("📡 [DEBUG] enrichment-worker received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))
	if err := e.handleColumnsProof(ctx, msg); err != nil {
		e.logger.Error("enrichment worker transient error", "error", err)
		return err
	}
	return nil
}

func (e *EnrichmentWorker) handleColumnsProof(ctx context.Context, msg *nats.Msg) error {
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
		e.logger.Error("enrichment worker: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	e.logger.Info("enrichment worker: waiting for database insertion sync", "session", sessionID)

	var pendingRows []database.GetPendingSessionRowsRow
	expectedCount := len(rows)
	var err error
	for retries := 0; retries < 10; retries++ {
		pendingRows, err = e.db.GetPendingSessionRows(ctx, pgSessionID)
		e.logger.Info("enrichment worker loop", "expected", expectedCount, "found", len(pendingRows), "err", err)
		if err == nil && len(pendingRows) == expectedCount {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if len(pendingRows) == 0 {
		e.logger.Error("enrichment worker: rows never appeared in DB, aborting.", "session", sessionID)
		return fmt.Errorf("timeout waiting for DB rows")
	}

	e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHING",
	})

	type Cluster struct {
		Head  *cleanup.EnrichedRow
		Tails []*cleanup.EnrichedRow
		Row   database.GetPendingSessionRowsRow
	}

	clusters := make(map[string]*Cluster)
	allRows := make([]*cleanup.EnrichedRow, 0, len(pendingRows))

	for _, row := range pendingRows {
		rowRealm := realmID
		if rowRealm == "" && row.RealmID.Valid {
			rowRealm = row.RealmID.String
		}

		ptr := &cleanup.EnrichedRow{
			ID:              uuidStr(row.ID),
			SessionID:       uuidStr(row.SessionID),
			RealmID:         rowRealm,
			RawDescription:  FormatHashTags(row.RawDescription.String),
			RawAmount:       ParseDirtyAmount(row.RawAmount),
			RawVendorName:   row.PredictedVendorName,
			RawCustomerName: row.PredictedCustomerName,
		}
		if row.RawDate.Valid {
			ptr.RawDate = row.RawDate.Time
		}

		allRows = append(allRows, ptr)

		normDesc := normalizeDescription(ptr.RawDescription)
		sign := "in"
		if ptr.RawAmount < 0 {
			sign = "out"
		}
		groupKey := fmt.Sprintf("%s_%s", normDesc, sign)

		if c, exists := clusters[groupKey]; exists {
			c.Tails = append(c.Tails, ptr)
			c.Head.IsRecurring = true
			ptr.IsRecurring = true
			ptr.DuplicateOf = c.Head.ID
		} else {
			clusters[groupKey] = &Cluster{
				Head: ptr,
				Row:  row,
			}
		}
	}

	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, 5)

	for _, c := range clusters {
		c := c
		sem <- struct{}{}
		g.Go(func() error {
			defer func() { <-sem }()
			er, enrichErr := e.enrichRow(gCtx, realmID, c.Row)
			if enrichErr != nil {
				e.logger.Warn("enrichment worker: matching failed", "row", c.Row.ID, "err", enrichErr)
			} else {
				c.Head.PredictedVendorID = er.PredictedVendorID
				c.Head.PredictedAccountID = er.PredictedAccountID
				c.Head.PredictedCustomerID = er.PredictedCustomerID
				c.Head.PredictedAccountName = er.PredictedAccountName
				c.Head.NormalizedVendor = er.NormalizedVendor
				c.Head.NormalizedCustomer = er.NormalizedCustomer
				c.Head.ConfidenceScore = er.ConfidenceScore
				c.Head.AIReasoning = er.AIReasoning
				c.Head.SplitSuggestion = er.SplitSuggestion
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		e.logger.Error("enrichment worker: error during concurrency phase", "err", err)
	}

	for _, c := range clusters {
		for _, tail := range c.Tails {
			tail.PredictedVendorID = c.Head.PredictedVendorID
			tail.PredictedAccountID = c.Head.PredictedAccountID
			tail.PredictedCustomerID = c.Head.PredictedCustomerID
			tail.PredictedAccountName = c.Head.PredictedAccountName
			tail.NormalizedVendor = c.Head.NormalizedVendor
			tail.NormalizedCustomer = c.Head.NormalizedCustomer
			tail.ConfidenceScore = c.Head.ConfidenceScore
			tail.AIReasoning = c.Head.AIReasoning
			tail.SplitSuggestion = c.Head.SplitSuggestion
		}
	}

	enrichedValues := make([]cleanup.EnrichedRow, len(allRows))
	for i, ptr := range allRows {
		enrichedValues[i] = *ptr
	}

	e.dedup.AnnotateDuplicates(enrichedValues)
	e.dedup.AnnotateRecurring(enrichedValues)

	for i, val := range enrichedValues {
		*allRows[i] = val
	}

	// Native Redux Logging Patch Matrix manually executed decoupled from BaseAgent
	schemaString := `{ "type": "object", "properties": { "enrichments": { "type": "object" }, "status": { "type": "string" } } }`
	rbacRules := redux.RBACPolicy{
		AllowedPrefixes: map[string][]string{
			"accounting.cleanup": {"/enrichments", "/status"},
		},
	}
	cfg := redux.EngineConfig{
		MaxOperations:   500,
		MaxPayloadBytes: 1048576,
		SchemaString:    schemaString,
		RBAC:            rbacRules,
	}
	store, _ := redux.NewStore(cfg)

	var workflowID pgtype.UUID
	_ = workflowID.Scan(sessionID)

	wf, wfErr := e.db.GetWorkflow(ctx, workflowID)
	baseState := []byte(`{}`)
	currentSeq := uint64(0)
	if wfErr == nil {
		baseState = wf.State
		currentSeq = uint64(wf.SequenceID)
	}

	enrichmentMap := make(map[string]*cleanup.EnrichedRow)
	for _, ptr := range allRows {
		enrichmentMap[ptr.ID] = ptr
	}
	enrichmentsJSON, _ := json.Marshal(enrichmentMap)
	patch1 := `{"op": "add", "path": "/status", "value": "ENRICHED"}`
	patch2 := fmt.Sprintf(`{"op": "add", "path": "/enrichments", "value": %s}`, string(enrichmentsJSON))

	event := redux.RFC6902Event{
		SequenceID: currentSeq,
		Actor:      "accounting.cleanup",
		PatchArray: []json.RawMessage{[]byte(patch1), []byte(patch2)},
	}

	_, _, faults, reduceErr := store.Reduce(ctx, baseState, currentSeq, []redux.RFC6902Event{event})
	if reduceErr == nil && len(faults) == 0 {
		eventBytes, _ := json.Marshal(event)
		uuidStr := uuid.UUID(workflowID.Bytes).String()
		traceTopic := fmt.Sprintf("workflow.trace.%s", uuidStr)
		js, _ := e.nc.JetStream()
		_, _ = js.Publish(traceTopic, eventBytes)
	} else {
		e.logger.Error("Redux engine rejection during enrichment worker manual sequence", "err", reduceErr, "faults", faults)
	}

	// Persist the unified structured DB data
	for _, ptr := range allRows {
		if dbErr := e.persistEnrichedRow(ctx, *ptr); dbErr != nil {
			e.logger.Error("enrichment worker: persist row failed", "err", dbErr)
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

func (e *EnrichmentWorker) broadcastEnrichmentProof(ctx context.Context, sessionID, cid string) error {
	proof := core.Proof{
		TaskID:    sessionID,
		Type:      core.ProofAPI,
		Data:      []byte(`{"status":"enriched"}`),
		Timestamp: time.Now().Unix(),
	}

	proofEnv, _ := core.NewEnvelope(uuid.New().String(), "did:toro:worker:enrichment", "did:toro:hive", cid, core.INFORM, proof)

	finalBytes, _ := json.Marshal(proofEnv)
	targetTopic := "proof.accounting.cleanup.enrichment"
	js, err := e.nc.JetStream()
	if err != nil {
		return err
	}
	e.logger.Info("🚀 [DEBUG] enrichment-worker sending message to JetStream", "topic", targetTopic, "data_length", len(finalBytes))
	_, err = js.Publish(targetTopic, finalBytes)
	return err
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func ParseDirtyAmount(amt string) float64 {
	cleaned := strings.ReplaceAll(amt, "*", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(cleaned, 64)
	return v
}

func (e *EnrichmentWorker) persistEnrichedRow(ctx context.Context, er cleanup.EnrichedRow) error {
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
		MerchantName:          pgtype.Text{String: er.MerchantName, Valid: er.MerchantName != ""},
		PlaidCategory:         pgtype.Text{String: er.PlaidCategory, Valid: er.PlaidCategory != ""},
	})
}

type EnrichmentExtract struct {
	MerchantName  string `json:"merchant_name"`
	PlaidCategory string `json:"plaid_category"`
}

func (e *EnrichmentWorker) enrichRow(ctx context.Context, realmID string, row database.GetPendingSessionRowsRow) (cleanup.EnrichedRow, error) {
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
		RawVendorName:   row.PredictedVendorName,
		RawCustomerName: row.PredictedCustomerName,
	}
	if row.RawDate.Valid {
		er.RawDate = row.RawDate.Time
	}

	// NATIVE LLM EXTRACTION
	if e.llm != nil {
		systemPrompt := "You are a financial data categorization engine. Given a raw bank transaction description, extract the pure merchant/customer name and a generalized physical industry category (e.g. 'Software', 'Food and Drink'). Return exactly the JSON format requested."
		userPrompt := fmt.Sprintf("Analyze this raw bank transaction: \"%s\"", er.RawDescription)

		var extract EnrichmentExtract
		if err := e.llm.GenerateJSON(ctx, systemPrompt, userPrompt, &extract); err == nil {
			er.MerchantName = extract.MerchantName
			er.PlaidCategory = extract.PlaidCategory
		} else {
			e.logger.Warn("Failed LLM extraction", "err", err)
		}
	}

	er.ConfidenceScore = 1.0
	return er, nil
}

package workers

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/redis/go-redis/v9"
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

var noiseRegex = regexp.MustCompile(`(?i)([\d]{4,})|(SQ\s*\*)|(AMZN\*)|(PAYPAL\s*\*)|(TST\*)|(CARD\s*\d+)|(\bUS\b)|(\b[A-Z]{2}\b)`)

func SanitizeDescriptor(raw string) string {
	cleaned := noiseRegex.ReplaceAllString(raw, "")
	return strings.ToUpper(strings.TrimSpace(cleaned))
}

const (
	enrichmentLegacyProofSubject = "proof.accounting.cleanup.enrichment"
	enrichmentWorkerDID          = "did:toro:worker:enrichment-worker"
)

// EnrichmentWorker acts as a purely deterministic data processing worker
// avoiding AI-based agent scaffolding.
type EnrichmentWorker struct {
	db     *database.Queries
	dedup  *cleanup.Deduplicator
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
	rdb    *redis.Client
}

// EnrichmentWorkerPayload defines the expected JSON payload for LLM tool invocation.
type EnrichmentWorkerPayload struct {
	SessionID string `json:"session_id" desc:"The ID of the cleanup session to enrich"`
}

// ToolName returns the unique LLM tool name for this worker.
func (e *EnrichmentWorker) ToolName() string {
	return "TriggerEnrichment"
}

// ToolDescription provides the context for the LLM.
func (e *EnrichmentWorker) ToolDescription() string {
	return "Triggers the semantic enrichment process for a specific cleanup session. This dedupes and formats extracted bank transaction rows."
}

// PayloadStruct returns a typed instance to automatically generate a JSON schema.
func (e *EnrichmentWorker) PayloadStruct() any {
	return EnrichmentWorkerPayload{}
}

func NewEnrichmentWorker(
	db *database.Queries,
	nc *nats.Conn,
	logger *slog.Logger,
	cfg *config.Config,
	rdb *redis.Client,
) (*EnrichmentWorker, error) {
	return &EnrichmentWorker{
		db:     db,
		nc:     nc,
		dedup:  cleanup.NewDeduplicator(),
		logger: logger,
		cfg:    cfg,
		rdb:    rdb,
	}, nil
}

func (e *EnrichmentWorker) Init(ctx context.Context) error {
	return nil
}

func (e *EnrichmentWorker) Subscriptions() []SubscriptionConfig {
	if e.cfg == nil {
		e.logger.Error("enrichment worker: missing config")
		return nil
	}

	_, workerCfg := e.cfg.Workers.GetForWorker(e)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		e.logger.Error("enrichment worker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		derived, err := core.BuildWorkerInboxFromActivity(activityType)
		if err != nil {
			e.logger.Error("enrichment worker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
		subject = derived
	}
	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}
	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{nats.Durable(durableFromSubject(subject)), nats.DeliverAll(), nats.AckExplicit()},
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
		e.logger.Warn("enrichment worker: dropping malformed envelope", "error", err)
		return nil
	}
	if !core.IsValidPerformative(env.Performative) {
		e.logger.Warn("enrichment worker: dropping message, invalid performative", "perf", env.Performative)
		return nil
	}

	if env.Performative != core.REQUEST {
		return nil
	}

	bodyBytes := env.Body
	var workflowID string

	var taskDef core.TaskDefinition
	if err := json.Unmarshal(env.Body, &taskDef); err == nil {
		workflowID = taskDef.ID
		if len(taskDef.Payload) > 0 {
			bodyBytes = taskDef.Payload
		}
	}

	rows, err := e.extractRowsPayload(bodyBytes, 0)
	if err != nil {
		e.logger.Warn("enrichment worker: failed to extract rows payload", "error", err)
		return nil
	}

	if len(rows) == 0 {
		return nil
	}

	sessionID := core.RowString(rows[0], "SessionID", "session_id")
	if sessionID == "" {
		e.logger.Warn("enrichment worker: missing SessionID in first row")
		return nil
	}
	realmID := core.RowString(rows[0], "RealmID", "realm_id")

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(sessionID); err != nil || !pgSessionID.Valid {
		e.logger.Error("enrichment worker: invalid session ID format", "session", sessionID, "error", err)
		return nil
	}

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.logger.Error("enrichment worker: poison pill message exceeded max retries", "session", sessionID)
		if statusErr := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		}); statusErr != nil {
			e.logger.Warn("enrichment worker: failed to set ERROR status for poison pill", "session", sessionID, "error", statusErr)
		}
		msg.Term()
		return nil
	}

	e.logger.Info("enrichment worker: waiting for database insertion sync", "session", sessionID, "expected", len(rows), "realm", realmID)

	var pendingRows []database.GetPendingSessionRowsRow
	expectedCount := len(rows)
	var lastQueryErr error
	for retries := 0; retries < 15; retries++ {
		pendingRows, err = e.db.GetPendingSessionRows(ctx, pgSessionID)
		if err != nil {
			lastQueryErr = err
			e.logger.Warn("enrichment worker: db query error during loop", "error", err)
		}

		e.logger.Info("enrichment worker loop",
			"session", sessionID,
			"attempt", retries+1,
			"expected", expectedCount,
			"found", len(pendingRows),
		)

		if err == nil && len(pendingRows) >= expectedCount {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	if len(pendingRows) == 0 {
		e.logger.Error("enrichment worker: rows never appeared in DB, aborting.", "session", sessionID)
		if lastQueryErr != nil {
			return fmt.Errorf("failed to fetch pending rows: %w", lastQueryErr)
		}
		return errors.New("timeout waiting for DB rows")
	}

	if err := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHING",
	}); err != nil {
		return fmt.Errorf("failed to update session status to ENRICHING: %w", err)
	}

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
		if row.ParsedDate.Valid {
			ptr.RawDate = row.ParsedDate.Time
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

	var pgWorkflowID pgtype.UUID
	if workflowID != "" {
		_ = pgWorkflowID.Scan(workflowID)
	} else {
		// Fallback to sessionID for legacy or direct calls if TaskID is missing
		_ = pgWorkflowID.Scan(sessionID)
	}

	wf, wfErr := e.db.GetWorkflow(ctx, pgWorkflowID)
	baseState := []byte(`{}`)
	currentSeq := uint64(0)
	if wfErr == nil {
		baseState = wf.State
		currentSeq = uint64(wf.SequenceID)
	}

	_, _, faults, reduceErr := RunEnrichmentRedux(ctx, baseState, currentSeq, allRows)
	if reduceErr == nil && len(faults) == 0 {
		enrichmentMap := make(map[string]reduxEnrichedTraceRow, len(allRows))
		for _, ptr := range allRows {
			enrichmentMap[ptr.ID] = buildReduxEnrichedTraceRow(ptr)
		}
		enrichmentsJSON, _ := json.Marshal(enrichmentMap)
		patch1 := `{"op": "add", "path": "/status", "value": "ENRICHED"}`
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/enrichments", "value": %s}`, string(enrichmentsJSON))

		event := redux.RFC6902Event{
			SequenceID: currentSeq,
			Actor:      "accounting.cleanup",
			PatchArray: []json.RawMessage{[]byte(patch1), []byte(patch2)},
		}

		eventBytes, _ := json.Marshal(event)
		var uuidStr string
		if pgWorkflowID.Valid {
			uuidStr = uuid.UUID(pgWorkflowID.Bytes).String()
		} else {
			uuidStr = sessionID
		}
		traceTopic := fmt.Sprintf("workflow.trace.%s", uuidStr)
		js, _ := e.nc.JetStream()
		_, _ = js.Publish(traceTopic, eventBytes)
	} else {
		e.logger.Error("Redux engine rejection during enrichment worker manual sequence", "err", reduceErr, "faults", faults)
	}

	// Persist the unified structured DB data
	for _, ptr := range allRows {
		if dbErr := e.persistEnrichedRow(ctx, *ptr); dbErr != nil {
			return fmt.Errorf("failed to persist enriched row %s: %w", ptr.ID, dbErr)
		}
	}

	if err := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "ENRICHED",
	}); err != nil {
		return fmt.Errorf("failed to update session status to ENRICHED: %w", err)
	}
	e.logger.Info("✅ semantic enrichment complete!", "session", sessionID)

	if err := e.broadcastEnrichmentProof(ctx, msg, sessionID, env.ConversationID); err != nil {
		return fmt.Errorf("broadcast failed: %w", err)
	}

	return nil
}

func (e *EnrichmentWorker) extractRowsPayload(data []byte, depth int) ([]map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, errors.New("empty payload")
	}
	if depth > 5 {
		return nil, errors.New("payload nesting too deep")
	}

	// Use the enhanced recursive ExtractRows from manager.go
	if rows, err := ExtractRows(data); err == nil && len(rows) > 0 {
		return rows, nil
	}

	// If it fails, it might be a nested string (base64 or escaped JSON)
	var payloadString string
	if err := json.Unmarshal(data, &payloadString); err == nil {
		return e.extractRowsFromString(payloadString, depth+1)
	}

	return nil, errors.New("no rows payload found")
}

func (e *EnrichmentWorker) extractRowsFromString(payload string, depth int) ([]map[string]interface{}, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, errors.New("empty string payload")
	}

	if decoded, err := decodeBase64Payload(payload); err == nil && len(decoded) > 0 {
		if rows, innerErr := e.extractRowsPayload(decoded, depth+1); innerErr == nil && len(rows) > 0 {
			return rows, nil
		}
	}

	if json.Valid([]byte(payload)) {
		if rows, innerErr := e.extractRowsPayload([]byte(payload), depth+1); innerErr == nil && len(rows) > 0 {
			return rows, nil
		}
	}

	return nil, errors.New("string payload not decodable")
}

func decodeBase64Payload(payload string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(payload)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("payload is not valid base64")
}

func (e *EnrichmentWorker) broadcastEnrichmentProof(ctx context.Context, msg *nats.Msg, sessionID, cid string) error {
	resultData, _ := json.Marshal(map[string]string{
		"status":     "enriched",
		"session_id": sessionID,
	})
	proof := core.Proof{
		TaskID:    sessionID,
		Type:      core.ProofAPI,
		Data:      resultData,
		Timestamp: time.Now().Unix(),
	}

	dst := "did:toro:hive"
	targetTopic := msg.Reply
	isCoreReply := targetTopic != "" && !strings.HasPrefix(targetTopic, "$JS.ACK.")

	if !isCoreReply {
		targetTopic = enrichmentLegacyProofSubject
		if cid != "" {
			dst = workflows.OrchestratorDID
			targetTopic = workflows.OrchestratorInbox
		}
	}

	proofEnv, err := core.NewEnvelope(uuid.New().String(), enrichmentWorkerDID, dst, cid, core.INFORM, proof)
	if err != nil {
		return err
	}

	finalBytes, _ := json.Marshal(proofEnv)
	js, err := e.nc.JetStream()
	if err != nil {
		return err
	}
	e.logger.Info("🚀 [DEBUG] enrichment-worker sending message to JetStream", "topic", targetTopic, "data_length", len(finalBytes))

	if isCoreReply {
		err = e.nc.Publish(targetTopic, finalBytes)
	} else {
		_, err = js.Publish(targetTopic, finalBytes)
	}
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
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.ReplaceAll(cleaned, "$", "")
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
		PredictedVendorID:     vendorID,
		PredictedVendorName:   pgtype.Text{String: er.NormalizedVendor, Valid: er.NormalizedVendor != ""},
		PredictedCustomerID:   customerID,
		PredictedCustomerName: pgtype.Text{String: er.NormalizedCustomer, Valid: er.NormalizedCustomer != ""},
		PredictedAccountID:    accountID,
		PredictedAccountName:  pgtype.Text{String: er.PredictedAccountName, Valid: er.PredictedAccountName != ""},
		ConfidenceScore:       confScore,
		AiReasoning:           pgtype.Text{String: er.AIReasoning, Valid: er.AIReasoning != ""},
		DuplicateOf:           dupOf,
		IsRecurring:           pgtype.Bool{Bool: er.IsRecurring, Valid: true},
		SplitSuggestion:       splitJSON,
		MerchantName:          pgtype.Text{String: er.MerchantName, Valid: er.MerchantName != ""},
		Category:              pgtype.Text{String: er.Category, Valid: er.Category != ""},
		Status:                pgtype.Text{String: er.Status, Valid: er.Status != ""},
		ID:                    rowID,
	})
}

type reduxEnrichedTraceRow struct {
	ID                   string                       `json:"id"`
	SessionID            string                       `json:"session_id"`
	RealmID              string                       `json:"realm_id,omitempty"`
	RawDescription       string                       `json:"raw_description,omitempty"`
	RawAmount            float64                      `json:"raw_amount,omitempty"`
	RawDate              time.Time                    `json:"raw_date,omitempty"`
	RawVendorName        string                       `json:"raw_vendor_name,omitempty"`
	RawCustomerName      string                       `json:"raw_customer_name,omitempty"`
	PredictedVendorID    string                       `json:"predicted_vendor_id,omitempty"`
	PredictedCustomerID  string                       `json:"predicted_customer_id,omitempty"`
	PredictedAccountID   string                       `json:"predicted_account_id,omitempty"`
	PredictedAccountName string                       `json:"predicted_account_name,omitempty"`
	NormalizedVendor     string                       `json:"normalized_vendor,omitempty"`
	NormalizedCustomer   string                       `json:"normalized_customer,omitempty"`
	MerchantName         string                       `json:"merchant_name,omitempty"`
	Category             string                       `json:"category,omitempty"`
	ConfidenceScore      float64                      `json:"confidence_score,omitempty"`
	AIReasoning          string                       `json:"ai_reasoning,omitempty"`
	IsRecurring          bool                         `json:"is_recurring,omitempty"`
	SplitSuggestion      map[string]cleanup.SplitLine `json:"split_suggestion,omitempty"`
	DuplicateOf          string                       `json:"duplicate_of,omitempty"`
}

func buildReduxEnrichedTraceRow(ptr *cleanup.EnrichedRow) reduxEnrichedTraceRow {
	return reduxEnrichedTraceRow{
		ID:                   ptr.ID,
		SessionID:            ptr.SessionID,
		RealmID:              ptr.RealmID,
		RawDescription:       ptr.RawDescription,
		RawAmount:            ptr.RawAmount,
		RawDate:              ptr.RawDate,
		RawVendorName:        ptr.RawVendorName,
		RawCustomerName:      ptr.RawCustomerName,
		PredictedVendorID:    ptr.PredictedVendorID,
		PredictedCustomerID:  ptr.PredictedCustomerID,
		PredictedAccountID:   ptr.PredictedAccountID,
		PredictedAccountName: ptr.PredictedAccountName,
		NormalizedVendor:     ptr.NormalizedVendor,
		NormalizedCustomer:   ptr.NormalizedCustomer,
		MerchantName:         ptr.MerchantName,
		Category:             ptr.Category,
		ConfidenceScore:      ptr.ConfidenceScore,
		AIReasoning:          ptr.AIReasoning,
		IsRecurring:          ptr.IsRecurring,
		SplitSuggestion:      splitSuggestionTraceMap(ptr.SplitSuggestion),
		DuplicateOf:          ptr.DuplicateOf,
	}
}

func splitSuggestionTraceMap(suggestions []cleanup.SplitLine) map[string]cleanup.SplitLine {
	if len(suggestions) == 0 {
		return nil
	}
	out := make(map[string]cleanup.SplitLine, len(suggestions))
	for idx, line := range suggestions {
		out[fmt.Sprintf("line_%d", idx+1)] = line
	}
	return out
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
	if row.ParsedDate.Valid {
		er.RawDate = row.ParsedDate.Time
	}

	sanitized := SanitizeDescriptor(er.RawDescription)

	var match database.FignodeMasterMerchant
	var found bool

	// L1 Exact Lookup (Redis)
	if e.rdb != nil {
		hasher := md5.New()
		hasher.Write([]byte(sanitized))
		hashStr := hex.EncodeToString(hasher.Sum(nil))
		redisKey := "cee:raw:" + hashStr
		val, err := e.rdb.Get(ctx, redisKey).Result()
		if err == nil {
			var cached database.FignodeMasterMerchant
			if err := json.Unmarshal([]byte(val), &cached); err == nil {
				match = cached
				found = true
			}
		}
	}

	// L1 Database exact pattern fallback
	if !found {
		mm, err := e.db.GetMasterMerchantByExactPattern(ctx, sanitized)
		if err == nil {
			match = mm
			found = true
		}
	}

	// L2 Substring Trie Match
	if !found {
		mm, err := e.db.GetMasterMerchantBySubstringPattern(ctx, sanitized)
		if err == nil {
			match = mm
			found = true
		}
	}

	// L3 Postgres Trigram Matching
	if !found {
		mm, err := e.db.GetMasterMerchantByTrigramSimilarity(ctx, sanitized)
		if err == nil {
			match = mm
			found = true
		}
	}

	// L4 Sweep Gate (Transfers)
	var parsedTime time.Time
	if row.ParsedDate.Valid {
		parsedTime = row.ParsedDate.Time
	}
	bankAccountID := row.BankAccountID
	var realmText pgtype.Text
	if row.RealmID.Valid && row.RealmID.String != "" {
		realmText = row.RealmID
	} else {
		realmText = pgtype.Text{String: realmID, Valid: realmID != ""}
	}

	isTransferTerm := strings.Contains(sanitized, "INTERNAL TRANSFER") ||
		strings.Contains(sanitized, "ONLINE PAYMENT") ||
		strings.Contains(sanitized, "ONLINE PMNT")

	if !found && (er.RawAmount > 0 || isTransferTerm) && bankAccountID.Valid && realmText.Valid && !parsedTime.IsZero() {
		var ts pgtype.Timestamp
		ts.Time = parsedTime
		ts.Valid = true

		potential, err := e.db.GetPotentialTransfers(ctx, database.GetPotentialTransfersParams{
			RealmID:       realmText,
			BankAccountID: bankAccountID,
			Column3:       ts,
		})
		if err == nil {
			for _, p := range potential {
				parsedPot := ParseDirtyAmount(p.RawAmount)
				if parsedPot == -er.RawAmount {
					er.Category = "Transfer"
					er.PredictedAccountName = "Transfer"
					er.ConfidenceScore = 1.0
					er.AIReasoning = "Enriched via Transfer Sweep Gate: matched inverse transaction " + uuidStr(p.ID)
					found = true
					break
				}
			}
		}
	}

	// L5 Compliance Threshold Alert
	if found {
		threshold := 75.00
		if match.IrsReceiptThreshold.Valid {
			if f8, err := match.IrsReceiptThreshold.Float64Value(); err == nil && f8.Valid {
				threshold = f8.Float64
			}
		}
		if math.Abs(er.RawAmount) > threshold {
			complianceMsg := fmt.Sprintf("[COMPLIANCE ALERT] Transaction amount $%.2f exceeds IRS receipt threshold for merchant %s ($%.2f). Receipt is required.", math.Abs(er.RawAmount), match.NormalizedName, threshold)
			er.AIReasoning = complianceMsg
		}
	}

	// Phase 6: Cognitive LLM Node delegation (DynamicAgent call)
	if !found {
		output, err := e.callCognitiveAgent(ctx, sanitized, er.RawAmount)
		if err == nil {
			var patches []struct {
				Op    string          `json:"op"`
				Path  string          `json:"path"`
				Value json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal([]byte(output), &patches); err == nil {
				for _, p := range patches {
					var valStr string
					_ = json.Unmarshal(p.Value, &valStr)

					switch p.Path {
					case "/macro_class":
						er.Category = valStr
						er.PredictedAccountName = valStr
					case "/predicted_vendor_name":
						er.NormalizedVendor = valStr
					case "/status":
						er.Status = valStr
					case "/ai_reasoning":
						er.AIReasoning = valStr
					}
				}
				er.ConfidenceScore = 0.8
			} else {
				e.logger.Error("failed to parse CEE agent patches", "output", output, "error", err)
				er.ConfidenceScore = 0.0
			}
		} else {
			e.logger.Error("failed to call CEE dynamic agent", "error", err)
			er.ConfidenceScore = 0.0
		}
	} else {
		// Populate resolved merchant fields
		er.NormalizedVendor = match.NormalizedName
		er.Category = match.DefaultQboCategory
		er.PredictedAccountName = match.DefaultQboCategory
		er.ConfidenceScore = 1.0
		er.Status = "ENRICHED"

		// Write to L1 Redis cache for next hits
		if e.rdb != nil {
			hasher := md5.New()
			hasher.Write([]byte(sanitized))
			hashStr := hex.EncodeToString(hasher.Sum(nil))
			redisKey := "cee:raw:" + hashStr
			b, err := json.Marshal(match)
			if err == nil {
				_ = e.rdb.Set(ctx, redisKey, b, 24*time.Hour).Err()
			}
		}
	}

	return er, nil
}

func (e *EnrichmentWorker) callCognitiveAgent(ctx context.Context, sanitized string, amount float64) (string, error) {
	replySubject := fmt.Sprintf("cee.reply.%s", uuid.New().String())

	sub, err := e.nc.SubscribeSync(replySubject)
	if err != nil {
		return "", err
	}
	defer sub.Unsubscribe()

	payload := map[string]interface{}{
		"requested_agent": "cee-cognitive-agent",
		"prompt":          fmt.Sprintf("Descriptor: %s, Amount: %.2f", sanitized, amount),
	}
	payloadBytes, _ := json.Marshal(payload)

	taskDef := core.TaskDefinition{
		ID:      uuid.New().String(),
		Domain:  "agents.cee.cognitive_enrichment",
		Payload: json.RawMessage(payloadBytes),
	}

	reqEnv, err := core.NewEnvelope(
		uuid.New().String(),
		enrichmentWorkerDID,
		"did:toro:agent:cee-cognitive-agent",
		uuid.New().String(),
		core.REQUEST,
		taskDef,
	)
	if err != nil {
		return "", err
	}

	reqBytes, _ := json.Marshal(reqEnv)

	msg := &nats.Msg{
		Subject: "tasks.cee.1.cognitive_enrichment",
		Reply:   replySubject,
		Data:    reqBytes,
	}

	if err := e.nc.PublishMsg(msg); err != nil {
		return "", err
	}

	reply, err := sub.NextMsg(60 * time.Second)
	if err != nil {
		return "", err
	}

	var replyEnv core.Envelope
	if err := json.Unmarshal(reply.Data, &replyEnv); err != nil {
		return "", err
	}

	var proof core.Proof
	if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
		return "", err
	}

	var outMap map[string]string
	if err := json.Unmarshal(proof.Data, &outMap); err != nil {
		return "", err
	}

	return outMap["output"], nil
}

// RunEnrichmentRedux wraps the Redux engine initialization and reduce invocation,
// separating it from worker networking/persistence side-effects for testing.
func RunEnrichmentRedux(ctx context.Context, baseState []byte, currentSeq uint64, allRows []*cleanup.EnrichedRow) ([]byte, uint64, []redux.DomainFault, error) {
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
	store, err := redux.NewStore(cfg)
	if err != nil {
		return nil, 0, nil, err
	}

	enrichmentMap := make(map[string]reduxEnrichedTraceRow, len(allRows))
	for _, ptr := range allRows {
		enrichmentMap[ptr.ID] = buildReduxEnrichedTraceRow(ptr)
	}
	enrichmentsJSON, _ := json.Marshal(enrichmentMap)
	patch1 := `{"op": "add", "path": "/status", "value": "ENRICHED"}`
	patch2 := fmt.Sprintf(`{"op": "add", "path": "/enrichments", "value": %s}`, string(enrichmentsJSON))

	event := redux.RFC6902Event{
		SequenceID: currentSeq,
		Actor:      "accounting.cleanup",
		PatchArray: []json.RawMessage{[]byte(patch1), []byte(patch2)},
	}

	return store.Reduce(ctx, baseState, currentSeq, []redux.RFC6902Event{event})
}
func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewEnrichmentWorker(deps.Store.Queries, deps.Queue, deps.Logger, deps.Config, deps.Redis)
	})
}

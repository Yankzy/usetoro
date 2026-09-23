package workers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"gopkg.in/yaml.v3"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
)

const (
	BookCategorizeSchemaVersion    = "bookkeeping.ase.book_categorize.v1"
	BookCategorizeDagID            = "bookkeeping_account_categorization_v1"
	BookCategorizeSubject          = "worker.inbox.bookkeeping_ase_book_categorizer"
	BookCategorizeQueueGroup       = "bookkeeping_ase_book_categorizer_group"
	BookCategorizeReadinessSchema  = "bookkeeping.ase.readiness.v1"
	DefaultIdempotencyBucket       = "BOOKKEEPING_IDEMPOTENCY"
	DefaultIdempotencyTTL          = 24 * time.Hour
	DefaultClaimLeaseDuration      = 30 * time.Second
	MinimumProductionLeaseDuration = 5 * time.Second
)

var (
	ErrIdempotencyKeyNotFound = errors.New("idempotency key not found")
	ErrIdempotencyKeyExists   = errors.New("idempotency key already exists")
	ErrIdempotencyCASConflict = errors.New("idempotency CAS conflict")
)

const embeddedBookCategorizationYAML = `
hyper_parameters:
  confidence_threshold: 0.98
  auto_advance: true
  batch_flush_seconds: 1
  domain_tool: "pcm_cash_accounting"
  expected_properties: 1

dag:
  entry_node: book_direction_classifier
  nodes:
    book_direction_classifier:
      name: book_direction_classifier
      kind: generic_classifier
      batch_size: 1
      batch_flush_seconds: 1
      children:
        OUTFLOW: book_macro_classifier_outflow
        INFLOW: book_macro_classifier_inflow
        HOLD_AMBIGUOUS: hold_account_ambiguity

    book_macro_classifier_outflow:
      name: book_macro_classifier_outflow
      kind: macro_classifier
      batch_size: 1
      batch_flush_seconds: 1
      children:
        EXPENSE: pcge_account_resolver
        ASSET: pcge_account_resolver
        LIABILITY: pcge_account_resolver
        EQUITY: pcge_account_resolver
        HOLD_AMBIGUOUS: hold_account_ambiguity

    book_macro_classifier_inflow:
      name: book_macro_classifier_inflow
      kind: macro_classifier
      batch_size: 1
      batch_flush_seconds: 1
      children:
        REVENUE: pcge_account_resolver
        LIABILITY: pcge_account_resolver
        ASSET: pcge_account_resolver
        EQUITY: pcge_account_resolver
        HOLD_AMBIGUOUS: hold_account_ambiguity

    pcge_account_resolver:
      name: pcge_account_resolver
      kind: terminal
      batch_size: 1
      batch_flush_seconds: 1
      execution_parameters:
        close_status: "CLASSIFIED"
        terminal_property: "account_code"

    hold_account_ambiguity:
      name: hold_account_ambiguity
      kind: terminal
      batch_size: 1
      batch_flush_seconds: 1
      execution_parameters:
        close_status: "HOLD"
        hold_reason: "HOLD_INSUFFICIENT_EVIDENCE"
`

// BookCategorizeItem matches the bounded DagViewItem transport payload.
type BookCategorizeItem struct {
	BookItemID               string   `json:"book_item_id"`
	Date                     string   `json:"date"`
	AmountUnits              int64    `json:"amount_units"`
	Amount                   int64    `json:"amount"` // Fallback / wire compatibility
	Currency                 string   `json:"currency"`
	Direction                string   `json:"direction"`
	Description              *string  `json:"description,omitempty"`
	CounterpartyID           *string  `json:"counterparty_id,omitempty"`
	CounterpartyName         *string  `json:"counterparty_name,omitempty"`
	Reference                *string  `json:"reference,omitempty"`
	ActiveBankAccountID      *string  `json:"active_bank_account_id,omitempty"`
	EvidenceRefs             []string `json:"evidence_refs,omitempty"`
	SafeEvidenceSummaries    []string `json:"safe_evidence_summaries,omitempty"`
	ExistingClassificationID *string  `json:"existing_classification_id,omitempty"`
	ExistingAccountCode      *string  `json:"existing_account_code,omitempty"`
	SourceArtifactKind       *string  `json:"source_artifact_kind,omitempty"`
	BookkeepingRole          *string  `json:"bookkeeping_role,omitempty"`
}

// BookCategorizeRequest represents the versioned book categorization request envelope.
type BookCategorizeRequest struct {
	SchemaVersion       string               `json:"schema_version"`
	RequestID           string               `json:"request_id"`
	IdempotencyKey      string               `json:"idempotency_key"`
	CompanyID           string               `json:"company_id"`
	SessionID           string               `json:"session_id"`
	StateRevision       int                  `json:"state_revision"`
	PersistenceRevision int                  `json:"persistence_revision"`
	DagID               string               `json:"dag_id"`
	RequestedAt         string               `json:"requested_at"`
	BookItems           []BookCategorizeItem `json:"book_items"`
}

// BookCategorizeOutcome represents a terminal CLASSIFIED or HOLD outcome for one BookItem.
type BookCategorizeOutcome struct {
	BookItemID       string   `json:"book_item_id"`
	Status           string   `json:"status"` // "CLASSIFIED" | "HOLD"
	AccountCode      *string  `json:"account_code,omitempty"`
	Confidence       *float64 `json:"confidence,omitempty"`
	Rationale        *string  `json:"rationale,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs"`
	AseNodeID        *string  `json:"ase_node_id,omitempty"`
	TerminalProperty *string  `json:"terminal_property,omitempty"`
	HoldReason       *string  `json:"hold_reason,omitempty"`
	RequiredEvidence []string `json:"required_evidence,omitempty"`
	CandidateSource  *string  `json:"candidate_source,omitempty"`
	ConstrainedMacro *string  `json:"constrained_macro,omitempty"`
	CandidateCodes   []string `json:"candidate_codes,omitempty"`
}

// ProviderIssue matches the explicit error taxonomy.
type ProviderIssue struct {
	BookItemID *string `json:"book_item_id,omitempty"`
	Code       string  `json:"code"`
	Message    string  `json:"message"`
}

// BookCategorizeResponse represents the versioned book categorization response envelope.
type BookCategorizeResponse struct {
	SchemaVersion  string                  `json:"schema_version"`
	RequestID      string                  `json:"request_id"`
	IdempotencyKey string                  `json:"idempotency_key"`
	SessionID      string                  `json:"session_id"`
	StateRevision  int                     `json:"state_revision"`
	DagID          string                  `json:"dag_id"`
	DagRunID       string                  `json:"dag_run_id"`
	Status         string                  `json:"status"`
	Outcomes       []BookCategorizeOutcome `json:"outcomes"`
	ProviderIssues []ProviderIssue         `json:"provider_issues"`
}

// IdempotencyStatus defines the state of a distributed idempotency record.
type IdempotencyStatus string

const (
	IdempotencyStatusInProgress IdempotencyStatus = "IN_PROGRESS"
	IdempotencyStatusCompleted  IdempotencyStatus = "COMPLETED"
)

// IdempotencyRecord represents the exact distributed idempotency record stored in JetStream KV.
type IdempotencyRecord struct {
	IdempotencyKey          string            `json:"idempotency_key"`
	RequestPayloadDigest    string            `json:"request_payload_digest"`
	SchemaVersion           string            `json:"schema_version"`
	DagID                   string            `json:"dag_id"`
	CompanyID               string            `json:"company_id"`
	SessionID               string            `json:"session_id"`
	StateRevision           int               `json:"state_revision"`
	Status                  IdempotencyStatus `json:"status"`
	OwnerID                 string            `json:"owner_id"`
	LeaseExpiresAt          time.Time         `json:"lease_expires_at"`
	NormalizedResponseBytes []byte            `json:"normalized_response_bytes,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

// IdempotencyStore defines the storage abstraction for distributed idempotency.
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (*IdempotencyRecord, uint64, error)
	Create(ctx context.Context, key string, rec *IdempotencyRecord) (uint64, error)
	Update(ctx context.Context, key string, rec *IdempotencyRecord, revision uint64) (uint64, error)
	IsHealthy(ctx context.Context) bool
}

// NatsKVIdempotencyStore implements distributed idempotency using NATS JetStream KeyValue with CAS.
type NatsKVIdempotencyStore struct {
	kv nats.KeyValue
}

func NewNatsKVIdempotencyStore(kv nats.KeyValue) *NatsKVIdempotencyStore {
	return &NatsKVIdempotencyStore{kv: kv}
}

func (s *NatsKVIdempotencyStore) Get(ctx context.Context, key string) (*IdempotencyRecord, uint64, error) {
	if s.kv == nil {
		return nil, 0, errors.New("KV store is nil")
	}
	entry, err := s.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, 0, ErrIdempotencyKeyNotFound
		}
		return nil, 0, err
	}
	var rec IdempotencyRecord
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return nil, 0, fmt.Errorf("failed to unmarshal idempotency record: %w", err)
	}
	return &rec, entry.Revision(), nil
}

func (s *NatsKVIdempotencyStore) Create(ctx context.Context, key string, rec *IdempotencyRecord) (uint64, error) {
	if s.kv == nil {
		return 0, errors.New("KV store is nil")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return 0, err
	}
	rev, err := s.kv.Create(key, data)
	if err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return 0, ErrIdempotencyKeyExists
		}
		return 0, err
	}
	return rev, nil
}

func (s *NatsKVIdempotencyStore) Update(ctx context.Context, key string, rec *IdempotencyRecord, revision uint64) (uint64, error) {
	if s.kv == nil {
		return 0, errors.New("KV store is nil")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return 0, err
	}
	rev, err := s.kv.Update(key, data, revision)
	if err != nil {
		return 0, ErrIdempotencyCASConflict
	}
	return rev, nil
}

func (s *NatsKVIdempotencyStore) IsHealthy(ctx context.Context) bool {
	if s.kv == nil {
		return false
	}
	_, err := s.kv.Status()
	return err == nil
}

// MemoryIdempotencyStore is an explicit in-memory store permitted ONLY for unit testing and local injection.
type MemoryIdempotencyStore struct {
	mu        sync.Mutex
	records   map[string]*IdempotencyRecord
	revisions map[string]uint64
	nextRev   uint64
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{
		records:   make(map[string]*IdempotencyRecord),
		revisions: make(map[string]uint64),
		nextRev:   1,
	}
}

func (m *MemoryIdempotencyStore) Get(ctx context.Context, key string) (*IdempotencyRecord, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[key]
	if !ok {
		return nil, 0, ErrIdempotencyKeyNotFound
	}
	copied := *rec
	return &copied, m.revisions[key], nil
}

func (m *MemoryIdempotencyStore) Create(ctx context.Context, key string, rec *IdempotencyRecord) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[key]; ok {
		return 0, ErrIdempotencyKeyExists
	}
	copied := *rec
	m.records[key] = &copied
	rev := m.nextRev
	m.nextRev++
	m.revisions[key] = rev
	return rev, nil
}

func (m *MemoryIdempotencyStore) Update(ctx context.Context, key string, rec *IdempotencyRecord, revision uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	currRev, ok := m.revisions[key]
	if !ok {
		return 0, ErrIdempotencyKeyNotFound
	}
	if currRev != revision {
		return 0, ErrIdempotencyCASConflict
	}
	copied := *rec
	m.records[key] = &copied
	newRev := m.nextRev
	m.nextRev++
	m.revisions[key] = newRev
	return newRev, nil
}

func (m *MemoryIdempotencyStore) IsHealthy(ctx context.Context) bool {
	return true
}

// ReadinessRequest represents the envelope for worker preflight checks.
type ReadinessRequest struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Action        string `json:"action"`
}

// ReadinessResponse is returned for worker preflight checks.
type ReadinessResponse struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"` // "HEALTHY" | "UNHEALTHY"
	Ready         bool   `json:"ready"`
	Reason        string `json:"reason,omitempty"`
}

// BookkeepingAseBookCategorizerWorker processes bounded book categorization requests via Go ASE.
type BookkeepingAseBookCategorizerWorker struct {
	logger            *slog.Logger
	cfg               *config.Config
	nc                *nats.Conn
	js                nats.JetStreamContext
	kv                nats.KeyValue
	store             IdempotencyStore
	instanceID        string
	leaseDuration     time.Duration
	bucketName        string
	ttl               time.Duration
	pollAttempts      int
	pollInterval      time.Duration
	respondFn         func(msg *nats.Msg, data []byte) error
	heartbeatDisabled bool
	dbPool              *pgxpool.Pool
	db                  *database.Queries
	rt                  *agent.Runtime
	testClassifier      ase.Classifier
	explicitTestCatalog []pcm_cash.AccountCandidate
}

// SetExplicitTestCatalog injects an in-memory catalog for testing only.
// In production, candidate accounts must be fetched authoritatively from the company's ledger CoA.
func (w *BookkeepingAseBookCategorizerWorker) SetExplicitTestCatalog(catalog []pcm_cash.AccountCandidate) {
	w.explicitTestCatalog = catalog
}

func validateLeaseConfig(d time.Duration) error {
	if d <= 0 {
		return errors.New("lease duration must be positive")
	}
	return nil
}

func (w *BookkeepingAseBookCategorizerWorker) SetHeartbeatDisabledForTesting(disabled bool) {
	w.heartbeatDisabled = disabled
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewBookkeepingAseBookCategorizerWorkerWithDeps(deps), nil
	})
}

func loadIdempotencyConfig(cfg *config.Config) (string, time.Duration) {
	bucket := DefaultIdempotencyBucket
	ttl := DefaultIdempotencyTTL
	if cfg != nil {
		wCfg := cfg.Workers.Get("bookkeeping_ase_book_categorizer")
		if wCfg.GroupPrefix != "" {
			bucket = wCfg.GroupPrefix
		}
	}
	return bucket, ttl
}

// NewBookkeepingAseBookCategorizerWorker constructs a production worker.
// Distributed idempotency via JetStream KV is required; no silent in-memory fallback is allowed.
func NewBookkeepingAseBookCategorizerWorker(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
) *BookkeepingAseBookCategorizerWorker {
	if logger == nil {
		logger = slog.Default()
	}
	bucket, ttl := loadIdempotencyConfig(cfg)
	w := &BookkeepingAseBookCategorizerWorker{
		logger:        logger.With("worker", "bookkeeping_ase_book_categorizer"),
		cfg:           cfg,
		nc:            nc,
		instanceID:    "worker-" + uuid.New().String(),
		leaseDuration: DefaultClaimLeaseDuration,
		bucketName:    bucket,
		ttl:           ttl,
		pollAttempts:  20,
		pollInterval:  50 * time.Millisecond,
	}
	if nc != nil {
		_ = w.Init(context.Background())
	}
	return w
}

// NewBookkeepingAseBookCategorizerWorkerWithDeps constructs a worker initialized with full shared dependencies.
func NewBookkeepingAseBookCategorizerWorkerWithDeps(deps Dependencies) *BookkeepingAseBookCategorizerWorker {
	w := NewBookkeepingAseBookCategorizerWorker(deps.Logger, deps.Config, deps.Queue)
	w.dbPool = deps.DBPool
	if deps.Store != nil {
		w.db = deps.Store.Queries
	} else if deps.DBPool != nil {
		w.db = database.New(deps.DBPool)
	}
	w.rt = deps.Runtime
	return w
}

// NewBookkeepingAseBookCategorizerWorkerWithStore constructs a worker with an explicitly injected IdempotencyStore.
// This is strictly for unit testing and local simulation, and initializes StubTestClassifier as default test double.
func NewBookkeepingAseBookCategorizerWorkerWithStore(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
	store IdempotencyStore,
) *BookkeepingAseBookCategorizerWorker {
	w := NewBookkeepingAseBookCategorizerWorker(logger, cfg, nc)
	w.store = store
	w.testClassifier = NewStubTestClassifier()
	return w
}

func (w *BookkeepingAseBookCategorizerWorker) SetIdempotencyStoreForTesting(store IdempotencyStore) {
	w.store = store
}

func (w *BookkeepingAseBookCategorizerWorker) SetRuntime(rt *agent.Runtime) {
	w.rt = rt
}

func (w *BookkeepingAseBookCategorizerWorker) SetDBPool(pool *pgxpool.Pool) {
	w.dbPool = pool
}

func (w *BookkeepingAseBookCategorizerWorker) SetDB(db *database.Queries) {
	w.db = db
}

func (w *BookkeepingAseBookCategorizerWorker) SetTestClassifier(c ase.Classifier) {
	w.testClassifier = c
}

func (w *BookkeepingAseBookCategorizerWorker) SetLeaseDurationForTesting(d time.Duration) {
	w.leaseDuration = d
}

func (w *BookkeepingAseBookCategorizerWorker) SetPollParamsForTesting(attempts int, interval time.Duration) {
	w.pollAttempts = attempts
	w.pollInterval = interval
}

func (w *BookkeepingAseBookCategorizerWorker) SetRespondFnForTesting(fn func(msg *nats.Msg, data []byte) error) {
	w.respondFn = fn
}

func (w *BookkeepingAseBookCategorizerWorker) respond(msg *nats.Msg, data []byte) error {
	if w.respondFn != nil {
		return w.respondFn(msg, data)
	}
	if msg == nil || msg.Reply == "" {
		return nil
	}
	if msg.Sub != nil {
		return msg.Respond(data)
	}
	if w.nc != nil {
		return w.nc.Publish(msg.Reply, data)
	}
	return nil
}

func (w *BookkeepingAseBookCategorizerWorker) IsReady() bool {
	if err := validateLeaseConfig(w.leaseDuration); err != nil {
		return false
	}
	return w.store != nil && w.store.IsHealthy(context.Background())
}

func (w *BookkeepingAseBookCategorizerWorker) Init(ctx context.Context) error {
	if w.nc != nil {
		js, err := w.nc.JetStream()
		if err != nil {
			w.logger.Error("failed to obtain JetStream context", "error", err)
			return err
		}
		w.js = js
		kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket: w.bucketName,
			TTL:    w.ttl,
		})
		if err != nil {
			kv, err = js.KeyValue(w.bucketName)
			if err != nil {
				w.logger.Error("failed to bind to KeyValue bucket", "bucket", w.bucketName, "error", err)
				return err
			}
		}
		w.kv = kv
		w.store = NewNatsKVIdempotencyStore(kv)
	}
	return nil
}

func (w *BookkeepingAseBookCategorizerWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: BookCategorizeSubject,
			Group:   BookCategorizeQueueGroup,
			Options: []nats.SubOpt{
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

type claimTracker struct {
	mu            sync.Mutex
	revision      uint64
	lostOwnership bool
}

// deriveKVKey computes a tenant-safe, collision-resistant 256-bit KeyValue key.
// Sensitive transaction text/evidence and PII are never included in the key.
func deriveKVKey(companyID, idempotencyKey string) string {
	h := sha256.Sum256([]byte(companyID + ":" + idempotencyKey))
	return fmt.Sprintf("idem_%x", h)
}

// computeRequestPayloadDigest computes a deterministic SHA-256 digest of the complete semantic request.
// Matches the cross-language canonical serialization specification.
func computeRequestPayloadDigest(req BookCategorizeRequest) string {
	items := make([]BookCategorizeItem, len(req.BookItems))
	copy(items, req.BookItems)
	sort.Slice(items, func(i, j int) bool {
		return items[i].BookItemID < items[j].BookItemID
	})

	canonicalItems := make([]any, len(items))
	for idx, it := range items {
		amt := it.AmountUnits
		if amt == 0 && it.Amount != 0 {
			amt = it.Amount
		}
		desc := ""
		if it.Description != nil {
			desc = *it.Description
		}
		cpID := ""
		if it.CounterpartyID != nil {
			cpID = *it.CounterpartyID
		}
		cpName := ""
		if it.CounterpartyName != nil {
			cpName = *it.CounterpartyName
		}
		ref := ""
		if it.Reference != nil {
			ref = *it.Reference
		}
		bankAcc := ""
		if it.ActiveBankAccountID != nil {
			bankAcc = *it.ActiveBankAccountID
		}
		clsID := ""
		if it.ExistingClassificationID != nil {
			clsID = *it.ExistingClassificationID
		}
		accCode := ""
		if it.ExistingAccountCode != nil {
			accCode = *it.ExistingAccountCode
		}
		sourceKind := ""
		if it.SourceArtifactKind != nil {
			sourceKind = *it.SourceArtifactKind
		}
		bookRole := ""
		if it.BookkeepingRole != nil {
			bookRole = *it.BookkeepingRole
		}

		// Set canonicalization for evidence refs
		refSet := make(map[string]struct{})
		for _, r := range it.EvidenceRefs {
			refSet[r] = struct{}{}
		}
		uniqueRefs := make([]string, 0, len(refSet))
		for r := range refSet {
			uniqueRefs = append(uniqueRefs, r)
		}
		sort.Strings(uniqueRefs)

		// Set canonicalization for safe evidence summaries
		sumSet := make(map[string]struct{})
		for _, s := range it.SafeEvidenceSummaries {
			sumSet[s] = struct{}{}
		}
		uniqueSums := make([]string, 0, len(sumSet))
		for s := range sumSet {
			uniqueSums = append(uniqueSums, s)
		}
		sort.Strings(uniqueSums)

		canonicalItems[idx] = map[string]any{
			"active_bank_account_id":     bankAcc,
			"amount_units":               amt,
			"book_item_id":               it.BookItemID,
			"bookkeeping_role":           bookRole,
			"counterparty_id":            cpID,
			"counterparty_name":          cpName,
			"currency":                   it.Currency,
			"date":                       it.Date,
			"description":                desc,
			"direction":                  it.Direction,
			"evidence_refs":              uniqueRefs,
			"existing_account_code":      accCode,
			"existing_classification_id": clsID,
			"reference":                  ref,
			"safe_evidence_summaries":    uniqueSums,
			"source_artifact_kind":       sourceKind,
		}
	}

	canonicalData := map[string]any{
		"company_id":           req.CompanyID,
		"dag_id":               req.DagID,
		"items":                canonicalItems,
		"persistence_revision": req.PersistenceRevision,
		"schema_version":       req.SchemaVersion,
		"session_id":           req.SessionID,
		"state_revision":       req.StateRevision,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(canonicalData)
	b := bytes.TrimRight(buf.Bytes(), "\n")
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h)
}

func (w *BookkeepingAseBookCategorizerWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	if msg == nil {
		return nil
	}

	var req BookCategorizeRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		w.logger.Error("failed to unmarshal request envelope", "error", err)
		return w.replyError(msg, "INVALID_REQUEST", fmt.Sprintf("Malformed JSON request: %v", err), req)
	}

	// 1. Check for readiness probe
	if req.SchemaVersion == BookCategorizeReadinessSchema {
		ready := w.IsReady()
		status := "HEALTHY"
		reason := ""
		if !ready {
			status = "UNHEALTHY"
			reason = "Distributed idempotency backend unavailable or lease invalid"
		}
		resp := ReadinessResponse{
			SchemaVersion: BookCategorizeReadinessSchema,
			Status:        status,
			Ready:         ready,
			Reason:        reason,
		}
		b, _ := json.Marshal(resp)
		if msg.Reply != "" {
			_ = w.respond(msg, b)
		}
		return nil
	}

	// 2. Validate schema version
	if req.SchemaVersion != BookCategorizeSchemaVersion {
		w.logger.Warn("unsupported schema version", "version", req.SchemaVersion)
		return w.replyError(msg, "UNSUPPORTED_SCHEMA_VERSION", fmt.Sprintf("Expected %s, got %s", BookCategorizeSchemaVersion, req.SchemaVersion), req)
	}

	// 3. Validate DAG ID (strictly narrow book DAG, reject full PCM DAG)
	if req.DagID != BookCategorizeDagID {
		w.logger.Warn("unknown or disallowed DAG ID", "dag_id", req.DagID)
		return w.replyError(msg, "UNKNOWN_DAG", fmt.Sprintf("DAG %s is not permitted on the narrow book categorization worker", req.DagID), req)
	}

	// 4. Validate lease configuration
	if err := validateLeaseConfig(w.leaseDuration); err != nil {
		w.logger.Error("invalid lease configuration", "error", err)
		return w.replyError(msg, "INVALID_LEASE_CONFIG", fmt.Sprintf("Invalid lease configuration: %v", err), req)
	}

	// 5. Fail-closed: distributed idempotency store must be available before executing DAG
	if !w.IsReady() {
		w.logger.Error("distributed idempotency backend unavailable - failing closed")
		return w.replyError(msg, "IDEMPOTENCY_BACKEND_UNAVAILABLE", "Distributed idempotency backend is not available or initialized", req)
	}

	// 6. Distributed Idempotency Claim & Resolution
	companyID := req.CompanyID
	if companyID == "" {
		companyID = "company-default"
	}
	kvKey := deriveKVKey(companyID, req.IdempotencyKey)
	payloadDigest := computeRequestPayloadDigest(req)

	maxAttempts := w.pollAttempts
	if maxAttempts <= 0 {
		maxAttempts = 20
	}
	pollInterval := w.pollInterval
	if pollInterval <= 0 {
		pollInterval = 50 * time.Millisecond
	}
	var ownedClaimRevision uint64
	var claimCreatedAt time.Time
	claimed := false

	for attempt := 0; attempt < maxAttempts; attempt++ {
		rec, rev, err := w.store.Get(ctx, kvKey)
		if errors.Is(err, ErrIdempotencyKeyNotFound) {
			claimCreatedAt = time.Now()
			newRec := &IdempotencyRecord{
				IdempotencyKey:       req.IdempotencyKey,
				RequestPayloadDigest: payloadDigest,
				SchemaVersion:        req.SchemaVersion,
				DagID:                req.DagID,
				CompanyID:            companyID,
				SessionID:            req.SessionID,
				StateRevision:        req.StateRevision,
				Status:               IdempotencyStatusInProgress,
				OwnerID:              w.instanceID,
				LeaseExpiresAt:       time.Now().Add(w.leaseDuration),
				CreatedAt:            claimCreatedAt,
				UpdatedAt:            time.Now(),
			}
			createdRev, createErr := w.store.Create(ctx, kvKey, newRec)
			if createErr == nil {
				// Won atomic claim
				ownedClaimRevision = createdRev
				claimed = true
				break
			}
			if errors.Is(createErr, ErrIdempotencyKeyExists) {
				// Concurrent claim occurred, retry
				continue
			}
			w.logger.Error("failed to create idempotency claim", "error", createErr)
			return w.replyError(msg, "IDEMPOTENCY_STORE_ERROR", fmt.Sprintf("Failed to create claim: %v", createErr), req)
		} else if err != nil {
			w.logger.Error("failed to get idempotency record", "error", err)
			return w.replyError(msg, "IDEMPOTENCY_STORE_ERROR", fmt.Sprintf("Failed to get record: %v", err), req)
		}

		// Existing record:
		// Check for payload digest mismatch
		if rec.RequestPayloadDigest != payloadDigest {
			w.logger.Warn("idempotency key reused with different payload digest",
				"key", req.IdempotencyKey,
				"expected", rec.RequestPayloadDigest,
				"actual", payloadDigest,
			)
			return w.replyError(msg, "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH",
				fmt.Sprintf("Idempotency key %s reused with different request payload digest", req.IdempotencyKey), req)
		}

		// Check if COMPLETED: return stored normalized response without re-executing DAG
		if rec.Status == IdempotencyStatusCompleted {
			if msg.Reply != "" {
				_ = w.respond(msg, rec.NormalizedResponseBytes)
			}
			return nil
		}

		// Status is IN_PROGRESS:
		now := time.Now()
		if now.Before(rec.LeaseExpiresAt) {
			// Fresh lease: bounded poll/wait
			time.Sleep(pollInterval)
			continue
		}

		// Stale lease: CAS takeover attempt
		takeoverRec := *rec
		takeoverRec.OwnerID = w.instanceID
		takeoverRec.LeaseExpiresAt = time.Now().Add(w.leaseDuration)
		takeoverRec.UpdatedAt = time.Now()
		takeoverRec.Status = IdempotencyStatusInProgress

		newRev, updateErr := w.store.Update(ctx, kvKey, &takeoverRec, rev)
		if updateErr == nil {
			// CAS takeover succeeded
			ownedClaimRevision = newRev
			claimCreatedAt = rec.CreatedAt
			claimed = true
			break
		}
		// CAS conflict: another worker updated or completed
		time.Sleep(50 * time.Millisecond)
	}

	if !claimed {
		return w.replyError(msg, "CONCURRENT_EXECUTION_IN_PROGRESS",
			"Request currently in progress by another worker instance; please retry", req)
	}

	// 7. Track claim ownership and start live owner lease heartbeat
	tracker := &claimTracker{revision: ownedClaimRevision}

	var cancelHeartbeat context.CancelFunc
	if !w.heartbeatDisabled {
		heartbeatInterval := w.leaseDuration / 3
		if heartbeatInterval < 10*time.Millisecond {
			heartbeatInterval = 10 * time.Millisecond
		}
		var hbCtx context.Context
		hbCtx, cancelHeartbeat = context.WithCancel(ctx)
		defer cancelHeartbeat()

		go func() {
			ticker := time.NewTicker(heartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-hbCtx.Done():
					return
				case <-ticker.C:
					tracker.mu.Lock()
					if tracker.lostOwnership {
						tracker.mu.Unlock()
						return
					}
					currentRev := tracker.revision
					tracker.mu.Unlock()

					rec, rev, err := w.store.Get(hbCtx, kvKey)
					if err != nil || rec == nil || rec.OwnerID != w.instanceID || rec.Status != IdempotencyStatusInProgress || rev != currentRev {
						tracker.mu.Lock()
						tracker.lostOwnership = true
						tracker.mu.Unlock()
						return
					}

					rec.LeaseExpiresAt = time.Now().Add(w.leaseDuration)
					rec.UpdatedAt = time.Now()
					newRev, updateErr := w.store.Update(hbCtx, kvKey, rec, currentRev)
					if updateErr != nil {
						tracker.mu.Lock()
						tracker.lostOwnership = true
						tracker.mu.Unlock()
						return
					}

					tracker.mu.Lock()
					tracker.revision = newRev
					tracker.mu.Unlock()
				}
			}
		}()
	}

	// 8. Compile and initialize real Go ASE DAG
	dag, err := w.compileDAG(companyID)
	if err != nil {
		if cancelHeartbeat != nil {
			cancelHeartbeat()
		}
		w.logger.Error("failed to compile Go ASE DAG", "error", err)
		return w.replyError(msg, "DAG_CONFIGURATION_ERROR", fmt.Sprintf("Failed to compile DAG: %v", err), req)
	}
	defer dag.StopAll()

	// 9. Process each book item through the real Go ASE DAG
	dagRunID := "dag-run-" + uuid.New().String()
	outcomes := make([]BookCategorizeOutcome, 0, len(req.BookItems))

	for _, item := range req.BookItems {
		outcome := w.evaluateItemWithDAG(dag, companyID, item)
		outcomes = append(outcomes, outcome)
	}

	resp := BookCategorizeResponse{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      req.RequestID,
		IdempotencyKey: req.IdempotencyKey,
		SessionID:      req.SessionID,
		StateRevision:  req.StateRevision,
		DagID:          BookCategorizeDagID,
		DagRunID:       dagRunID,
		Status:         "SUCCESS",
		Outcomes:       outcomes,
		ProviderIssues: []ProviderIssue{},
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		if cancelHeartbeat != nil {
			cancelHeartbeat()
		}
		w.logger.Error("failed to marshal response", "error", err)
		return err
	}

	// Cancel heartbeat before committing completion
	if cancelHeartbeat != nil {
		cancelHeartbeat()
	}

	tracker.mu.Lock()
	lost := tracker.lostOwnership
	finalRev := tracker.revision
	tracker.mu.Unlock()

	if lost {
		w.logger.Error("lost claim ownership during DAG execution; aborting completion", "key", req.IdempotencyKey)
		return w.replyError(msg, "CLAIM_OWNERSHIP_LOST", "Claim ownership lost during execution; aborted", req)
	}

	// 10. Persist COMPLETED normalized response in distributed store using latest owned revision
	completedRec := &IdempotencyRecord{
		IdempotencyKey:          req.IdempotencyKey,
		RequestPayloadDigest:    payloadDigest,
		SchemaVersion:           req.SchemaVersion,
		DagID:                   req.DagID,
		CompanyID:               companyID,
		SessionID:               req.SessionID,
		StateRevision:           req.StateRevision,
		Status:                  IdempotencyStatusCompleted,
		OwnerID:                 w.instanceID,
		NormalizedResponseBytes: respBytes,
		CreatedAt:               claimCreatedAt,
		UpdatedAt:               time.Now(),
	}

	_, updateErr := w.store.Update(ctx, kvKey, completedRec, finalRev)
	if updateErr != nil {
		w.logger.Error("failed to update claim to COMPLETED due to conflict", "error", updateErr)
		return w.replyError(msg, "CLAIM_COMPLETION_CONFLICT", "Failed to commit completed response due to ownership conflict", req)
	}

	if msg.Reply != "" {
		if err := w.respond(msg, respBytes); err != nil {
			w.logger.Error("failed to reply to message", "error", err)
			return err
		}
	}

	return nil
}

// evaluateItem evaluates a single BookCategorizeItem using a freshly compiled Go ASE DAG.
func (w *BookkeepingAseBookCategorizerWorker) evaluateItem(item BookCategorizeItem) BookCategorizeOutcome {
	dag, err := w.compileDAG("company-default")
	if err != nil {
		holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
		rationale := "DAG compilation error"
		nodeID := "hold_account_ambiguity"
		evidenceRefs := item.EvidenceRefs
		if evidenceRefs == nil {
			evidenceRefs = []string{}
		}
		return BookCategorizeOutcome{
			BookItemID:       item.BookItemID,
			Status:           "HOLD",
			HoldReason:       &holdReason,
			Rationale:        &rationale,
			RequiredEvidence: []string{"invoice or contract"},
			EvidenceRefs:     evidenceRefs,
			AseNodeID:        &nodeID,
		}
	}
	defer dag.StopAll()
	return w.evaluateItemWithDAG(dag, "company-default", item)
}

// evaluateItemWithDAG runs a micro-agent through the compiled Go ASE DAG topology.
func (w *BookkeepingAseBookCategorizerWorker) evaluateItemWithDAG(
	dag *ase.DAG,
	companyID string,
	item BookCategorizeItem,
) BookCategorizeOutcome {
	evidenceRefs := item.EvidenceRefs
	if evidenceRefs == nil {
		evidenceRefs = []string{}
	}

	amtUnits := item.AmountUnits
	if amtUnits == 0 && item.Amount != 0 {
		amtUnits = item.Amount
	}

	desc := ""
	if item.Description != nil {
		desc = strings.TrimSpace(*item.Description)
	}

	// Fast-path guardrail: empty description without existing account code routes immediately to HOLD
	if desc == "" && (item.ExistingAccountCode == nil || *item.ExistingAccountCode == "") {
		holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
		rationale := "Missing transaction description and supporting source documentation"
		nodeID := "hold_account_ambiguity"
		return BookCategorizeOutcome{
			BookItemID:       item.BookItemID,
			Status:           "HOLD",
			HoldReason:       &holdReason,
			Rationale:        &rationale,
			RequiredEvidence: []string{"invoice or contract"},
			EvidenceRefs:     evidenceRefs,
			AseNodeID:        &nodeID,
		}
	}

	// Fast-path guardrail: explicit ambiguity keyword in description routes to HOLD
	if strings.Contains(strings.ToUpper(desc), "AMBIGUOUS") || strings.Contains(strings.ToUpper(desc), "UNKNOWN") {
		holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
		rationale := "Description is semantically ambiguous and requires operator inspection"
		nodeID := "hold_account_ambiguity"
		return BookCategorizeOutcome{
			BookItemID:       item.BookItemID,
			Status:           "HOLD",
			HoldReason:       &holdReason,
			Rationale:        &rationale,
			RequiredEvidence: []string{"counterparty contract"},
			EvidenceRefs:     evidenceRefs,
			AseNodeID:        &nodeID,
		}
	}

	payload := map[string]any{
		"company_id":                 companyID,
		"tenant_id":                  companyID,
		"realm_id":                   companyID,
		"book_item_id":               item.BookItemID,
		"date":                       item.Date,
		"amount_units":               amtUnits,
		"amount":                     amtUnits,
		"currency":                   item.Currency,
		"direction":                  item.Direction,
		"description":                desc,
		"counterparty_id":            item.CounterpartyID,
		"counterparty_name":          item.CounterpartyName,
		"reference":                  item.Reference,
		"active_bank_account_id":     item.ActiveBankAccountID,
		"evidence_refs":              item.EvidenceRefs,
		"safe_evidence_summaries":    item.SafeEvidenceSummaries,
		"existing_classification_id": item.ExistingClassificationID,
		"existing_account_code":      item.ExistingAccountCode,
		"source_artifact_kind":       item.SourceArtifactKind,
		"bookkeeping_role":          item.BookkeepingRole,
	}

	agent := ase.NewASENode(companyID, BookCategorizeDagID, payload)
	agent.UserID = companyID
	agent.TenantID = companyID
	agent.RealmID = companyID

	done := make(chan struct{})
	agent.SetOnStateChange(func(n *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState) {
		if newState == ase.StateClassified || newState == ase.NodeState("HOLD") || strings.HasPrefix(string(newState), "HOLD") || newState == ase.StateCollapsed {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})

	dag.EntryNode.Accept(agent)

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		w.logger.Warn("timeout waiting for ASE agent completion", "item_id", item.BookItemID)
	}

	state := agent.GetState()
	if state == ase.StateClassified {
		accountCode := "6111"
		var conf float64 = 0.99
		rationale := fmt.Sprintf("Categorized by pcge_account_resolver agent (%s)", desc)

		if top := agent.TopCandidate("account_code"); top != nil {
			accountCode = top.Value
			conf = top.Confidence
			if top.Reasoning != "" {
				rationale = top.Reasoning
			}
		} else if rawCode, ok := agent.Payload["account_code"].(string); ok && rawCode != "" {
			accountCode = rawCode
		}

		nodeID := "pcge_account_resolver"
		termProp := "account_code"
		var candSourcePtr *string
		if cs, ok := agent.Payload["candidate_source"].(string); ok && cs != "" {
			candSourcePtr = &cs
		}
		var constrMacroPtr *string
		if cm, ok := agent.Payload["constrained_macro"].(string); ok && cm != "" {
			constrMacroPtr = &cm
		}
		var candCodes []string
		if cc, ok := agent.Payload["candidate_codes"].([]string); ok {
			candCodes = cc
		}

		return BookCategorizeOutcome{
			BookItemID:       item.BookItemID,
			Status:           "CLASSIFIED",
			AccountCode:      &accountCode,
			Confidence:       &conf,
			Rationale:        &rationale,
			EvidenceRefs:     evidenceRefs,
			AseNodeID:        &nodeID,
			TerminalProperty: &termProp,
			CandidateSource:  candSourcePtr,
			ConstrainedMacro: constrMacroPtr,
			CandidateCodes:   candCodes,
		}
	}

	// Terminal HOLD outcome
	holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
	if hr := agent.GetHoldReason(); hr != "" {
		holdReason = hr
	}
	rationale := "Transaction held for operator review by Go ASE DAG"
	nodeID := "hold_account_ambiguity"

	var candSourcePtr *string
	if cs, ok := agent.Payload["candidate_source"].(string); ok && cs != "" {
		candSourcePtr = &cs
	}
	var constrMacroPtr *string
	if cm, ok := agent.Payload["constrained_macro"].(string); ok && cm != "" {
		constrMacroPtr = &cm
	}
	var candCodes []string
	if cc, ok := agent.Payload["candidate_codes"].([]string); ok {
		candCodes = cc
	}

	return BookCategorizeOutcome{
		BookItemID:       item.BookItemID,
		Status:           "HOLD",
		HoldReason:       &holdReason,
		Rationale:        &rationale,
		RequiredEvidence: []string{"invoice or contract"},
		EvidenceRefs:     evidenceRefs,
		AseNodeID:        &nodeID,
		CandidateSource:  candSourcePtr,
		ConstrainedMacro: constrMacroPtr,
		CandidateCodes:   candCodes,
	}
}

// compileDAG compiles the real Go ASE DAG configuration from YAML and wires ThinkFunc handlers via domain_tools.
func (w *BookkeepingAseBookCategorizerWorker) compileDAG(companyID string) (*ase.DAG, error) {
	cfg, domainToolName, err := loadBookCategorizationConfig()
	if err != nil {
		return nil, err
	}

	dag := ase.BuildDAGFromConfig(cfg, w.logger)

	var classifier ase.Classifier
	if w.testClassifier != nil {
		classifier = w.testClassifier
	} else {
		if domainToolName == "" {
			domainToolName = "pcm_cash_accounting"
		}
		dt := domain_tools.Get(domainToolName)
		if dt == nil {
			return nil, fmt.Errorf("domain tool %q not found in registry", domainToolName)
		}
		toolDeps := domain_tools.ToolDependencies{
			DB:      w.db,
			DBPool:  w.dbPool,
			Logger:  w.logger,
			NC:      w.nc,
			Runtime: w.rt,
		}
		classifier = dt.GetClassifier(toolDeps)
		if classifier == nil {
			return nil, fmt.Errorf("domain tool %q returned nil classifier", domainToolName)
		}
		if w.explicitTestCatalog != nil {
			if pcmClf, ok := classifier.(*pcm_cash.PcmClassifier); ok {
				pcmClf.SetExplicitTestCatalog(w.explicitTestCatalog)
			}
		}
	}

	// 1. Entry node: book_direction_classifier
	entryNode := dag.GetNode("book_direction_classifier")
	if entryNode == nil {
		return nil, fmt.Errorf("entry node book_direction_classifier not found in compiled DAG")
	}
	entryNode.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc("direction"))

	// 2. Outflow macro classifier
	outflowNode := dag.GetNode("book_macro_classifier_outflow")
	if outflowNode != nil {
		outflowNode.SetThinkFunc(classifier.BuildGenericThinkFunc("book_macro_classifier_outflow"))
	}

	// 3. Inflow macro classifier
	inflowNode := dag.GetNode("book_macro_classifier_inflow")
	if inflowNode != nil {
		inflowNode.SetThinkFunc(classifier.BuildGenericThinkFunc("book_macro_classifier_inflow"))
	}

	// 4. Terminal classifier: pcge_account_resolver
	pcgeNode := dag.GetNode("pcge_account_resolver")
	if pcgeNode != nil {
		pcgeNode.SetThinkFunc(classifier.BuildDynamicThinkFunc("pcge"))
	}

	// 5. Terminal hold node: hold_account_ambiguity
	holdNode := dag.GetNode("hold_account_ambiguity")
	if holdNode != nil {
		holdNode.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
			results := make(map[string]ase.NodeClassification, len(batch))
			for _, node := range batch {
				node.Mu.Lock()
				node.HoldReason = "HOLD_INSUFFICIENT_EVIDENCE"
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["hold_reason"] = "HOLD_INSUFFICIENT_EVIDENCE"
				node.Mu.Unlock()

				results[node.NodeID] = ase.NodeClassification{
					Property: "hold_reason",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "HOLD_INSUFFICIENT_EVIDENCE",
							Confidence: 1.0,
							Reasoning:  "Insufficient evidence for automated booking",
						},
					},
				}
			}
			return results, nil
		})
	}

	dag.StartAll()
	return dag, nil
}

func loadBookCategorizationConfig() (ase.DAGConfig, string, error) {
	var yamlBytes []byte
	var err error

	candidatePaths := []string{
		"internal/erp/ase/dags/bookkeeping_account_categorization_v1.yml",
		"go/internal/erp/ase/dags/bookkeeping_account_categorization_v1.yml",
		"../erp/ase/dags/bookkeeping_account_categorization_v1.yml",
		"../../go/internal/erp/ase/dags/bookkeeping_account_categorization_v1.yml",
	}

	for _, p := range candidatePaths {
		if b, readErr := os.ReadFile(p); readErr == nil && len(b) > 0 {
			yamlBytes = b
			break
		}
	}

	if len(yamlBytes) == 0 {
		yamlBytes = []byte(embeddedBookCategorizationYAML)
	}

	var raw map[string]interface{}
	if err = yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return ase.DAGConfig{}, "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	domainTool := "pcm_cash_accounting"
	if hp, ok := raw["hyper_parameters"].(map[string]interface{}); ok {
		if dt, ok := hp["domain_tool"].(string); ok && dt != "" {
			domainTool = dt
		}
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		return ase.DAGConfig{}, "", fmt.Errorf("missing dag key in YAML")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		return ase.DAGConfig{}, "", fmt.Errorf("failed to marshal dag structure: %w", err)
	}

	var cfg ase.DAGConfig
	if err = json.Unmarshal(j, &cfg); err != nil {
		return ase.DAGConfig{}, "", fmt.Errorf("failed to unmarshal into DAGConfig: %w", err)
	}

	// Set batch size to 1 for immediate processing
	for k, v := range cfg.Nodes {
		v.BatchSize = 1
		cfg.Nodes[k] = v
	}

	return cfg, domainTool, nil
}

func (w *BookkeepingAseBookCategorizerWorker) replyError(
	msg *nats.Msg,
	code string,
	message string,
	req BookCategorizeRequest,
) error {
	resp := BookCategorizeResponse{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      req.RequestID,
		IdempotencyKey: req.IdempotencyKey,
		SessionID:      req.SessionID,
		StateRevision:  req.StateRevision,
		DagID:          req.DagID,
		DagRunID:       "error-run",
		Status:         "FAILED",
		Outcomes:       []BookCategorizeOutcome{},
		ProviderIssues: []ProviderIssue{
			{
				Code:    code,
				Message: message,
			},
		},
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	if msg.Reply != "" {
		return w.respond(msg, respBytes)
	}
	return nil
}

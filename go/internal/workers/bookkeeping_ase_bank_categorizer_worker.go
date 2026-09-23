package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
)

// BankCategorizationExecutor defines the internal boundary for domain-specific semantic categorization logic.
type BankCategorizationExecutor interface {
	Execute(ctx context.Context, req BankCategorizeRequest) (BankCategorizeResponse, error)
}

// BookkeepingAseBankCategorizerWorker implements the Go ASE boundary for residual bank categorization.
// Actual semantic worker logic is deferred.
type BookkeepingAseBankCategorizerWorker struct {
	logger        *slog.Logger
	cfg           *config.Config
	nc            *nats.Conn
	js            nats.JetStreamContext
	kv            nats.KeyValue
	store         IdempotencyStore
	instanceID    string
	executor      BankCategorizationExecutor
	
	bucketName    string
	ttl           time.Duration
	leaseDuration time.Duration
	pollAttempts  int
	pollInterval  time.Duration
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewBookkeepingAseBankCategorizerWorkerWithDeps(deps), nil
	})
}

// NewBookkeepingAseBankCategorizerWorkerWithDeps constructs the worker with shared dependencies.
func NewBookkeepingAseBankCategorizerWorkerWithDeps(deps Dependencies) *BookkeepingAseBankCategorizerWorker {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	w := NewBookkeepingAseBankCategorizerWorker(
		logger.With("worker", "bookkeeping_ase_bank_categorizer"),
		deps.Config,
		deps.Queue,
	)

	var dbQueries *database.Queries
	if deps.Store != nil {
		dbQueries = deps.Store.Queries
	} else if deps.DBPool != nil {
		dbQueries = database.New(deps.DBPool)
	}

	toolDeps := domain_tools.ToolDependencies{
		DB:      dbQueries,
		DBPool:  deps.DBPool,
		Logger:  w.logger,
		NC:      deps.Queue,
		Runtime: deps.Runtime,
		Redis:   deps.Redis,
	}

	if exec, err := NewBankCategorizationASEExecutorFromDomainTool(w.logger, "pcm_cash_accounting", toolDeps); err == nil {
		w.SetExecutor(exec)
	} else {
		w.logger.Warn("failed to initialize default domain tool executor for bank categorizer", "error", err)
	}

	return w
}

// NewBookkeepingAseBankCategorizerWorker constructs a worker boundary instance.
func NewBookkeepingAseBankCategorizerWorker(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
) *BookkeepingAseBankCategorizerWorker {
	if logger == nil {
		logger = slog.Default()
	}
	
	bucket, ttl := loadIdempotencyConfig(cfg)
	
	return &BookkeepingAseBankCategorizerWorker{
		logger:        logger,
		cfg:           cfg,
		nc:            nc,
		instanceID:    uuid.New().String(),
		bucketName:    bucket,
		ttl:           ttl,
		leaseDuration: DefaultClaimLeaseDuration,
		pollAttempts:  60,
		pollInterval:  500 * time.Millisecond,
	}
}

// SetExecutor injects the domain semantic executor. 
func (w *BookkeepingAseBankCategorizerWorker) SetExecutor(e BankCategorizationExecutor) {
	w.executor = e
}

// Executor returns the configured domain semantic executor.
func (w *BookkeepingAseBankCategorizerWorker) Executor() BankCategorizationExecutor {
	return w.executor
}

// SetIdempotencyStoreForTesting allows injecting an in-memory store.
func (w *BookkeepingAseBankCategorizerWorker) SetIdempotencyStoreForTesting(store IdempotencyStore) {
	w.store = store
}

// SetLeaseDurationForTesting allows tweaking the lease duration.
func (w *BookkeepingAseBankCategorizerWorker) SetLeaseDurationForTesting(d time.Duration) {
	w.leaseDuration = d
}

// SetPollParamsForTesting allows tweaking the claim poll parameters.
func (w *BookkeepingAseBankCategorizerWorker) SetPollParamsForTesting(attempts int, interval time.Duration) {
	w.pollAttempts = attempts
	w.pollInterval = interval
}

// IsReady checks if the idempotency backend is available.
func (w *BookkeepingAseBankCategorizerWorker) IsReady() bool {
	if err := validateLeaseConfig(w.leaseDuration); err != nil {
		return false
	}
	return w.store != nil && w.store.IsHealthy(context.Background())
}

// Init initializes the worker context (e.g. JetStream binding, idempotency setup).
func (w *BookkeepingAseBankCategorizerWorker) Init(ctx context.Context) error {
	if w.nc != nil && w.store == nil {
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

// Subscriptions returns the NATS subject bindings for this worker boundary.
func (w *BookkeepingAseBankCategorizerWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: BankCategorizeSubject,
			Group:   BankCategorizeQueueGroup,
			Options: []nats.SubOpt{
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// replyError sends a structured ProviderIssue response to the caller.
func (w *BookkeepingAseBankCategorizerWorker) replyError(msg *nats.Msg, code, desc string, req BankCategorizeRequest) error {
	resp := BankCategorizeResponse{
		SchemaVersion:  BankCategorizeSchemaVersion,
		RequestID:      req.RequestID,
		IdempotencyKey: req.IdempotencyKey,
		SessionID:      req.SessionID,
		StateRevision:  req.StateRevision,
		DagID:          req.DagID,
		Status:         "ERROR",
		Outcomes:       []BankCategorizeOutcome{},
		ProviderIssues: []ProviderIssue{
			{
				Code:    code,
				Message: desc,
			},
		},
	}
	b, _ := json.Marshal(resp)
	if msg.Reply != "" {
		_ = w.respond(msg, b)
	}
	return nil
}

func (w *BookkeepingAseBankCategorizerWorker) respond(msg *nats.Msg, data []byte) error {
	if w.nc != nil && msg.Reply != "" {
		return w.nc.Publish(msg.Reply, data)
	}
	return nil
}

// Handle executes the worker logic per message.
func (w *BookkeepingAseBankCategorizerWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("Received bank categorization request", "subject", msg.Subject, "size", len(msg.Data))
	
	if msg == nil {
		return nil
	}

	var req BankCategorizeRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		w.logger.Error("failed to unmarshal bank categorize request envelope", "error", err)
		return w.replyError(msg, "INVALID_REQUEST", fmt.Sprintf("Malformed JSON request: %v", err), req)
	}

	// 1. Validate Schema
	if req.SchemaVersion != BankCategorizeSchemaVersion {
		w.logger.Warn("unsupported schema version", "version", req.SchemaVersion)
		return w.replyError(msg, "UNSUPPORTED_SCHEMA_VERSION", fmt.Sprintf("Expected %s, got %s", BankCategorizeSchemaVersion, req.SchemaVersion), req)
	}
	
	// 2. Validate DAG ID
	if req.DagID != BankCategorizeDagID {
		w.logger.Warn("unknown or disallowed DAG ID", "dag_id", req.DagID)
		return w.replyError(msg, "UNKNOWN_DAG", fmt.Sprintf("Expected %s, got %s", BankCategorizeDagID, req.DagID), req)
	}

	// 3. Validate Envelope and constraints
	if err := req.Validate(); err != nil {
		w.logger.Warn("request validation failed", "error", err)
		return w.replyError(msg, "INVALID_REQUEST", fmt.Sprintf("Validation failed: %v", err), req)
	}

	// 4. Fail-closed on missing executor
	if w.executor == nil {
		w.logger.Error("executor is nil - failing closed")
		return w.replyError(msg, "EXECUTOR_UNAVAILABLE", "Bank categorization executor is not configured", req)
	}

	// 5. Fail-closed on store readiness
	if !w.IsReady() {
		w.logger.Error("distributed idempotency backend unavailable - failing closed")
		return w.replyError(msg, "IDEMPOTENCY_BACKEND_UNAVAILABLE", "Distributed idempotency backend is not available or initialized", req)
	}

	companyID := req.CompanyID
	if companyID == "" {
		companyID = "company-default"
	}
	kvKey := deriveKVKey(companyID, req.IdempotencyKey)
	
	payloadDigest, err := ComputeBankRequestPayloadDigest(req)
	if err != nil {
		w.logger.Error("failed to compute canonical payload digest", "error", err)
		return w.replyError(msg, "INTERNAL_ERROR", "Failed to compute payload digest", req)
	}

	var ownedClaimRevision uint64
	var claimCreatedAt time.Time
	claimed := false

	for attempt := 0; attempt < w.pollAttempts; attempt++ {
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
				ownedClaimRevision = createdRev
				claimed = true
				break
			}
			if errors.Is(createErr, ErrIdempotencyKeyExists) {
				continue
			}
			w.logger.Error("failed to create idempotency claim", "error", createErr)
			return w.replyError(msg, "IDEMPOTENCY_STORE_ERROR", fmt.Sprintf("Failed to create claim: %v", createErr), req)
		} else if err != nil {
			w.logger.Error("failed to get idempotency record", "error", err)
			return w.replyError(msg, "IDEMPOTENCY_STORE_ERROR", fmt.Sprintf("Failed to get record: %v", err), req)
		}

		if rec.RequestPayloadDigest != payloadDigest {
			w.logger.Warn("idempotency key reused with different payload digest",
				"key", req.IdempotencyKey,
				"expected", rec.RequestPayloadDigest,
				"actual", payloadDigest,
			)
			return w.replyError(msg, "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH",
				fmt.Sprintf("Idempotency key %s reused with different request payload digest", req.IdempotencyKey), req)
		}

		if rec.Status == IdempotencyStatusCompleted {
			if msg.Reply != "" {
				_ = w.respond(msg, rec.NormalizedResponseBytes)
			}
			return nil
		}

		now := time.Now()
		if now.Before(rec.LeaseExpiresAt) {
			time.Sleep(w.pollInterval)
			continue
		}

		takeoverRec := *rec
		takeoverRec.OwnerID = w.instanceID
		takeoverRec.LeaseExpiresAt = time.Now().Add(w.leaseDuration)
		takeoverRec.UpdatedAt = time.Now()
		takeoverRec.Status = IdempotencyStatusInProgress

		newRev, updateErr := w.store.Update(ctx, kvKey, &takeoverRec, rev)
		if updateErr == nil {
			ownedClaimRevision = newRev
			claimCreatedAt = rec.CreatedAt
			claimed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !claimed {
		w.logger.Warn("failed to acquire lease for idempotency key after polling", "key", req.IdempotencyKey)
		return w.replyError(msg, "CONCURRENCY_CONFLICT", "Request is being processed by another worker and did not complete in time", req)
	}

	// Heartbeat goroutine
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	heartbeatDone := make(chan struct{})

	go func(rev uint64) {
		defer close(heartbeatDone)
		ticker := time.NewTicker(w.leaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				rec, currentRev, err := w.store.Get(context.Background(), kvKey)
				if err != nil || currentRev != rev || rec.OwnerID != w.instanceID {
					w.logger.Error("lost ownership during heartbeat", "key", kvKey)
					return
				}
				rec.LeaseExpiresAt = time.Now().Add(w.leaseDuration)
				rec.UpdatedAt = time.Now()
				newRev, err := w.store.Update(context.Background(), kvKey, rec, currentRev)
				if err == nil {
					rev = newRev
				}
			}
		}
	}(ownedClaimRevision)

	// Call the domain executor
	resp, execErr := w.executor.Execute(ctx, req)
	
	// Ensure heartbeat is stopped before updating store
	cancelHeartbeat()
	<-heartbeatDone

	// Double check ownership before caching completion
	finalRec, finalRev, checkErr := w.store.Get(ctx, kvKey)
	if checkErr != nil || finalRec.OwnerID != w.instanceID {
		w.logger.Error("lost ownership of idempotency key, aborting completion cache", "key", req.IdempotencyKey)
		return fmt.Errorf("lost ownership before completion")
	}

	if execErr != nil {
		w.logger.Error("executor failed", "error", execErr)
		w.replyError(msg, "EXECUTOR_ERROR", fmt.Sprintf("Operational execution failure: %v", execErr), req)
		return nil // NATS msg acked by replyError
	}

	// Validate the returned response against the contract rules
	if resp.SchemaVersion != BankCategorizeSchemaVersion {
		w.replyError(msg, "INVALID_RESPONSE", "Executor returned invalid schema version", req)
		return nil
	}
	if resp.RequestID != req.RequestID {
		w.replyError(msg, "INVALID_RESPONSE", "Executor returned mismatched request_id", req)
		return nil
	}
	if err := resp.Validate(); err != nil {
		w.replyError(msg, "INVALID_RESPONSE", fmt.Sprintf("Executor returned malformed outcomes: %v", err), req)
		return nil
	}
	
	// Check exactly one outcome per requested item
	if len(resp.Outcomes) != len(req.BankItems) {
		w.replyError(msg, "INVALID_RESPONSE", "Executor returned mismatched outcomes length", req)
		return nil
	}
	
	requestedSet := make(map[string]struct{})
	for _, it := range req.BankItems {
		requestedSet[it.BankItemID] = struct{}{}
	}
	
	for _, o := range resp.Outcomes {
		if _, exists := requestedSet[o.BankItemID]; !exists {
			w.replyError(msg, "INVALID_RESPONSE", fmt.Sprintf("Executor returned unknown bank_item_id: %s", o.BankItemID), req)
			return nil
		}
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		w.logger.Error("failed to marshal successful response", "error", err)
		return w.replyError(msg, "INTERNAL_ERROR", "Failed to marshal response", req)
	}

	// Cache successful semantic result
	finalRec.Status = IdempotencyStatusCompleted
	finalRec.NormalizedResponseBytes = respBytes
	finalRec.UpdatedAt = time.Now()
	_, updateErr := w.store.Update(ctx, kvKey, finalRec, finalRev)
	if updateErr != nil {
		w.logger.Error("failed to mark idempotency claim completed", "error", updateErr)
		return w.replyError(msg, "IDEMPOTENCY_STORE_ERROR", "Failed to cache completion", req)
	}

	if msg.Reply != "" {
		_ = w.respond(msg, respBytes)
	}

	return nil
}

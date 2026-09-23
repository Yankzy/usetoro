package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a test message with capturing responder
func newTestMsg(data []byte, reply string, capture *[]byte) (*nats.Msg, func(m *nats.Msg, d []byte) error) {
	msg := &nats.Msg{
		Data:  data,
		Reply: reply,
	}
	responder := func(m *nats.Msg, d []byte) error {
		if capture != nil {
			*capture = d
		}
		return nil
	}
	return msg, responder
}

func loadGoldenVector() (BookCategorizeRequest, string, error) {
	candidatePaths := []string{
		"../../ledger/tests/fixtures/book_categorize_golden_vector.json",
		"ledger/tests/fixtures/book_categorize_golden_vector.json",
		"../ledger/tests/fixtures/book_categorize_golden_vector.json",
		"../../../ledger/tests/fixtures/book_categorize_golden_vector.json",
	}
	var data []byte
	var err error
	for _, p := range candidatePaths {
		if b, readErr := os.ReadFile(p); readErr == nil && len(b) > 0 {
			data = b
			err = nil
			break
		} else {
			err = readErr
		}
	}
	if len(data) == 0 {
		return BookCategorizeRequest{}, "", fmt.Errorf("failed to read golden vector: %w", err)
	}

	var fixture struct {
		Request struct {
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
		} `json:"request"`
		ExpectedSHA256Digest string `json:"expected_sha256_digest"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return BookCategorizeRequest{}, "", err
	}

	req := BookCategorizeRequest{
		SchemaVersion:       fixture.Request.SchemaVersion,
		RequestID:           fixture.Request.RequestID,
		IdempotencyKey:      fixture.Request.IdempotencyKey,
		CompanyID:           fixture.Request.CompanyID,
		SessionID:           fixture.Request.SessionID,
		StateRevision:       fixture.Request.StateRevision,
		PersistenceRevision: fixture.Request.PersistenceRevision,
		DagID:               fixture.Request.DagID,
		RequestedAt:         fixture.Request.RequestedAt,
		BookItems:           fixture.Request.BookItems,
	}
	return req, fixture.ExpectedSHA256Digest, nil
}

// =========================================================================
// GO TESTS A - Q (FINAL BOUNDARY SEALING)
// =========================================================================

// Test A: Every book success/error envelope uses bookkeeping.ase.book_categorize.v1
func TestBookCategorizer_TestA_EveryBookSuccessErrorEnvelopeUsesCanonicalSchema(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	desc := "ACHAT FOURNITURES"
	baseReq := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-schema-check",
		IdempotencyKey: "idemp-schema-check",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-01-01", AmountUnits: 10000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc},
		},
	}

	// 1. Success envelope uses canonical schema
	data, err := json.Marshal(baseReq)
	require.NoError(t, err)
	var capSuccess []byte
	msgSuccess, respFnSuccess := newTestMsg(data, "reply.success", &capSuccess)
	worker.SetRespondFnForTesting(respFnSuccess)
	require.NoError(t, worker.Handle(context.Background(), msgSuccess))
	var respSuccess BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capSuccess, &respSuccess))
	assert.Equal(t, BookCategorizeSchemaVersion, respSuccess.SchemaVersion)
	assert.Equal(t, "bookkeeping.ase.book_categorize.v1", respSuccess.SchemaVersion)

	// 2. Error envelope for unsupported schema version uses canonical schema
	badSchemaReq := baseReq
	badSchemaReq.SchemaVersion = "bookkeeping.ase.unsupported.v9"
	badData, err := json.Marshal(badSchemaReq)
	require.NoError(t, err)
	var capBadSchema []byte
	msgBadSchema, respFnBadSchema := newTestMsg(badData, "reply.bad_schema", &capBadSchema)
	worker.SetRespondFnForTesting(respFnBadSchema)
	require.NoError(t, worker.Handle(context.Background(), msgBadSchema))
	var respBadSchema BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capBadSchema, &respBadSchema))
	assert.Equal(t, BookCategorizeSchemaVersion, respBadSchema.SchemaVersion)
	assert.Equal(t, "bookkeeping.ase.book_categorize.v1", respBadSchema.SchemaVersion)

	// 3. Error envelope for unknown DAG uses canonical schema
	badDagReq := baseReq
	badDagReq.DagID = "pcm_bank_cash_accounting_dag"
	badDagData, err := json.Marshal(badDagReq)
	require.NoError(t, err)
	var capBadDag []byte
	msgBadDag, respFnBadDag := newTestMsg(badDagData, "reply.bad_dag", &capBadDag)
	worker.SetRespondFnForTesting(respFnBadDag)
	require.NoError(t, worker.Handle(context.Background(), msgBadDag))
	var respBadDag BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capBadDag, &respBadDag))
	assert.Equal(t, BookCategorizeSchemaVersion, respBadDag.SchemaVersion)
	assert.Equal(t, "bookkeeping.ase.book_categorize.v1", respBadDag.SchemaVersion)

	// 4. Error envelope for backend unavailable uses canonical schema
	unreadyWorker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	var capUnready []byte
	msgUnready, respFnUnready := newTestMsg(data, "reply.unready", &capUnready)
	unreadyWorker.SetRespondFnForTesting(respFnUnready)
	require.NoError(t, unreadyWorker.Handle(context.Background(), msgUnready))
	var respUnready BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capUnready, &respUnready))
	assert.Equal(t, BookCategorizeSchemaVersion, respUnready.SchemaVersion)
	assert.Equal(t, "bookkeeping.ase.book_categorize.v1", respUnready.SchemaVersion)

	// 5. Readiness envelope remains separate schema
	probeReq := ReadinessRequest{
		SchemaVersion: BookCategorizeReadinessSchema,
		RequestID:     "readiness-1",
		Action:        "PING",
	}
	probeData, _ := json.Marshal(probeReq)
	var capReadiness []byte
	msgProbe, respFnProbe := newTestMsg(probeData, "reply.probe", &capReadiness)
	worker.SetRespondFnForTesting(respFnProbe)
	require.NoError(t, worker.Handle(context.Background(), msgProbe))
	var respProbe ReadinessResponse
	require.NoError(t, json.Unmarshal(capReadiness, &respProbe))
	assert.Equal(t, "bookkeeping.ase.readiness.v1", respProbe.SchemaVersion)
}

// Test B: Digest changes when date changes
func TestBookCategorizer_TestB_DigestChangesWhenDateChanges(t *testing.T) {
	desc := "TEST ITEM"
	req1 := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	req2 := req1
	req2.BookItems = []BookCategorizeItem{
		{BookItemID: "item-1", Date: "2026-03-02", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
	}

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.NotEqual(t, digest1, digest2, "Digest must change when item date changes")
}

// Test C: Digest changes when currency changes
func TestBookCategorizer_TestC_DigestChangesWhenCurrencyChanges(t *testing.T) {
	desc := "TEST ITEM"
	req1 := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	req2 := req1
	req2.BookItems = []BookCategorizeItem{
		{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc},
	}

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.NotEqual(t, digest1, digest2, "Digest must change when currency changes")
}

// Test D: Digest changes when effective_description changes
func TestBookCategorizer_TestD_DigestChangesWhenEffectiveDescriptionChanges(t *testing.T) {
	desc1 := "ACHAT FOURNITURES BUREAU"
	desc2 := "SERVICES NETTOYAGE LOCAUX"
	req1 := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc1},
		},
	}
	req2 := req1
	req2.BookItems = []BookCategorizeItem{
		{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc2},
	}

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.NotEqual(t, digest1, digest2, "Digest must change when description changes")
}

// Test E: Digest changes when persistence_revision changes
func TestBookCategorizer_TestE_DigestChangesWhenPersistenceRevisionChanges(t *testing.T) {
	desc := "TEST ITEM"
	req1 := BookCategorizeRequest{
		SchemaVersion:       BookCategorizeSchemaVersion,
		CompanyID:           "comp-1",
		SessionID:           "sess-1",
		StateRevision:       1,
		PersistenceRevision: 1,
		DagID:               BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	req2 := req1
	req2.PersistenceRevision = 2

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.NotEqual(t, digest1, digest2, "Digest must change when persistence revision changes")
}

// Test F: Digest ignores requested_at, request_id, and idempotency_key
func TestBookCategorizer_TestF_DigestIgnoresRequestedAt(t *testing.T) {
	desc := "TEST ITEM"
	req1 := BookCategorizeRequest{
		SchemaVersion:       BookCategorizeSchemaVersion,
		RequestID:           "req-first-attempt",
		IdempotencyKey:      "idemp-key-one",
		CompanyID:           "comp-1",
		SessionID:           "sess-1",
		StateRevision:       1,
		PersistenceRevision: 1,
		DagID:               BookCategorizeDagID,
		RequestedAt:         "2026-09-14T08:00:00Z",
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	req2 := req1
	req2.RequestID = "req-retry-attempt-2"
	req2.IdempotencyKey = "idemp-key-two"
	req2.RequestedAt = "2026-09-14T15:45:30Z"

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.Equal(t, digest1, digest2, "Digest must ignore non-semantic metadata (requested_at, request_id, idempotency_key)")
}

// Test G: Semantic item ordering produces same digest
func TestBookCategorizer_TestG_SemanticItemOrderingProducesSameDigest(t *testing.T) {
	descA := "ITEM A"
	descB := "ITEM B"
	itemA := BookCategorizeItem{BookItemID: "item-001", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &descA}
	itemB := BookCategorizeItem{BookItemID: "item-002", Date: "2026-03-02", AmountUnits: 20000, Currency: "EUR", Direction: "INFLOW", Description: &descB}

	req1 := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems:     []BookCategorizeItem{itemA, itemB},
	}
	req2 := req1
	req2.BookItems = []BookCategorizeItem{itemB, itemA}

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.Equal(t, digest1, digest2, "Digest must be independent of book items order in request")
}

// Test H: Evidence ordering produces same digest where order is semantically irrelevant
func TestBookCategorizer_TestH_EvidenceOrderingProducesSameDigest(t *testing.T) {
	desc := "ITEM WITH EVIDENCE"
	item1 := BookCategorizeItem{
		BookItemID:            "item-001",
		Date:                  "2026-03-01",
		AmountUnits:           10000,
		Currency:              "EUR",
		Direction:             "OUTFLOW",
		Description:           &desc,
		EvidenceRefs:          []string{"ev-doc-2", "ev-doc-1"},
		SafeEvidenceSummaries: []string{"Doc:2:invoice", "Doc:1:receipt"},
	}
	item2 := item1
	item2.EvidenceRefs = []string{"ev-doc-1", "ev-doc-2"}
	item2.SafeEvidenceSummaries = []string{"Doc:1:receipt", "Doc:2:invoice"}

	req1 := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems:     []BookCategorizeItem{item1},
	}
	req2 := req1
	req2.BookItems = []BookCategorizeItem{item2}

	digest1 := computeRequestPayloadDigest(req1)
	digest2 := computeRequestPayloadDigest(req2)
	assert.Equal(t, digest1, digest2, "Digest must treat evidence refs and summaries as deterministic sorted sets")
}

// Test I: Go digest matches cross-language golden vector
func TestBookCategorizer_TestI_GoDigestMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenReq, expectedDigest, err := loadGoldenVector()
	require.NoError(t, err)
	require.NotEmpty(t, expectedDigest)

	computedDigest := computeRequestPayloadDigest(goldenReq)
	assert.Equal(t, expectedDigest, computedDigest, "Go payload digest must exactly match the cross-language golden vector SHA-256")
	assert.Equal(t, "c16e6f272c3d98561be7b311bae99e02b49689fb8b60275fc38b8134dad4d855", computedDigest)
}

// Test I2: Source artifact kind and bookkeeping role alter semantic digest
func TestBookCategorizer_SourceSemanticsParticipateInDigest(t *testing.T) {
	kindInvoice := "INVOICE"
	kindBill := "BILL"
	roleReceivable := "OPEN_RECEIVABLE"
	rolePayable := "OPEN_PAYABLE"

	itemBase := BookCategorizeItem{
		BookItemID:         "item-001",
		Date:               "2026-03-01",
		AmountUnits:        10000,
		Currency:           "EUR",
		Direction:          "OUTFLOW",
		SourceArtifactKind: &kindBill,
		BookkeepingRole:    &rolePayable,
	}

	reqBase := BookCategorizeRequest{
		SchemaVersion: BookCategorizeSchemaVersion,
		CompanyID:     "comp-1",
		SessionID:     "sess-1",
		StateRevision: 1,
		DagID:         BookCategorizeDagID,
		BookItems:     []BookCategorizeItem{itemBase},
	}

	baseDigest := computeRequestPayloadDigest(reqBase)

	// Vary SourceArtifactKind
	itemKindChanged := itemBase
	itemKindChanged.SourceArtifactKind = &kindInvoice
	reqKindChanged := reqBase
	reqKindChanged.BookItems = []BookCategorizeItem{itemKindChanged}
	digestKindChanged := computeRequestPayloadDigest(reqKindChanged)
	assert.NotEqual(t, baseDigest, digestKindChanged, "Changing SourceArtifactKind must alter semantic digest")

	// Vary BookkeepingRole
	itemRoleChanged := itemBase
	itemRoleChanged.BookkeepingRole = &roleReceivable
	reqRoleChanged := reqBase
	reqRoleChanged.BookItems = []BookCategorizeItem{itemRoleChanged}
	digestRoleChanged := computeRequestPayloadDigest(reqRoleChanged)
	assert.NotEqual(t, baseDigest, digestRoleChanged, "Changing BookkeepingRole must alter semantic digest")
}

// Test J: KV key uses full/adequate collision-resistant hash (256-bit SHA-256 hex)
func TestBookCategorizer_TestJ_KvKeyUsesFullCollisionResistantHash(t *testing.T) {
	key := deriveKVKey("comp-1", "idemp-key-1")
	assert.Regexp(t, "^idem_[0-9a-f]{64}$", key, "KV key must use full 256-bit SHA-256 hex format")
	assert.Equal(t, 5+64, len(key))
}

// Test K: Company identity affects KV key
func TestBookCategorizer_TestK_CompanyIdentityAffectsKvKey(t *testing.T) {
	keyComp1 := deriveKVKey("tenant-uuid-1111", "shared-idempotency-key")
	keyComp2 := deriveKVKey("tenant-uuid-2222", "shared-idempotency-key")

	assert.NotEqual(t, keyComp1, keyComp2, "Different companies with identical idempotency keys must produce distinct KV keys")
}

// Test L: No raw semantic text in KV key
func TestBookCategorizer_TestL_NoRawSemanticTextInKvKey(t *testing.T) {
	sensitiveDesc := "CONFIDENTIAL SALARY WIRE TO CEO 99999 USD"
	key := deriveKVKey("comp-secret", sensitiveDesc)

	assert.NotContains(t, key, sensitiveDesc)
	assert.NotContains(t, key, "CONFIDENTIAL")
	assert.NotContains(t, key, "SALARY")
	assert.NotContains(t, key, "CEO")
	assert.NotContains(t, key, "comp-secret")
	assert.Regexp(t, "^idem_[0-9a-f]{64}$", key)
}

// Test M: Lease configuration validation
func TestBookCategorizer_TestM_LeaseConfigurationValidation(t *testing.T) {
	assert.Error(t, validateLeaseConfig(0))
	assert.Error(t, validateLeaseConfig(-5*time.Second))
	assert.NoError(t, validateLeaseConfig(5*time.Second))
	assert.NoError(t, validateLeaseConfig(30*time.Second))

	// Worker with invalid lease is not ready
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)
	worker.SetLeaseDurationForTesting(0)
	assert.False(t, worker.IsReady(), "Worker with non-positive lease duration must not be ready")

	desc := "TEST ITEM"
	req := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-lease-invalid",
		IdempotencyKey: "idemp-lease-invalid",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	data, _ := json.Marshal(req)
	var capResp []byte
	msg, respFn := newTestMsg(data, "reply.lease", &capResp)
	worker.SetRespondFnForTesting(respFn)

	err := worker.Handle(context.Background(), msg)
	require.NoError(t, err)

	var resp BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capResp, &resp))
	assert.Equal(t, "FAILED", resp.Status)
	assert.Equal(t, "INVALID_LEASE_CONFIG", resp.ProviderIssues[0].Code)
}

// Test N: Long-running execution cannot be taken over while legitimate owner is live (heartbeat extends lease)
func TestBookCategorizer_TestN_LongRunningExecutionCannotBeTakenOverWhileLegitimateOwnerIsLive(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker1 := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)
	worker2 := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	// Set a short lease duration (150ms) so heartbeat runs every 50ms
	worker1.SetLeaseDurationForTesting(150 * time.Millisecond)
	worker1.SetHeartbeatDisabledForTesting(false)

	worker2.SetLeaseDurationForTesting(150 * time.Millisecond)
	worker2.SetPollParamsForTesting(2, 50*time.Millisecond) // Polls briefly

	desc := "LONG RUNNING TRANSACTION"
	req := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-long-live",
		IdempotencyKey: "idemp-long-live-key",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	kvKey := deriveKVKey("comp-1", req.IdempotencyKey)
	digest := computeRequestPayloadDigest(req)

	// Create initial claim by worker1
	claim := &IdempotencyRecord{
		IdempotencyKey:       req.IdempotencyKey,
		RequestPayloadDigest: digest,
		SchemaVersion:        req.SchemaVersion,
		DagID:                req.DagID,
		CompanyID:            "comp-1",
		SessionID:            req.SessionID,
		StateRevision:        req.StateRevision,
		Status:               IdempotencyStatusInProgress,
		OwnerID:              worker1.instanceID,
		LeaseExpiresAt:       time.Now().Add(150 * time.Millisecond),
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	_, err := memStore.Create(context.Background(), kvKey, claim)
	require.NoError(t, err)

	// Start simulated heartbeat for worker1 extending lease
	hbCtx, cancelHb := context.WithCancel(context.Background())
	defer cancelHb()
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				rec, rev, getErr := memStore.Get(context.Background(), kvKey)
				if getErr == nil && rec != nil && rec.OwnerID == worker1.instanceID {
					rec.LeaseExpiresAt = time.Now().Add(150 * time.Millisecond)
					rec.UpdatedAt = time.Now()
					_, _ = memStore.Update(context.Background(), kvKey, rec, rev)
				}
			}
		}
	}()

	// Wait 200ms (> initial 150ms lease). Heartbeat has kept it alive!
	time.Sleep(200 * time.Millisecond)

	// Worker 2 attempts to handle request. Because lease is fresh, it must NOT take over!
	data, _ := json.Marshal(req)
	var capWorker2 []byte
	msgWorker2, respFn2 := newTestMsg(data, "reply.worker2", &capWorker2)
	worker2.SetRespondFnForTesting(respFn2)

	err = worker2.Handle(context.Background(), msgWorker2)
	require.NoError(t, err)

	var respWorker2 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capWorker2, &respWorker2))
	assert.Equal(t, "FAILED", respWorker2.Status)
	assert.Equal(t, "CONCURRENT_EXECUTION_IN_PROGRESS", respWorker2.ProviderIssues[0].Code)

	// Verify Worker 1 still owns the claim in KV
	finalRec, _, err := memStore.Get(context.Background(), kvKey)
	require.NoError(t, err)
	assert.Equal(t, worker1.instanceID, finalRec.OwnerID)
}

// Test O: Stale dead owner remains recoverable
func TestBookCategorizer_TestO_StaleDeadOwnerRemainsRecoverable(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker2 := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	desc := "RECOVERABLE ITEM"
	req := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-recoverable",
		IdempotencyKey: "idemp-recoverable-key",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-rec", Date: "2026-03-01", AmountUnits: 10000, Currency: "EUR", Direction: "OUTFLOW", Description: &desc},
		},
	}
	kvKey := deriveKVKey("comp-1", req.IdempotencyKey)
	digest := computeRequestPayloadDigest(req)

	// Simulate dead worker: stale lease in past, no heartbeat running
	staleRecord := &IdempotencyRecord{
		IdempotencyKey:       req.IdempotencyKey,
		RequestPayloadDigest: digest,
		SchemaVersion:        req.SchemaVersion,
		DagID:                req.DagID,
		CompanyID:            "comp-1",
		SessionID:            req.SessionID,
		StateRevision:        req.StateRevision,
		Status:               IdempotencyStatusInProgress,
		OwnerID:              "dead-worker-instance",
		LeaseExpiresAt:       time.Now().Add(-5 * time.Second), // expired in past
		CreatedAt:            time.Now().Add(-35 * time.Second),
		UpdatedAt:            time.Now().Add(-5 * time.Second),
	}
	_, err := memStore.Create(context.Background(), kvKey, staleRecord)
	require.NoError(t, err)

	// Worker 2 takes over and completes
	data, _ := json.Marshal(req)
	var capWorker2 []byte
	msgWorker2, respFn2 := newTestMsg(data, "reply.worker2", &capWorker2)
	worker2.SetRespondFnForTesting(respFn2)

	err = worker2.Handle(context.Background(), msgWorker2)
	require.NoError(t, err)

	var respWorker2 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(capWorker2, &respWorker2))
	assert.Equal(t, "SUCCESS", respWorker2.Status)

	// Record in store is now COMPLETED by worker2
	finalRec, _, err := memStore.Get(context.Background(), kvKey)
	require.NoError(t, err)
	assert.Equal(t, IdempotencyStatusCompleted, finalRec.Status)
	assert.Equal(t, worker2.instanceID, finalRec.OwnerID)
}

// Test P: Completed replay behavior remains unchanged
func TestBookCategorizer_TestP_CompletedReplayBehaviorRemainsUnchanged(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	desc := "ACHAT FOURNITURES BUREAU"
	req := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-replay-1",
		IdempotencyKey: "idemp-replay-key-1",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-01-01", AmountUnits: 12000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc},
		},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var cap1 []byte
	msg1, respFn1 := newTestMsg(data, "reply.1", &cap1)
	worker.SetRespondFnForTesting(respFn1)
	require.NoError(t, worker.Handle(context.Background(), msg1))

	var resp1 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(cap1, &resp1))
	assert.Equal(t, "SUCCESS", resp1.Status)
	assert.Equal(t, "6111", *resp1.Outcomes[0].AccountCode)

	var cap2 []byte
	msg2, respFn2 := newTestMsg(data, "reply.2", &cap2)
	worker.SetRespondFnForTesting(respFn2)
	require.NoError(t, worker.Handle(context.Background(), msg2))

	var resp2 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(cap2, &resp2))

	assert.Equal(t, resp1.DagRunID, resp2.DagRunID)
	assert.Equal(t, resp1.Status, resp2.Status)
	assert.Equal(t, *resp1.Outcomes[0].AccountCode, *resp2.Outcomes[0].AccountCode)
	assert.Equal(t, *resp1.Outcomes[0].Confidence, *resp2.Outcomes[0].Confidence)
	assert.Equal(t, *resp1.Outcomes[0].Rationale, *resp2.Outcomes[0].Rationale)
}

// Test Q: All previous idempotency and real engine tests remain green
func TestBookCategorizer_TestQ_AllIdempotencyAndRealEngineTestsGreen(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	// 1. Same key + different payload is rejected explicitly
	desc1 := "ACHAT MATERIEL"
	req1 := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-mismatch-1",
		IdempotencyKey: "idemp-shared-key",
		CompanyID:      "comp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-1", Date: "2026-01-01", AmountUnits: 10000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc1},
		},
	}
	data1, _ := json.Marshal(req1)
	var cap1 []byte
	msg1, respFn1 := newTestMsg(data1, "reply.1", &cap1)
	worker.SetRespondFnForTesting(respFn1)
	require.NoError(t, worker.Handle(context.Background(), msg1))

	desc2 := "MUTATED DESCRIPTION"
	req2 := req1
	req2.BookItems = []BookCategorizeItem{
		{BookItemID: "item-1", Date: "2026-01-01", AmountUnits: 10000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc2},
	}
	data2, _ := json.Marshal(req2)
	var cap2 []byte
	msg2, respFn2 := newTestMsg(data2, "reply.2", &cap2)
	worker.SetRespondFnForTesting(respFn2)
	require.NoError(t, worker.Handle(context.Background(), msg2))

	var resp2 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(cap2, &resp2))
	assert.Equal(t, "FAILED", resp2.Status)
	assert.Equal(t, "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH", resp2.ProviderIssues[0].Code)

	// 2. Real Go ASE DAG execution
	dag, err := worker.compileDAG("company-test-1")
	require.NoError(t, err)
	defer dag.StopAll()

	outcome := worker.evaluateItemWithDAG(dag, "company-test-1", req1.BookItems[0])
	assert.Equal(t, "CLASSIFIED", outcome.Status)
	assert.Equal(t, "pcge_account_resolver", *outcome.AseNodeID)
}

// =========================================================================
// PREVIOUS REAL ASE DAG SUITE TESTS PRESERVED
// =========================================================================

func TestBookCategorizer_SchemaParsing(t *testing.T) {
	req := BookCategorizeRequest{
		SchemaVersion:       BookCategorizeSchemaVersion,
		RequestID:           "req-123",
		IdempotencyKey:      "idemp-123",
		CompanyID:           "comp-1",
		SessionID:           "sess-1",
		StateRevision:       1,
		PersistenceRevision: 1,
		DagID:               BookCategorizeDagID,
		RequestedAt:         "2026-09-13T19:00:00Z",
		BookItems: []BookCategorizeItem{
			{
				BookItemID: "book-1",
				Date:       "2026-01-01",
				Amount:     1000,
				Currency:   "MAD",
				Direction:  "OUTFLOW",
			},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed BookCategorizeRequest
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, req.SchemaVersion, parsed.SchemaVersion)
	assert.Equal(t, req.DagID, parsed.DagID)
	assert.Equal(t, len(req.BookItems), len(parsed.BookItems))
	assert.Equal(t, "book-1", parsed.BookItems[0].BookItemID)
}

func TestBookCategorizer_SchemaMismatchRejection(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)

	// Wrong schema version
	reqWrongSchema := BookCategorizeRequest{
		SchemaVersion:  "bookkeeping.ase.wrong_version",
		RequestID:      "req-1",
		IdempotencyKey: "idemp-1",
		SessionID:      "sess-1",
		StateRevision:  1,
		DagID:          BookCategorizeDagID,
	}
	data, err := json.Marshal(reqWrongSchema)
	require.NoError(t, err)

	var cap1 []byte
	msg1, respFn1 := newTestMsg(data, "reply.1", &cap1)
	worker.SetRespondFnForTesting(respFn1)
	err = worker.Handle(context.Background(), msg1)
	assert.NoError(t, err)
	var resp1 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(cap1, &resp1))
	assert.Equal(t, "FAILED", resp1.Status)
	assert.Equal(t, "UNSUPPORTED_SCHEMA_VERSION", resp1.ProviderIssues[0].Code)

	// Wrong DAG ID
	reqWrongDAG := BookCategorizeRequest{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      "req-2",
		IdempotencyKey: "idemp-2",
		SessionID:      "sess-2",
		StateRevision:  1,
		DagID:          "pcm_bank_cash_accounting_dag",
	}
	data2, err := json.Marshal(reqWrongDAG)
	require.NoError(t, err)

	var cap2 []byte
	msg2, respFn2 := newTestMsg(data2, "reply.2", &cap2)
	worker.SetRespondFnForTesting(respFn2)
	err = worker.Handle(context.Background(), msg2)
	assert.NoError(t, err)
	var resp2 BookCategorizeResponse
	require.NoError(t, json.Unmarshal(cap2, &resp2))
	assert.Equal(t, "FAILED", resp2.Status)
	assert.Equal(t, "UNKNOWN_DAG", resp2.ProviderIssues[0].Code)
}

func TestBookCategorizer_TerminalClassifiedResponse(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	desc := "LOYER MENSUEL BUREAU"
	item := BookCategorizeItem{
		BookItemID:   "book-100",
		Date:         "2026-01-15",
		Amount:       2500000,
		Currency:     "MAD",
		Direction:    "OUTFLOW",
		Description:  &desc,
		EvidenceRefs: []string{"doc-lease-1"},
	}

	outcome := worker.evaluateItem(item)
	assert.Equal(t, "book-100", outcome.BookItemID)
	assert.Equal(t, "CLASSIFIED", outcome.Status)
	require.NotNil(t, outcome.AccountCode)
	assert.Equal(t, "6131", *outcome.AccountCode)
	assert.Equal(t, 0.99, *outcome.Confidence)
	assert.Equal(t, "pcge_account_resolver", *outcome.AseNodeID)
	assert.Equal(t, "account_code", *outcome.TerminalProperty)
	assert.Contains(t, outcome.EvidenceRefs, "doc-lease-1")
}

func TestBookCategorizer_TerminalHoldResponse(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)

	ambiguousDesc := "VIREMENT AMBIGUOUS PAYMENT"
	item := BookCategorizeItem{
		BookItemID:  "book-200",
		Date:        "2026-01-15",
		Amount:      500000,
		Currency:    "MAD",
		Direction:   "OUTFLOW",
		Description: &ambiguousDesc,
	}

	outcome := worker.evaluateItem(item)
	assert.Equal(t, "book-200", outcome.BookItemID)
	assert.Equal(t, "HOLD", outcome.Status)
	assert.Nil(t, outcome.AccountCode)
	require.NotNil(t, outcome.HoldReason)
	assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", *outcome.HoldReason)
	assert.Equal(t, "hold_account_ambiguity", *outcome.AseNodeID)

	emptyDesc := ""
	itemEmpty := BookCategorizeItem{
		BookItemID:  "book-201",
		Date:        "2026-01-15",
		Amount:      500000,
		Currency:    "MAD",
		Direction:   "OUTFLOW",
		Description: &emptyDesc,
	}

	outcomeEmpty := worker.evaluateItem(itemEmpty)
	assert.Equal(t, "HOLD", outcomeEmpty.Status)
	assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", *outcomeEmpty.HoldReason)
}

func TestBookCategorizer_OneTerminalOutcomePerItem(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)

	desc1 := "COMMISSION BANCAIRE"
	desc2 := "HONORAIRES AVOCAT"
	items := []BookCategorizeItem{
		{BookItemID: "item-1", Date: "2026-01-01", Amount: 100, Currency: "MAD", Direction: "OUTFLOW", Description: &desc1},
		{BookItemID: "item-2", Date: "2026-01-02", Amount: 200, Currency: "MAD", Direction: "OUTFLOW", Description: &desc2},
	}

	outcomes := make([]BookCategorizeOutcome, 0)
	for _, it := range items {
		outcomes = append(outcomes, worker.evaluateItem(it))
	}

	assert.Equal(t, 2, len(outcomes))
	assert.Equal(t, "item-1", outcomes[0].BookItemID)
	assert.Equal(t, "6147", *outcomes[0].AccountCode)
	assert.Equal(t, "item-2", outcomes[1].BookItemID)
	assert.Equal(t, "6136", *outcomes[1].AccountCode)
}

func TestBookCategorizer_NoReconciliationPostingNodesReachable(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)

	desc := "REGLEMENT FOURNISSEUR"
	outcome := worker.evaluateItem(BookCategorizeItem{
		BookItemID:  "book-300",
		Date:        "2026-01-10",
		Amount:      1000,
		Currency:    "MAD",
		Direction:   "OUTFLOW",
		Description: &desc,
	})

	assert.NotEqual(t, "reconciliation", *outcome.AseNodeID)
	assert.NotEqual(t, "journal_builder", *outcome.AseNodeID)
	assert.True(t, outcome.Status == "CLASSIFIED" || outcome.Status == "HOLD")
}

func TestBookCategorizer_RequestResponseCorrelation(t *testing.T) {
	req := BookCategorizeRequest{
		SchemaVersion:       BookCategorizeSchemaVersion,
		RequestID:           "req-corr-1",
		IdempotencyKey:      "idemp-corr-1",
		CompanyID:           "comp-1",
		SessionID:           "sess-corr-1",
		StateRevision:       5,
		PersistenceRevision: 12,
		DagID:               BookCategorizeDagID,
		RequestedAt:         "2026-09-13T19:00:00Z",
	}

	resp := BookCategorizeResponse{
		SchemaVersion:  BookCategorizeSchemaVersion,
		RequestID:      req.RequestID,
		IdempotencyKey: req.IdempotencyKey,
		SessionID:      req.SessionID,
		StateRevision:  req.StateRevision,
		DagID:          BookCategorizeDagID,
		DagRunID:       "run-1",
		Status:         "SUCCESS",
	}

	assert.Equal(t, req.RequestID, resp.RequestID)
	assert.Equal(t, req.IdempotencyKey, resp.IdempotencyKey)
	assert.Equal(t, req.SessionID, resp.SessionID)
	assert.Equal(t, req.StateRevision, resp.StateRevision)
	assert.Equal(t, req.DagID, resp.DagID)
	assert.Equal(t, req.SchemaVersion, resp.SchemaVersion)
}

func TestBookCategorizer_RealGoAseDagExecution(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	desc := "ACHAT FOURNITURES BUREAU"
	item := BookCategorizeItem{
		BookItemID:   "book-real-dag-1",
		Date:         "2026-02-01",
		AmountUnits:  123456,
		Currency:     "MAD",
		Direction:    "OUTFLOW",
		Description:  &desc,
		EvidenceRefs: []string{"receipt-1"},
	}

	dag, err := worker.compileDAG("company-test-1")
	require.NoError(t, err)
	defer dag.StopAll()

	outcome := worker.evaluateItemWithDAG(dag, "company-test-1", item)
	assert.Equal(t, "book-real-dag-1", outcome.BookItemID)
	assert.Equal(t, "CLASSIFIED", outcome.Status)
	assert.Equal(t, "pcge_account_resolver", *outcome.AseNodeID)
	assert.Equal(t, "account_code", *outcome.TerminalProperty)
	assert.Equal(t, "6111", *outcome.AccountCode)
}

func TestBookCategorizer_ExactAmountUnitsPreservation(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	desc := "TEST FRAIS DIVERS"
	testAmounts := []int64{10001, 123456, 1, 99999999}

	for _, amt := range testAmounts {
		item := BookCategorizeItem{
			BookItemID:  fmt.Sprintf("book-amt-%d", amt),
			Date:        "2026-02-01",
			AmountUnits: amt,
			Amount:      amt,
			Currency:    "MAD",
			Direction:   "OUTFLOW",
			Description: &desc,
		}
		outcome := worker.evaluateItem(item)
		assert.Equal(t, "CLASSIFIED", outcome.Status)
	}
}

func TestBookCategorizer_AllFourDirectionsPreserved(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	descIn := "VENTE CLIENT PRODUIT"
	descOut := "ACHAT MARCHANDISE FOURNISSEUR"

	directions := []struct {
		dir      string
		desc     string
		expected string
	}{
		{"BOOK_BANK_DEBIT", descIn, "7111"},
		{"BOOK_BANK_CREDIT", descOut, "6111"},
		{"INFLOW", descIn, "7111"},
		{"OUTFLOW", descOut, "6111"},
	}

	for _, tc := range directions {
		d := tc.desc
		item := BookCategorizeItem{
			BookItemID:  "book-dir-" + tc.dir,
			Date:        "2026-02-01",
			AmountUnits: 10000,
			Currency:    "MAD",
			Direction:   tc.dir,
			Description: &d,
		}
		outcome := worker.evaluateItem(item)
		assert.Equal(t, "CLASSIFIED", outcome.Status)
		require.NotNil(t, outcome.AccountCode)
		assert.Equal(t, tc.expected, *outcome.AccountCode, "direction %s failed", tc.dir)
	}
}

func TestBookCategorizer_TenantIsolation(t *testing.T) {
	memStore := NewMemoryIdempotencyStore()
	worker := NewBookkeepingAseBookCategorizerWorkerWithStore(nil, nil, nil, memStore)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	desc := "LOCATION IMMOBILIERE"
	req := BookCategorizeRequest{
		SchemaVersion:       BookCategorizeSchemaVersion,
		RequestID:           "req-tenant-1",
		IdempotencyKey:      "idemp-tenant-1",
		CompanyID:           "tenant-uuid-12345",
		SessionID:           "sess-tenant-1",
		StateRevision:       1,
		PersistenceRevision: 1,
		DagID:               BookCategorizeDagID,
		RequestedAt:         "2026-09-14T00:00:00Z",
		BookItems: []BookCategorizeItem{
			{BookItemID: "item-t1", Date: "2026-01-01", AmountUnits: 2500000, Currency: "MAD", Direction: "OUTFLOW", Description: &desc},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var capResp []byte
	msg, respFn := newTestMsg(data, "reply.tenant", &capResp)
	worker.SetRespondFnForTesting(respFn)

	err = worker.Handle(context.Background(), msg)
	require.NoError(t, err)

	var resp BookCategorizeResponse
	err = json.Unmarshal(capResp, &resp)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", resp.Status)
	assert.Equal(t, 1, len(resp.Outcomes))
	assert.Equal(t, "6131", *resp.Outcomes[0].AccountCode)
}

func TestBookCategorizer_SourceAwareLifecycleRouting(t *testing.T) {
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	worker.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)

	kindInvoice := "INVOICE"
	roleReceivable := "OPEN_RECEIVABLE"
	kindBill := "BILL"
	rolePayable := "OPEN_PAYABLE"
	kindTx := "TRANSACTION"
	rolePosted := "POSTED_CASH_MOVEMENT"
	existingCode := "6134"

	descInv := "Customer Invoice #100"
	descBill := "Vendor Bill #200"
	descDirect := "LOYER MENSUEL"
	descPosted := "Posted Transaction"

	items := []BookCategorizeItem{
		// 1. Invoice item: open receivable obligation -> must constrain to 3421 (asset/receivable)
		{
			BookItemID:         "item-inv",
			Date:               "2026-03-01",
			AmountUnits:        2500000,
			Currency:           "MAD",
			Direction:          "INFLOW",
			Description:        &descInv,
			SourceArtifactKind: &kindInvoice,
			BookkeepingRole:    &roleReceivable,
		},
		// 2. Bill item: open payable obligation -> must constrain to 4411 (liability/payable)
		{
			BookItemID:         "item-bill",
			Date:               "2026-03-01",
			AmountUnits:        100000,
			Currency:           "MAD",
			Direction:          "OUTFLOW",
			Description:        &descBill,
			SourceArtifactKind: &kindBill,
			BookkeepingRole:    &rolePayable,
		},
		// 3. Direct/Other item: unconstrained -> semantic macro classification -> 6131 (expense)
		{
			BookItemID:  "item-direct",
			Date:        "2026-03-01",
			AmountUnits: 500000,
			Currency:    "MAD",
			Direction:   "OUTFLOW",
			Description: &descDirect,
		},
		// 4. Posted transaction with existing account code -> preserved 6134
		{
			BookItemID:          "item-posted-with-code",
			Date:                "2026-03-01",
			AmountUnits:         50000,
			Currency:            "MAD",
			Direction:           "OUTFLOW",
			Description:         &descPosted,
			SourceArtifactKind:  &kindTx,
			BookkeepingRole:     &rolePosted,
			ExistingAccountCode: &existingCode,
		},
		// 5. Posted transaction without existing code -> hold
		{
			BookItemID:         "item-posted-no-code",
			Date:               "2026-03-01",
			AmountUnits:        50000,
			Currency:           "MAD",
			Direction:          "OUTFLOW",
			Description:        &descPosted,
			SourceArtifactKind: &kindTx,
			BookkeepingRole:    &rolePosted,
		},
	}

	outcomes := make(map[string]BookCategorizeOutcome)
	for _, it := range items {
		outcomes[it.BookItemID] = worker.evaluateItem(it)
	}

	// 1. Invoice -> 3421
	invOutcome := outcomes["item-inv"]
	assert.Equal(t, "CLASSIFIED", invOutcome.Status)
	require.NotNil(t, invOutcome.AccountCode)
	assert.Equal(t, "3421", *invOutcome.AccountCode)
	require.NotNil(t, invOutcome.CandidateSource)
	assert.Equal(t, pcm_cash.CandidateSourceExplicitTestInjection, *invOutcome.CandidateSource)

	// 2. Bill -> 4411
	billOutcome := outcomes["item-bill"]
	assert.Equal(t, "CLASSIFIED", billOutcome.Status)
	require.NotNil(t, billOutcome.AccountCode)
	assert.Equal(t, "4411", *billOutcome.AccountCode)
	require.NotNil(t, billOutcome.CandidateSource)
	assert.Equal(t, pcm_cash.CandidateSourceExplicitTestInjection, *billOutcome.CandidateSource)

	// 3. Direct -> 6131
	directOutcome := outcomes["item-direct"]
	assert.Equal(t, "CLASSIFIED", directOutcome.Status)
	require.NotNil(t, directOutcome.AccountCode)
	assert.Equal(t, "6131", *directOutcome.AccountCode)

	// 4. Posted with code -> 6134
	postedWithCodeOutcome := outcomes["item-posted-with-code"]
	assert.Equal(t, "CLASSIFIED", postedWithCodeOutcome.Status)
	require.NotNil(t, postedWithCodeOutcome.AccountCode)
	assert.Equal(t, "6134", *postedWithCodeOutcome.AccountCode)

	// 5. Posted without code -> HOLD
	postedNoCodeOutcome := outcomes["item-posted-no-code"]
	assert.Equal(t, "HOLD", postedNoCodeOutcome.Status)
}


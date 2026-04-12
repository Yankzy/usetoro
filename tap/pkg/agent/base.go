package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

type BaseAgent struct {
	Logger  *slog.Logger
	Bus     core.EventBus
	Cfg     core.AgentConfig
	Mem     core.MemoryStore
	KP      *identity.KeyPair
	Sub     *nats.Subscription
	Handler nats.MsgHandler
}

// LLMCallback is a retry-aware callback signature. The Redux engine feeds back DomainFaults
// from previous attempts so the LLM can self-correct its patches. currentSeq tracks the
// monotonic sequence for idempotent replay safety. baseState provides the state for optimistic JSON patches.
type LLMCallback func(previousErrors []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error)

// WorkflowConfig holds per-invocation Redux tuning supplied by the calling Agent.
type WorkflowConfig struct {
	SchemaString string           // JSON Schema string for post-patch drift validation
	RBAC         redux.RBACPolicy // Actor path-authorization boundaries
	InitialState []byte           // Base JSON state (nil defaults to "{}")
}

func (b *BaseAgent) ExecuteGlobalWorkflow(
	ctx context.Context,
	queries *database.Queries,
	workflowID pgtype.UUID,
	wfCfg WorkflowConfig,
	llmCallback LLMCallback,
	onComplete func(nextState []byte) error,
) error {
	// ----- Phase 1: Hydrate DB snapshot -----
	b.Logger.Info("🔄 [REDUX] Phase 1: Hydrating workflow state from DB")
	wf, err := queries.GetWorkflow(ctx, workflowID)

	var currentSeq uint64
	var baseState []byte
	if err != nil {
		currentSeq = 0
		baseState = wfCfg.InitialState
	} else {
		currentSeq = uint64(wf.SequenceID)
		if len(wf.State) > 0 {
			baseState = wf.State
		} else {
			baseState = wfCfg.InitialState
		}
	}
	if len(baseState) == 0 {
		baseState = []byte("{}")
	}

	// ----- Phase 2: Boot Redux Store with full middleware stack -----
	b.Logger.Info("🏗️ [REDUX] Phase 2: Initializing Redux Store with schema + RBAC policies")
	cfg := redux.DefaultConfig()
	cfg.SchemaString = wfCfg.SchemaString
	cfg.RBAC = wfCfg.RBAC

	store, storeErr := redux.NewStore(cfg)
	if storeErr != nil {
		return fmt.Errorf("failed to initialize redux store: %w", storeErr)
	}

	// ----- Phase 3: Circuit-breaker LLM retry loop -----
	const maxRetries = 3
	var faults []redux.DomainFault
	var finalState []byte
	var completedEvent redux.RFC6902Event

	for attempt := 0; attempt < maxRetries; attempt++ {
		b.Logger.Info("🧠 [REDUX] Phase 3: Executing LLM callback", "attempt", attempt+1, "previous_faults", len(faults))

		patchArray, llmErr := llmCallback(faults, currentSeq, baseState)
		if llmErr != nil {
			return fmt.Errorf("llm callback failed (attempt %d): %w", attempt+1, llmErr)
		}

		// Construct the event for reduction
		event := redux.RFC6902Event{
			EventID:    fmt.Sprintf("evt_%d", time.Now().UnixNano()),
			SequenceID: currentSeq,
			Timestamp:  time.Now(),
			Type:       b.Cfg.ActivityType,
			Actor:      b.Cfg.DID,
			PatchArray: patchArray,
		}

		// Run the full Redux pipeline: payload bounds → RBAC → patch apply → array ban → schema
		nextState, nextSeq, reduceFaults, reduceErr := store.Reduce(ctx, baseState, currentSeq, []redux.RFC6902Event{event})
		if reduceErr != nil {
			return fmt.Errorf("redux store.Reduce fatal error: %w", reduceErr)
		}

		if len(reduceFaults) > 0 {
			b.Logger.Warn("⚠️ [REDUX] DomainFaults detected, feeding back to LLM", "faults", len(reduceFaults), "attempt", attempt+1)
			faults = reduceFaults
			continue
		}

		// Success — all middleware passed
		faults = nil
		finalState = nextState
		currentSeq = nextSeq
		completedEvent = event
		break
	}

	if len(faults) > 0 {
		return fmt.Errorf("redux circuit breaker tripped after %d retries: %d unresolved faults", maxRetries, len(faults))
	}

	// ----- Phase 4: Publish validated event to JetStream trace -----
	b.Logger.Info("📡 [REDUX] Phase 4: Publishing validated Redux event to JetStream")

	eventBytes, _ := json.Marshal(completedEvent)

	var uuidStr string
	if workflowID.Valid {
		uuidBytes, _ := workflowID.Value()
		uuidStr = uuidBytes.(string)
	}
	traceTopic := fmt.Sprintf("workflow.trace.%s", uuidStr)

	if pubErr := b.Bus.Publish(traceTopic, eventBytes); pubErr != nil {
		return fmt.Errorf("failed to publish redux trace to JetStream: %w", pubErr)
	}

	// ----- Phase 5: Invoke onComplete with validated state -----
	if onComplete != nil {
		b.Logger.Info("✅ [REDUX] Phase 5: Invoking onComplete handler with validated state")
		if err := onComplete(finalState); err != nil {
			return fmt.Errorf("onComplete handler failed: %w", err)
		}
	}

	return nil
}

func NewBaseAgent(
	logger *slog.Logger,
	bus core.EventBus,
	cfg core.AgentConfig,
	mem core.MemoryStore,
	handler nats.MsgHandler,
) *BaseAgent {
	// ActivityType is the seed for the deterministic DID — it MUST be set so the
	// agent's identity (and NATS durable consumer name) is stable across restarts.
	if cfg.ActivityType == "" {
		panic("NewBaseAgent: cfg.ActivityType must be set; it is used as the DID seed")
	}
	kp, err := identity.KeyPairFromSeed(cfg.ActivityType)
	if err != nil {
		panic("NewBaseAgent: KeyPairFromSeed failed: " + err.Error())
	}
	cfg.DID = identity.CreateDID(kp.Public)
	// Derive runtime routing fields from DID so callers don't set them manually.
	safe := sanitizeDID(cfg.DID)
	cfg.QueueGroup = safe + "-group"
	cfg.DurableName = safe + "-durable"
	return &BaseAgent{
		Logger:  logger,
		Bus:     bus,
		Cfg:     cfg,
		Mem:     mem,
		KP:      kp,
		Handler: handler,
	}
}

// sanitizeDID converts a DID string into a NATS-safe identifier (no colons).
func sanitizeDID(did string) string {
	return strings.ReplaceAll(did, ":", "-")
}

func (b *BaseAgent) Start() error {
	b.Logger.Info("🤖 TAP AI Agent Initializing...", "did", b.Cfg.DID, "activity_type", b.Cfg.ActivityType)

	if b.Cfg.ActivityType != "" {
		normalizedQueue, err := core.NormalizeTaskQueue(b.Cfg.ActivityType, b.Cfg.TaskQueue)
		if err != nil {
			return fmt.Errorf("agent %s: invalid task queue for activity %s: %w", b.Cfg.DID, b.Cfg.ActivityType, err)
		}
		b.Cfg.TaskQueue = normalizedQueue
	}

	// Register with Almanac — advertise DID, inbox, and activity_type capability.
	inbox := core.BuildAgentInbox(b.Cfg.DID)
	regPayload := map[string]interface{}{
		"did":       b.Cfg.DID,
		"endpoints": []string{inbox},
		"capabilities": []map[string]interface{}{
			{"activity_type": b.Cfg.ActivityType},
		},
		"expiry": time.Now().Add(24 * time.Hour).Unix(),
	}
	regBytes, err := json.Marshal(regPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal registration payload: %w", err)
	}
	if err := b.Bus.Publish(core.SubjectAlmanacRegister, regBytes); err != nil {
		return fmt.Errorf("agent %s: failed to register with almanac: %w", b.Cfg.DID, err)
	}

	// ── Subscription 1: Private inbox (always active) ──────────────────────────
	// The Orchestrator dispatches ACCEPT envelopes here for assigned work.
	safe := sanitizeDID(b.Cfg.DID)
	inboxSub, err := b.Bus.QueueSubscribe(
		inbox,
		safe+"-inbox-group",
		b.Handler,
		nats.Durable(safe+"-inbox"),
		nats.DeliverAll(),
		nats.AckExplicit(),
	)
	if err != nil {
		return fmt.Errorf("agent %s: failed to subscribe to inbox %s: %w", b.Cfg.DID, inbox, err)
	}
	b.Sub = inboxSub
	b.Logger.Info("👂 Listening on private inbox", "topic", inbox)

	// ── Subscription 2: Public Task Queue (when Orchestrator assigns one) ──────
	// Agents subscribe here to receive CFPs and participate in FIPA bidding.
	if b.Cfg.TaskQueue != "" {
		_, err := b.Bus.QueueSubscribe(
			b.Cfg.TaskQueue,
			b.Cfg.QueueGroup,
			b.Handler,
			nats.Durable(b.Cfg.DurableName),
			nats.DeliverAll(),
			nats.AckExplicit(),
		)
		if err != nil {
			return fmt.Errorf("agent %s: failed to subscribe to task queue %s: %w", b.Cfg.DID, b.Cfg.TaskQueue, err)
		}
		b.Logger.Info("👂 Listening on task queue", "topic", b.Cfg.TaskQueue)
	}

	return nil
}

func (b *BaseAgent) Stop() error {
	if b.Sub != nil {
		return nil
	}
	return nil
}

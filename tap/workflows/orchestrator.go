package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"gopkg.in/yaml.v3"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// OrchestratorInbox is the subject where Agents send their PROPOSE bids and INFORM proofs back.
const OrchestratorInbox = "orchestrator.inbox"

// OrchestratorDID is the canonical DID used by the Orchestrator in all FIPA envelopes.
const OrchestratorDID = "did:toro:orchestrator"

const (
	// WorkflowTriggerStream is the JetStream stream used to ingest workflow trigger messages.
	WorkflowTriggerStream = "WORKFLOW_TRIGGERS"

	// WorkflowTriggerConsumer is the durable consumer name used by orchestrator instances.
	WorkflowTriggerConsumer = "orchestrator-triggers"

	// WorkflowTriggerDeliverSubject is where JetStream will deliver trigger messages for the consumer.
	// We subscribe to this via a single NATS queue subscription, regardless of how many trigger topics exist.
	WorkflowTriggerDeliverSubject = "orchestrator.triggers.deliver"

	// WorkflowTriggerDeliverGroup is the queue group used for horizontal scaling across orchestrator instances.
	WorkflowTriggerDeliverGroup = "orchestrator-trigger-group"
)

// Orchestrator is the singleton Workflow Orchestrator service.
// It loads WorkflowDef blueprints at boot, subscribes to trigger topics, and advances
// WorkflowInstance state machines persisted in toro_core.workflows.
type Orchestrator struct {
	logger  *slog.Logger
	queries *database.Queries
	bus     core.EventBus

	nc *nats.Conn
	js nats.JetStreamContext

	blueprintMu sync.RWMutex
	blueprints  []WorkflowDef

	// subs holds the NATS subscriptions so we can drain them on shutdown.
	subs []*nats.Subscription
}

// InstanceState represents the internal state stored in the DB JSONB field.
type InstanceState struct {
	WorkflowDef   string                 `json:"workflow_def"`
	CurrentStepID string                 `json:"current_step_id"`
	Variables     map[string]interface{} `json:"variables"`
}

// NewOrchestrator initialises the central orchestrator system.
func NewOrchestrator(logger *slog.Logger, bus core.EventBus, nc *nats.Conn, js nats.JetStreamContext, queries *database.Queries) *Orchestrator {
	return &Orchestrator{
		logger:  logger,
		bus:     bus,
		nc:      nc,
		js:      js,
		queries: queries,
	}
}

// resolveActorByCapability performs a live NATS Request to the Almanac to discover
// an agent that advertises the given activity_type as a capability.
// Returns the first matching agent's inbox subject.
func (o *Orchestrator) resolveActorByCapability(ctx context.Context, activityType string) (string, error) {
	query := map[string]interface{}{
		"caller_did":      OrchestratorDID,
		"capability_type": activityType,
	}
	qBytes, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("almanac: marshal query: %w", err)
	}

	ctx5, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := o.bus.RequestWithContext(ctx5, core.SubjectAlmanacQuery, qBytes)
	if err != nil {
		return "", fmt.Errorf("almanac: query failed for capability %q: %w", activityType, err)
	}

	var entries []struct {
		DID       string   `json:"did"`
		Endpoints []string `json:"endpoints"`
	}
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return "", fmt.Errorf("almanac: unmarshal response: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("almanac: no agent registered for activity_type %q", activityType)
	}

	// Use the first matching agent's inbox (first endpoint).
	entry := entries[0]
	if len(entry.Endpoints) == 0 {
		return core.BuildAgentInbox(entry.DID), nil
	}
	return entry.Endpoints[0], nil
}

// LoadFromDir reads all YAML pipeline definitions from dirPath and upserts them into the DB blueprint registry.
// This is treated as a bootstrap mechanism; the DB remains the runtime source of truth.
func (o *Orchestrator) LoadFromDir(ctx context.Context, dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			o.logger.Warn("Orchestrator: workflow config directory does not exist", "path", dirPath)
			return nil
		}
		return fmt.Errorf("orchestrator: failed to read workflow config directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		fullPath := filepath.Join(dirPath, entry.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			o.logger.Error("Orchestrator: failed to read workflow file", "file", entry.Name(), "error", err)
			continue
		}

		var wfDef WorkflowDef
		if err := yaml.Unmarshal(data, &wfDef); err != nil {
			o.logger.Error("Orchestrator: failed to parse workflow file", "file", entry.Name(), "error", err)
			continue
		}

		defBytes, err := json.Marshal(wfDef)
		if err != nil {
			o.logger.Error("Orchestrator: failed to marshal workflow definition", "file", entry.Name(), "error", err)
			continue
		}

		_, err = o.queries.UpsertWorkflowBlueprint(ctx, database.UpsertWorkflowBlueprintParams{
			Name:         wfDef.Name,
			TriggerTopic: wfDef.TriggerTopic,
			Definition:   defBytes,
		})
		if err != nil {
			o.logger.Error("Orchestrator: failed to upsert workflow blueprint", "name", wfDef.Name, "error", err)
			continue
		}

		o.logger.Info("Orchestrator: upserted workflow blueprint", "name", wfDef.Name, "steps", len(wfDef.Steps), "trigger_topic", wfDef.TriggerTopic)
	}
	return nil
}

// SyncBlueprints refreshes the in-memory snapshot of workflow blueprints from the DB and reconciles
// the WORKFLOW_TRIGGERS stream subjects to exactly match the current set of trigger topics.
func (o *Orchestrator) SyncBlueprints(ctx context.Context) error {
	rows, err := o.queries.GetWorkflowBlueprints(ctx)
	if err != nil {
		return fmt.Errorf("get workflow blueprints: %w", err)
	}

	next := make([]WorkflowDef, 0, len(rows))
	subjectSet := make(map[string]struct{}, len(rows))

	for _, row := range rows {
		var def WorkflowDef
		if err := json.Unmarshal(row.Definition, &def); err != nil {
			// Treat this as a hard error: stream reconciliation would be wrong.
			return fmt.Errorf("unmarshal blueprint %q: %w", row.Name, err)
		}
		if def.Name == "" {
			def.Name = row.Name
		}
		if def.TriggerTopic == "" {
			def.TriggerTopic = row.TriggerTopic
		}
		next = append(next, def)

		if row.TriggerTopic != "" {
			subjectSet[row.TriggerTopic] = struct{}{}
		} else if def.TriggerTopic != "" {
			subjectSet[def.TriggerTopic] = struct{}{}
		}
	}

	o.blueprintMu.Lock()
	o.blueprints = next
	o.blueprintMu.Unlock()

	subjects := make([]string, 0, len(subjectSet))
	for s := range subjectSet {
		if s == "" {
			continue
		}
		subjects = append(subjects, s)
	}

	if len(subjects) == 0 {
		o.logger.Warn("Orchestrator: no workflow trigger topics found; WORKFLOW_TRIGGERS stream subjects not updated")
		return nil
	}

	if err := o.ensureWorkflowTriggerStream(subjects); err != nil {
		return fmt.Errorf("ensure %s stream: %w", WorkflowTriggerStream, err)
	}

	o.logger.Info("Orchestrator: synced workflow blueprints", "count", len(next), "trigger_topics", len(subjects))
	return nil
}

func (o *Orchestrator) ensureWorkflowTriggerStream(subjects []string) error {
	info, err := o.js.StreamInfo(WorkflowTriggerStream)
	if err != nil {
		if err == nats.ErrStreamNotFound {
			_, err := o.js.AddStream(&nats.StreamConfig{
				Name:      WorkflowTriggerStream,
				Subjects:  subjects,
				Retention: nats.WorkQueuePolicy,
				Storage:   nats.FileStorage,
				MaxAge:    72 * time.Hour,
			})
			return err
		}
		return err
	}

	if subjectsExactMatch(info.Config.Subjects, subjects) {
		return nil
	}

	newCfg := info.Config
	newCfg.Subjects = subjects
	_, err = o.js.UpdateStream(&newCfg)
	return err
}

func subjectsExactMatch(existing, required []string) bool {
	if len(existing) != len(required) {
		return false
	}
	m := make(map[string]struct{}, len(existing))
	for _, s := range existing {
		m[s] = struct{}{}
	}
	for _, s := range required {
		if _, ok := m[s]; !ok {
			return false
		}
	}
	return true
}

func (o *Orchestrator) startUnifiedTriggerSubscription(ctx context.Context) error {
	o.blueprintMu.RLock()
	hasBlueprints := len(o.blueprints) > 0
	o.blueprintMu.RUnlock()

	if !hasBlueprints {
		o.logger.Warn("Orchestrator: no blueprints loaded; unified trigger subscription not started")
		return nil
	}

	if err := o.ensureOrchestratorTriggerConsumer(); err != nil {
		return err
	}

	sub, err := o.nc.QueueSubscribe(WorkflowTriggerDeliverSubject, WorkflowTriggerDeliverGroup, func(msg *nats.Msg) {
		o.handleUnifiedTrigger(ctx, msg)
	})
	if err != nil {
		return err
	}

	o.subs = append(o.subs, sub)
	o.logger.Info("Orchestrator: listening for triggers",
		"stream", WorkflowTriggerStream,
		"consumer", WorkflowTriggerConsumer,
	)
	return nil
}

func (o *Orchestrator) ensureOrchestratorTriggerConsumer() error {
	// Ensure the stream exists first.
	if _, err := o.js.StreamInfo(WorkflowTriggerStream); err != nil {
		return fmt.Errorf("trigger stream not found: %w", err)
	}

	desired := &nats.ConsumerConfig{
		Durable:        WorkflowTriggerConsumer,
		DeliverSubject: WorkflowTriggerDeliverSubject,
		DeliverGroup:   WorkflowTriggerDeliverGroup,
		AckPolicy:      nats.AckExplicitPolicy,
		AckWait:        2 * time.Minute,
		MaxDeliver:     10,
		MaxAckPending:  2048,
		DeliverPolicy:  nats.DeliverAllPolicy,
		ReplayPolicy:   nats.ReplayInstantPolicy,
	}

	ci, err := o.js.ConsumerInfo(WorkflowTriggerStream, WorkflowTriggerConsumer)
	if err != nil {
		if err == nats.ErrConsumerNotFound {
			_, err := o.js.AddConsumer(WorkflowTriggerStream, desired)
			return err
		}
		return err
	}

	// If config drifted (e.g., deliver subject/group), recreate the consumer.
	if ci.Config.DeliverSubject != desired.DeliverSubject ||
		ci.Config.DeliverGroup != desired.DeliverGroup ||
		ci.Config.AckPolicy != desired.AckPolicy ||
		ci.Config.DeliverPolicy != desired.DeliverPolicy {
		if err := o.js.DeleteConsumer(WorkflowTriggerStream, WorkflowTriggerConsumer); err != nil {
			return fmt.Errorf("delete trigger consumer: %w", err)
		}
		_, err := o.js.AddConsumer(WorkflowTriggerStream, desired)
		return err
	}

	return nil
}

func (o *Orchestrator) handleUnifiedTrigger(ctx context.Context, msg *nats.Msg) {
	defer func() {
		if rec := recover(); rec != nil {
			o.logger.Error("Orchestrator: panic in unified trigger handler", "error", rec)
			msg.Nak()
		}
	}()

	subject := msg.Subject
	if msg.Header != nil {
		if jsSub := msg.Header.Get(nats.JSSubject); jsSub != "" {
			subject = jsSub
		}
	}

	def, err := o.resolveBlueprintForSubject(subject)
	if err != nil {
		// Not a workflow trigger we know about (or cache is stale). Ack so it doesn't clog the work queue.
		o.logger.Debug("Orchestrator: no blueprint matched trigger", "subject", subject)
		msg.Ack()
		return
	}

	if err := o.handleTrigger(ctx, def, msg); err != nil {
		o.logger.Error("Orchestrator: trigger handler error", "workflow", def.Name, "subject", subject, "error", err)
		msg.Nak()
		return
	}
	msg.Ack()
}

func (o *Orchestrator) resolveBlueprintForSubject(subject string) (WorkflowDef, error) {
	o.blueprintMu.RLock()
	defer o.blueprintMu.RUnlock()

	bestScore := -1
	var best WorkflowDef

	for _, def := range o.blueprints {
		if def.TriggerTopic == "" {
			continue
		}
		if !matchNATSSubject(def.TriggerTopic, subject) {
			continue
		}
		score := triggerPatternScore(def.TriggerTopic)
		if score > bestScore {
			bestScore = score
			best = def
		}
	}

	if bestScore < 0 {
		return WorkflowDef{}, fmt.Errorf("no blueprint matched subject %q", subject)
	}
	return best, nil
}

func (o *Orchestrator) resolveBlueprintByName(ctx context.Context, name string) (WorkflowDef, error) {
	o.blueprintMu.RLock()
	for _, def := range o.blueprints {
		if def.Name == name {
			o.blueprintMu.RUnlock()
			return def, nil
		}
	}
	o.blueprintMu.RUnlock()

	row, err := o.queries.GetBlueprintByName(ctx, name)
	if err != nil {
		return WorkflowDef{}, err
	}

	var def WorkflowDef
	if err := json.Unmarshal(row.Definition, &def); err != nil {
		return WorkflowDef{}, err
	}
	if def.Name == "" {
		def.Name = row.Name
	}
	if def.TriggerTopic == "" {
		def.TriggerTopic = row.TriggerTopic
	}
	return def, nil
}

func (o *Orchestrator) resolveBlueprintByNameOrTriggerTopic(ctx context.Context, query string) (WorkflowDef, error) {
	o.blueprintMu.RLock()
	for _, def := range o.blueprints {
		if def.Name == query || def.TriggerTopic == query {
			o.blueprintMu.RUnlock()
			return def, nil
		}
	}
	o.blueprintMu.RUnlock()

	row, err := o.queries.GetBlueprintByNameOrTriggerTopic(ctx, query)
	if err != nil {
		return WorkflowDef{}, err
	}
	var def WorkflowDef
	if err := json.Unmarshal(row.Definition, &def); err != nil {
		return WorkflowDef{}, err
	}
	if def.Name == "" {
		def.Name = row.Name
	}
	if def.TriggerTopic == "" {
		def.TriggerTopic = row.TriggerTopic
	}
	return def, nil
}

// matchNATSSubject checks if a NATS wildcard subject (pattern) matches a concrete subject.
// Supports `*` (one token) and `>` (the rest).
func matchNATSSubject(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if pattern == "" || subject == "" {
		return false
	}

	p := strings.Split(pattern, ".")
	s := strings.Split(subject, ".")

	for i := 0; i < len(p); i++ {
		pt := p[i]
		if pt == ">" {
			return true
		}
		if i >= len(s) {
			return false
		}
		if pt == "*" {
			continue
		}
		if pt != s[i] {
			return false
		}
	}

	return len(p) == len(s)
}

func triggerPatternScore(pattern string) int {
	toks := strings.Split(pattern, ".")
	exact := 0
	wild := 0
	hasGt := false
	for _, t := range toks {
		switch t {
		case "*":
			wild++
		case ">":
			hasGt = true
			wild++
		default:
			exact++
		}
	}
	score := exact*1000 + len(toks)*10 - wild*5
	if hasGt {
		score -= 1
	}
	return score
}

// Start listens on the orchestrator inbox for proofs returned by agents and consumes workflow triggers
// via a single JetStream consumer (no per-workflow subscriptions).
func (o *Orchestrator) Start(ctx context.Context) error {
	if o.nc == nil || o.js == nil {
		return fmt.Errorf("orchestrator: missing nats dependencies (nc/js)")
	}

	// Subscribe to OrchestratorInbox to receive PROPOSE bids and INFORM proofs from agents.
	inboxSub, err := o.bus.QueueSubscribe(
		OrchestratorInbox,
		"orchestrator-group",
		o.handleIncoming,
		nats.Durable("orchestrator-inbox-durable"),
		nats.DeliverAll(),
		nats.AckExplicit(),
	)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to subscribe to inbox: %w", err)
	}
	o.subs = append(o.subs, inboxSub)
	o.logger.Info("Orchestrator: listening on inbox", "subject", OrchestratorInbox)

	// Sync blueprints from DB and ensure trigger stream/consumer are configured.
	if err := o.SyncBlueprints(ctx); err != nil {
		return fmt.Errorf("orchestrator: failed to sync blueprints: %w", err)
	}

	// Single subscription for all workflow triggers (driven by WORKFLOW_TRIGGERS stream subjects).
	if err := o.startUnifiedTriggerSubscription(ctx); err != nil {
		return fmt.Errorf("orchestrator: failed to start unified trigger subscription: %w", err)
	}

	// ── Blueprint Query Listener ──────────────────────────────────────────────
	// Allows the API Gateway to fetch the full graph (steps) for a workflow.
	blueprintSub, err := o.bus.QueueSubscribe(
		"workflow.query.blueprint",
		"orchestrator-query-group",
		o.handleBlueprintQuery,
	)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to subscribe to blueprint query: %w", err)
	}
	o.subs = append(o.subs, blueprintSub)

	o.blueprintMu.RLock()
	loaded := len(o.blueprints)
	o.blueprintMu.RUnlock()

	o.logger.Info("🎛️  Workflow Orchestrator active",
		"blueprints_loaded", loaded,
		"discovery", "almanac",
	)

	<-ctx.Done()

	// Best-effort drain; shutdown ordering is managed by the daemon.
	for _, sub := range o.subs {
		_ = sub.Drain()
	}
	return nil
}

// handleTrigger fires when a workflow's trigger_topic receives a message.
// It creates a fresh WorkflowInstance and dispatches the first step.
func (o *Orchestrator) handleTrigger(ctx context.Context, def WorkflowDef, msg *nats.Msg) error {
	if len(def.Steps) == 0 {
		return fmt.Errorf("workflow %q has no steps", def.Name)
	}

	instanceID := uuid.New()
	o.logger.Info("Orchestrator: workflow triggered",
		"workflow", def.Name,
		"instance_id", instanceID.String(),
	)

	// --- Phase 1: Extract entity_id from the trigger envelope ---
	// The trigger message is a FIPA Envelope wrapping a TaskDefinition.
	// The TaskDefinition.Payload contains the original upload payload with entity_id.
	var entityID pgtype.UUID
	var triggerPayload []byte = msg.Data // fallback if not wrapped

	var triggerEnv core.Envelope
	if err := json.Unmarshal(msg.Data, &triggerEnv); err == nil {
		var taskDef core.TaskDefinition
		if err := json.Unmarshal(triggerEnv.Body, &taskDef); err == nil {
			triggerPayload = taskDef.Payload // Extract inner payload for the first Step
			var innerPayload map[string]interface{}
			if err := json.Unmarshal(taskDef.Payload, &innerPayload); err == nil {
				if eid, ok := innerPayload["entity_id"].(string); ok {
					if uuidEID, err := uuid.Parse(eid); err == nil {
						entityID = pgtype.UUID{Bytes: uuidEID, Valid: true}
					}
				}
			}
		}
	}

	if !entityID.Valid {
		o.logger.Warn("Orchestrator: no valid entity_id found in trigger payload, skipping workflow creation")
		return fmt.Errorf("orchestrator: trigger payload missing required entity_id")
	}

	// --- Phase 2: Persist initial state to DB ---
	initialState := map[string]interface{}{
		"workflow_def":    def.Name,
		"current_step_id": def.Steps[0].ID,
		"variables":       make(map[string]interface{}),
	}
	stateBytes, _ := json.Marshal(initialState)

	arg := database.CreateOrGetWorkflowParams{
		ID:       pgtype.UUID{Bytes: instanceID, Valid: true},
		EntityID: entityID,
	}
	wf, err := o.queries.CreateOrGetWorkflow(ctx, arg)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to create workflow row: %w", err)
	}

	// Update with initial state and status
	_, err = o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
		ID:         wf.ID,
		State:      stateBytes,
		SequenceID: 0,
	})
	if err != nil {
		return fmt.Errorf("orchestrator: failed to update initial state: %w", err)
	}

	o.logger.Info("Orchestrator: instance persisted", "id", instanceID.String())

	o.publishStatus(instanceID.String(), entityID, "started", def.Steps[0].ID, "", def)

	// Dispatch the first step
	return o.dispatchStep(ctx, def.Steps[0], instanceID.String(), triggerPayload)
}

// dispatchStep sends work to the actor responsible for the given step.
// If negotiate=true: broadcast a CFP to the public task_queue.
// If negotiate=false: send an ACCEPT directly to the internal worker/agent inbox.
func (o *Orchestrator) dispatchStep(ctx context.Context, step WorkflowStep, instanceID string, payload []byte) error {
	convID := instanceID + "." + step.ID

	if step.Negotiate {
		queue, err := core.NormalizeTaskQueue(step.ActivityType, step.TaskQueue)
		if err != nil {
			return fmt.Errorf("orchestrator: invalid task queue for step %q: %w", step.ID, err)
		}

		o.logger.Info("Orchestrator: dispatching step",
			"step_id", step.ID,
			"activity_type", step.ActivityType,
			"negotiate", step.Negotiate,
			"task_queue", queue,
		)

		// ── FIPA path: broadcast CFP to the public task_queue ─────────────────
		// 3rd-party agents (Firecracker) and internal agents listening on this
		// topic can all reply with PROPOSE to the orchestrator inbox.
		taskDef := core.TaskDefinition{
			ID:             instanceID,
			Domain:         step.ActivityType,
			Payload:        payload,
			WorkflowSchema: step.WorkflowSchema,
		}
		cfp, err := core.NewEnvelope(
			uuid.New().String(),
			OrchestratorDID,
			"", // broadcast — no specific receiver
			convID,
			core.CFP,
			taskDef,
		)
		if err != nil {
			return fmt.Errorf("orchestrator: failed to build CFP: %w", err)
		}
		cfpBytes, _ := json.Marshal(cfp)
		if err := o.bus.Publish(queue, cfpBytes); err != nil {
			return fmt.Errorf("orchestrator: failed to publish CFP to %s: %w", queue, err)
		}
		o.logger.Info("Orchestrator: CFP broadcast", "task_queue", queue, "conv_id", convID)

	} else {
		// ── Direct path: dispatch to a specific actor ─────────────────────────
		// If task_queue is defined (or derivable for workers), we use it directly as the inbox.
		// Otherwise, query Almanac at dispatch time for a live actor that advertises
		// the step's activity_type capability.
		var inbox string

		if strings.HasPrefix(step.ActivityType, core.PrefixWorkerActivities+".") {
			normalized, err := core.NormalizeTaskQueue(step.ActivityType, step.TaskQueue)
			if err != nil {
				return fmt.Errorf("orchestrator: invalid worker queue for step %q: %w", step.ID, err)
			}
			inbox = normalized
			o.logger.Debug("Orchestrator: using worker inbox for dispatch", "inbox", inbox)
		} else if step.TaskQueue != "" {
			inbox = step.TaskQueue
			o.logger.Debug("Orchestrator: using direct queue for dispatch", "inbox", inbox)
		} else {
			discoveredInbox, err := o.resolveActorByCapability(ctx, step.ActivityType)
			if err != nil {
				return fmt.Errorf("orchestrator: almanac lookup failed for step %q: %w", step.ID, err)
			}
			inbox = discoveredInbox
			o.logger.Info("Orchestrator: resolved actor via Almanac",
				"activity_type", step.ActivityType,
				"inbox", inbox,
			)
		}

		o.logger.Info("Orchestrator: dispatching step",
			"step_id", step.ID,
			"activity_type", step.ActivityType,
			"negotiate", step.Negotiate,
			"task_queue", inbox,
		)

		taskDef := core.TaskDefinition{
			ID:             instanceID,
			Domain:         step.ActivityType,
			Payload:        payload,
			WorkflowSchema: step.WorkflowSchema,
		}
		accept, err := core.NewEnvelope(
			uuid.New().String(),
			OrchestratorDID,
			"", // Specific DID not strictly required for direct inbox delivery
			convID,
			core.ACCEPT_PROPOSAL,
			taskDef,
		)
		if err != nil {
			return fmt.Errorf("orchestrator: failed to build ACCEPT: %w", err)
		}
		acceptBytes, _ := json.Marshal(accept)

		if err := o.bus.Publish(inbox, acceptBytes); err != nil {
			return fmt.Errorf("orchestrator: failed to dispatch to %s: %w", inbox, err)
		}
		o.logger.Info("Orchestrator: dispatched directly to actor", "inbox", inbox, "step", step.ID)
	}

	return nil
}

// handleIncoming processes messages arriving at the OrchestratorInbox:
// - PROPOSE   → log bid; in the future, select best proposal and send ACCEPT
// - INFORM    → step completed; advance to next step
func (o *Orchestrator) handleIncoming(msg *nats.Msg) {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		o.logger.Error("Orchestrator: malformed envelope on inbox", "error", err)
		msg.Term()
		return
	}

	ctx := context.Background()

	switch env.Performative {
	case core.PROPOSE:
		// TODO: implement bid selection strategy (price, ETA).
		// For now, auto-accept the first proposal by replying with ACCEPT_PROPOSAL to the proposer's inbox.
		o.logger.Info("Orchestrator: received PROPOSE (auto-accept stub)",
			"from", env.SenderDID,
			"conv_id", env.ConversationID,
		)
		msg.Ack()

	case core.INFORM:
		// An agent has completed its step and delivered a Proof.
		o.logger.Info("Orchestrator: received INFORM (step complete)",
			"from", env.SenderDID,
			"conv_id", env.ConversationID,
		)

		// 1. Unmarshal ConversationID (instanceID.stepID)
		parts := strings.Split(env.ConversationID, ".")
		if len(parts) < 2 {
			o.logger.Error("Orchestrator: malformed conversation ID", "id", env.ConversationID)
			msg.Term()
			return
		}
		instanceIDStr := parts[0]
		stepID := parts[1]

		instanceID, err := uuid.Parse(instanceIDStr)
		if err != nil {
			o.logger.Error("Orchestrator: invalid instance ID", "id", instanceIDStr, "error", err)
			msg.Term()
			return
		}

		// 2. Fetch current state from DB
		wf, err := o.queries.GetWorkflow(ctx, pgtype.UUID{Bytes: instanceID, Valid: true})
		if err != nil {
			o.logger.Error("Orchestrator: failed to fetch workflow", "id", instanceIDStr, "error", err)
			msg.Nak()
			return
		}

		var state InstanceState
		if err := json.Unmarshal(wf.State, &state); err != nil {
			o.logger.Error("Orchestrator: failed to unmarshal state", "id", instanceIDStr, "error", err)
			msg.Term()
			return
		}

		if state.CurrentStepID != stepID {
			o.logger.Warn("Orchestrator: out-of-order proof received", "expected", state.CurrentStepID, "got", stepID)
			msg.Ack() // Drop it, it's stale
			return
		}

		// 3. Resolve the WorkflowDef blueprint (DB-backed).
		wfDef, err := o.resolveBlueprintByName(ctx, state.WorkflowDef)
		if err != nil {
			o.logger.Error("Orchestrator: workflow definition not found", "name", state.WorkflowDef, "error", err)
			msg.Term()
			return
		}

		var nextStep *WorkflowStep
		for i, s := range wfDef.Steps {
			if s.ID == stepID {
				if i+1 < len(wfDef.Steps) {
					nextStep = &wfDef.Steps[i+1]
				}
				break
			}
		}

		// 4. Update state and dispatch
		if nextStep == nil {
			// Workflow finished!
			o.logger.Info("🏁 Orchestrator: workflow completed", "instance_id", instanceIDStr)
			state.CurrentStepID = "COMPLETED"
			stateBytes, _ := json.Marshal(state)
			o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
				ID:         wf.ID,
				State:      stateBytes,
				SequenceID: wf.SequenceID + 1,
			})
			o.publishStatus(instanceIDStr, wf.EntityID, "completed", "COMPLETED", env.SenderDID, wfDef)
		} else {
			o.logger.Info("Orchestrator: advancing step", "from", stepID, "to", nextStep.ID)
			state.CurrentStepID = nextStep.ID
			stateBytes, _ := json.Marshal(state)
			o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
				ID:         wf.ID,
				State:      stateBytes,
				SequenceID: wf.SequenceID + 1,
			})

			o.publishStatus(instanceIDStr, wf.EntityID, "running", nextStep.ID, "", wfDef)

			// The payload for the next step is often the Proof from the previous step.
			// In FIPA, the INFORM body contains the Proof.
			if err := o.dispatchStep(ctx, *nextStep, instanceIDStr, env.Body); err != nil {
				o.logger.Error("Orchestrator: failed to dispatch next step", "error", err)
				msg.Nak()
				return
			}
		}

		msg.Ack()

	default:
		o.logger.Warn("Orchestrator: unexpected performative on inbox", "perf", env.Performative)
		msg.Ack()
	}
}

// sanitize converts a workflow name into a NATS-safe durable consumer name.
func sanitize(s string) string {
	r := strings.NewReplacer(" ", "-", ".", "-", ":", "-", "/", "-")
	return strings.ToLower(r.Replace(s))
}

func (o *Orchestrator) publishStatus(instanceID string, entityID pgtype.UUID, status, stepID, assignedDID string, blueprint WorkflowDef) {
	evt := map[string]interface{}{
		"instance_id":     instanceID,
		"entity_id":       uuid.UUID(entityID.Bytes).String(),
		"status":          status,
		"current_step_id": stepID,
		"assigned_did":    assignedDID,
		"blueprint":       blueprint,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)
	subject := fmt.Sprintf("workflow.events.%s", status)
	if err := o.bus.Publish(subject, data); err != nil {
		o.logger.Error("Orchestrator: failed to publish status event", "error", err)
	}
}

// handleBlueprintQuery responds with the full WorkflowDef for a specific workflow name or trigger topic.
func (o *Orchestrator) handleBlueprintQuery(msg *nats.Msg) {
	// Query can be a name or a trigger topic.
	query := string(msg.Data)
	o.logger.Debug("Orchestrator: blueprint query received", "query", query)

	ctx := context.Background()
	def, err := o.resolveBlueprintByNameOrTriggerTopic(ctx, query)
	if err != nil {
		_ = msg.Respond([]byte(`{"error": "workflow not found"}`))
		return
	}

	res, _ := json.Marshal(def)
	_ = msg.Respond(res)
}

// GetTaskQueues returns a mapping from ActivityType to the public TaskQueue defined in loaded workflows.
func (o *Orchestrator) GetTaskQueues() map[string]string {
	mapping := make(map[string]string)
	o.blueprintMu.RLock()
	defer o.blueprintMu.RUnlock()
	for _, wf := range o.blueprints {
		for _, step := range wf.Steps {
			// Only map task queues for steps that require NATS negotiation.
			if step.Negotiate && step.ActivityType != "" {
				queue, err := core.NormalizeTaskQueue(step.ActivityType, step.TaskQueue)
				if err != nil || queue == "" {
					continue
				}
				mapping[step.ActivityType] = queue
			}
		}
	}
	return mapping
}

// keepaliveTick returns a timer string — utility for future heartbeat loop.
func keepaliveTick() string { return time.Now().Format(time.RFC3339) }

package workflows

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

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

	// DLQ subjects for poisoned messages.
	WorkflowTriggerDLQSubject = "workflow.dlq.trigger"
	WorkflowInboxDLQSubject   = "workflow.dlq.inbox"
	WorkflowResumeSubject     = "workflow.resume"
	WorkflowResumeDurable     = "orchestrator-resume-durable"
	WorkflowResumeGroup       = "orchestrator-resume-group"
	WorkflowAmbiguousSubject  = "workflow.events.ambiguous"

	// Redelivery limits before emitting to DLQ.
	orchestratorDeliverLimit = 5
)

var errMessageDeadLettered = errors.New("message already dead lettered")

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
	WorkflowDef      string                     `json:"workflow_def"`
	CurrentStepID    string                     `json:"current_step_id"`
	InstancePath     []string                   `json:"instance_path"`
	ActiveSteps      map[string]bool            `json:"active_steps"`
	CompletedSteps   map[string]bool            `json:"completed_steps"`
	Variables        map[string]json.RawMessage `json:"variables"`
	LastProof        json.RawMessage            `json:"last_proof"`
	ParentStepID     string                     `json:"parent_step_id,omitempty"`
	Suspended        bool                       `json:"suspended"`
	SuspensionStep   string                     `json:"suspension_step,omitempty"`
	SuspensionReason string                     `json:"suspension_reason,omitempty"`
	SuspensionRoute  int                        `json:"suspension_route,omitempty"`
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

	o.logger.Info("📂 Orchestrator: loading workflows from directory", "path", dirPath)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		fullPath := filepath.Join(dirPath, entry.Name())
		if err := o.UpsertWorkflowFromFile(ctx, fullPath); err != nil {
			o.logger.Error("❌ Orchestrator: failed to load workflow file", "file", entry.Name(), "error", err)
		}
	}
	return nil
}

// UpsertWorkflowFromFile reads a single YAML file using Viper and upserts it to the DB.
func (o *Orchestrator) UpsertWorkflowFromFile(ctx context.Context, filePath string) error {
	v := viper.New()
	v.SetConfigFile(filePath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read workflow config: %w", err)
	}

	var wfDef WorkflowDef
	if err := v.Unmarshal(&wfDef); err != nil {
		return fmt.Errorf("unmarshal workflow: %w", err)
	}

	if wfDef.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	var mutated bool
	wfDef, mutated = normalizeWorkflowDef(wfDef)
	if mutated {
		o.logger.Info("Orchestrator: normalized workflow graph", "file", filepath.Base(filePath))
	}

	defBytes, err := json.Marshal(wfDef)
	if err != nil {
		return fmt.Errorf("marshal workflow definition: %w", err)
	}

	_, err = o.queries.UpsertWorkflowBlueprint(ctx, database.UpsertWorkflowBlueprintParams{
		Name:         wfDef.Name,
		TriggerTopic: wfDef.TriggerTopic,
		Definition:   defBytes,
	})
	if err != nil {
		return fmt.Errorf("upsert workflow blueprint: %w", err)
	}

	o.logger.Info("✅ Orchestrator: upserted workflow blueprint", "name", wfDef.Name, "steps", len(wfDef.Steps), "trigger", wfDef.TriggerTopic, "file", filepath.Base(filePath))
	return nil
}

// WatchWorkflows monitors the provided directory for YAML changes and hot-reloads them.
func (o *Orchestrator) WatchWorkflows(ctx context.Context, dirPath string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fsnotify watcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(dirPath); err != nil {
		return fmt.Errorf("add dir to watcher: %w", err)
	}

	o.logger.Info("👁️  Orchestrator: watching workflows directory for changes", "path", dirPath)

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// We only care about writes or creates of .yaml files
			if filepath.Ext(event.Name) != ".yaml" {
				continue
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				o.logger.Info("🔄 Workflow file change detected", "file", event.Name)
				// Defer a bit to let the write finish (debounce)
				time.Sleep(200 * time.Millisecond)

				if err := o.UpsertWorkflowFromFile(ctx, event.Name); err != nil {
					o.logger.Error("❌ Orchestrator: failed to hot-reload workflow", "file", event.Name, "error", err)
					continue
				}

				// After upserting to DB, refresh the in-memory blueprints
				if err := o.SyncBlueprints(ctx); err != nil {
					o.logger.Error("❌ Orchestrator: failed to sync blueprints after hot-reload", "error", err)
				} else {
					o.logger.Info("🚀 Orchestrator: hot-reload complete", "file", event.Name)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			o.logger.Error("Orchestrator: watcher error", "error", err)
		}
	}
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
		var mutated bool
		def, mutated = normalizeWorkflowDef(def)
		if mutated {
			defBytes, _ := json.Marshal(def)
			if _, err := o.queries.UpsertWorkflowBlueprint(ctx, database.UpsertWorkflowBlueprintParams{
				Name:         def.Name,
				TriggerTopic: def.TriggerTopic,
				Definition:   defBytes,
			}); err != nil {
				o.logger.Error("Orchestrator: failed to persist normalized blueprint", "name", def.Name, "error", err)
			}
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

	o.logger.Info("📋 Orchestrator: synced workflow blueprints from DB", "count", len(next), "trigger_topics", len(subjects))
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
		MaxDeliver:     orchestratorDeliverLimit,
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

func (o *Orchestrator) startResumeSubscription(ctx context.Context) error {
	sub, err := o.bus.QueueSubscribe(
		WorkflowResumeSubject,
		WorkflowResumeGroup,
		o.handleWorkflowResume,
		nats.Durable(WorkflowResumeDurable),
		nats.DeliverAll(),
		nats.AckExplicit(),
	)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to subscribe to resume subject: %w", err)
	}
	o.subs = append(o.subs, sub)
	o.logger.Info("Orchestrator: listening for resume signals", "subject", WorkflowResumeSubject)
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
		if errors.Is(err, errMessageDeadLettered) {
			return
		}
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

	if err := o.startResumeSubscription(ctx); err != nil {
		return fmt.Errorf("orchestrator: failed to subscribe to resume topic: %w", err)
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
// It creates a fresh WorkflowInstance and schedules the entry steps.
func (o *Orchestrator) handleTrigger(ctx context.Context, def WorkflowDef, msg *nats.Msg) error {
	if len(def.Steps) == 0 {
		return fmt.Errorf("workflow %q has no steps", def.Name)
	}

	if o.maybeDeadLetter(msg, WorkflowTriggerDLQSubject, "trigger exceeded max deliveries") {
		return errMessageDeadLettered
	}

	instanceID := uuid.New()
	o.logger.Info("Orchestrator: workflow triggered",
		"workflow", def.Name,
		"instance_id", instanceID.String(),
	)

	var entityID pgtype.UUID
	var triggerPayload []byte = msg.Data
	var triggerEnv core.Envelope
	if err := json.Unmarshal(msg.Data, &triggerEnv); err == nil {
		var taskDef core.TaskDefinition
		if err := json.Unmarshal(triggerEnv.Body, &taskDef); err == nil {
			triggerPayload = taskDef.Payload
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

	state := InstanceState{
		WorkflowDef:    def.Name,
		CurrentStepID:  def.Steps[0].ID,
		InstancePath:   []string{instanceID.String()},
		ActiveSteps:    make(map[string]bool),
		CompletedSteps: make(map[string]bool),
		Variables:      make(map[string]json.RawMessage),
	}

	arg := database.CreateOrGetWorkflowParams{
		ID:       pgtype.UUID{Bytes: instanceID, Valid: true},
		EntityID: entityID,
	}
	wf, err := o.queries.CreateOrGetWorkflow(ctx, arg)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to create workflow row: %w", err)
	}

	stateBytes, _ := json.Marshal(state)
	wf, err = o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
		ID:         wf.ID,
		State:      stateBytes,
		SequenceID: 0,
	})
	if err != nil {
		return fmt.Errorf("orchestrator: failed to update initial state: %w", err)
	}

	ready, err := o.scheduleReadySteps(ctx, def, &state, wf.EntityID, triggerPayload)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to dispatch initial steps: %w", err)
	}
	stateBytes, _ = json.Marshal(state)
	wf, err = o.persistWorkflowState(ctx, wf, state)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to persist state after scheduling: %w", err)
	}

	activeIDs := stepIDsFromList(ready)
	if len(activeIDs) == 0 {
		activeIDs = []string{def.Steps[0].ID}
	}

	o.logger.Info("Orchestrator: instance persisted", "id", instanceID.String())
	o.publishStatus(instanceID.String(), entityID, "started", strings.Join(activeIDs, ","), "", def, activeIDs)

	return nil
}

func (o *Orchestrator) dispatchStep(ctx context.Context, step WorkflowStep, instancePath []string, payload []byte) error {
	cid := buildConversationID(instancePath, step.ID)

	o.logger.Info("🚀 [DEBUG] Orchestrator dispatching step",
		"activity", step.ActivityType,
		"cid", cid,
	)
	if len(instancePath) == 0 {
		return fmt.Errorf("orchestrator: missing instance path for step %q", step.ID)
	}
	instanceID := instancePath[len(instancePath)-1]
	convID := buildConversationID(instancePath, step.ID)

	if step.Negotiate {
		queue, err := core.NormalizeTaskQueueWithComplexity(step.ActivityType, step.TaskQueue, step.Complexity)
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
			Payload:        json.RawMessage(payload),
			Complexity:     step.Complexity,
			WorkflowSchema: step.WorkflowSchema,
			SystemPrompt:   step.SystemPrompt,
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
			normalized, err := core.NormalizeTaskQueueWithComplexity(step.ActivityType, step.TaskQueue, step.Complexity)
			if err != nil {
				return fmt.Errorf("orchestrator: invalid worker queue for step %q: %w", step.ID, err)
			}
			inbox = normalized
			o.logger.Debug("Orchestrator: using worker inbox for dispatch", "inbox", inbox)
		} else if step.TaskQueue != "" {
			normalized, err := core.NormalizeTaskQueueWithComplexity(step.ActivityType, step.TaskQueue, step.Complexity)
			if err != nil {
				return fmt.Errorf("orchestrator: invalid task queue override for step %q: %w", step.ID, err)
			}
			inbox = normalized
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
			Complexity:     step.Complexity,
			WorkflowSchema: step.WorkflowSchema,
			SystemPrompt:   step.SystemPrompt,
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

func (o *Orchestrator) spawnSubWorkflow(ctx context.Context, step WorkflowStep, parentState *InstanceState, entityID pgtype.UUID, payload []byte) error {
	if step.SubWorkflow == "" {
		return fmt.Errorf("orchestrator: sub-workflow step %q missing sub_workflow reference", step.ID)
	}

	childDef, err := o.resolveBlueprintByName(ctx, step.SubWorkflow)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to resolve sub-workflow %q: %w", step.SubWorkflow, err)
	}

	childInstanceID := uuid.New()
	childPath := append(append([]string(nil), parentState.InstancePath...), childInstanceID.String())
	childState := InstanceState{
		WorkflowDef:    childDef.Name,
		CurrentStepID:  "",
		InstancePath:   childPath,
		ActiveSteps:    make(map[string]bool),
		CompletedSteps: make(map[string]bool),
		Variables:      make(map[string]json.RawMessage),
		ParentStepID:   step.ID,
	}
	if len(childDef.Steps) > 0 {
		childState.CurrentStepID = childDef.Steps[0].ID
	}

	arg := database.CreateOrGetWorkflowParams{
		ID:       pgtype.UUID{Bytes: childInstanceID, Valid: true},
		EntityID: entityID,
	}

	childWf, err := o.queries.CreateOrGetWorkflow(ctx, arg)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to create sub-workflow row: %w", err)
	}

	stateBytes, _ := json.Marshal(childState)
	childWf, err = o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
		ID:         childWf.ID,
		State:      stateBytes,
		SequenceID: 0,
	})
	if err != nil {
		return fmt.Errorf("orchestrator: failed to persist sub-workflow state: %w", err)
	}

	ready, err := o.scheduleReadySteps(ctx, childDef, &childState, childWf.EntityID, payload)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to dispatch sub-workflow %q: %w", childDef.Name, err)
	}

	if _, err := o.persistWorkflowState(ctx, childWf, childState); err != nil {
		return fmt.Errorf("orchestrator: failed to persist sub-workflow active steps: %w", err)
	}

	activeIDs := stepIDsFromList(ready)
	o.publishStatus(childInstanceID.String(), childWf.EntityID, "started", strings.Join(activeIDs, ","), "", childDef, activeIDs)
	o.logger.Info("Orchestrator: sub-workflow launched",
		"parent_step", step.ID,
		"child_workflow", childDef.Name,
		"child_instance", childInstanceID.String(),
	)
	return nil
}

// handleIncoming processes messages arriving at the OrchestratorInbox:
// - PROPOSE   → log bid; in the future, select best proposal and send ACCEPT
// - INFORM    → step completed; advance to DAG-friendly successors
func (o *Orchestrator) handleIncoming(msg *nats.Msg) {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		o.logger.Error("Orchestrator: malformed envelope on inbox", "error", err)
		o.emitDeadLetter(msg, WorkflowInboxDLQSubject, "malformed envelope", nil)
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
		o.logger.Info("📡 [DEBUG] Orchestrator received INFORM proof",
			"from", env.SenderDID,
			"cid", env.ConversationID,
			"data_length", len(env.Body),
		)
		instancePath, stepID, err := parseConversationID(env.ConversationID)
		if err != nil {
			o.logger.Error("Orchestrator: malformed conversation ID", "cid", env.ConversationID, "error", err)
			if o.maybeDeadLetter(msg, WorkflowInboxDLQSubject, "malformed conversation ID") {
				return
			}
			msg.Term()
			return
		}

		if len(instancePath) == 0 {
			o.logger.Error("Orchestrator: empty instance path in conversation ID", "cid", env.ConversationID)
			msg.Term()
			return
		}
		instanceIDStr := instancePath[len(instancePath)-1]
		instanceID, err := uuid.Parse(instanceIDStr)
		if err != nil {
			o.logger.Error("Orchestrator: invalid instance ID", "id", instanceIDStr, "error", err)
			if o.maybeDeadLetter(msg, WorkflowInboxDLQSubject, "invalid instance ID") {
				return
			}
			msg.Term()
			return
		}

		wf, err := o.queries.GetWorkflow(ctx, pgtype.UUID{Bytes: instanceID, Valid: true})
		if err != nil {
			o.logger.Error("Orchestrator: failed to fetch workflow", "id", instanceIDStr, "error", err)
			if o.maybeDeadLetter(msg, WorkflowInboxDLQSubject, "workflow not found") {
				return
			}
			msg.Nak()
			return
		}

		var state InstanceState
		if err := json.Unmarshal(wf.State, &state); err != nil {
			o.logger.Error("Orchestrator: failed to unmarshal state", "id", instanceIDStr, "error", err)
			o.emitDeadLetter(msg, WorkflowInboxDLQSubject, "state unmarshal failure", nil)
			msg.Term()
			return
		}
		ensureInstanceState(&state, instancePath)

		if !state.ActiveSteps[stepID] {
			if state.CompletedSteps[stepID] {
				o.logger.Debug("Orchestrator: ignoring duplicate proof", "step", stepID, "instance", instanceIDStr)
				msg.Ack()
				return
			}
			o.logger.Warn("Orchestrator: proof for inactive step", "step", stepID, "instance", instanceIDStr)
			msg.Ack()
			return
		}

		wfDef, err := o.resolveBlueprintByName(ctx, state.WorkflowDef)
		if err != nil {
			o.logger.Error("Orchestrator: workflow definition not found", "name", state.WorkflowDef, "error", err)
			o.emitDeadLetter(msg, WorkflowInboxDLQSubject, "blueprint missing", nil)
			msg.Term()
			return
		}

		if err := o.handleStepCompletion(ctx, wf, &state, wfDef, stepID, env.Body, env.SenderDID); err != nil {
			o.logger.Error("Orchestrator: failed to advance workflow", "instance", instanceIDStr, "error", err)
			if o.maybeDeadLetter(msg, WorkflowInboxDLQSubject, "dispatch failure") {
				return
			}
			msg.Nak()
			return
		}
		msg.Ack()

	case core.FAILURE:
		instancePath, stepID, err := parseConversationID(env.ConversationID)
		if err != nil {
			o.logger.Error("Orchestrator: malformed conversation ID in FAILURE", "cid", env.ConversationID, "error", err)
			if o.maybeDeadLetter(msg, WorkflowInboxDLQSubject, "malformed conversation ID") {
				return
			}
			msg.Term()
			return
		}

		if len(instancePath) == 0 {
			o.logger.Error("Orchestrator: empty instance path in conversation ID", "cid", env.ConversationID)
			msg.Term()
			return
		}
		instanceIDStr := instancePath[len(instancePath)-1]
		instanceID, err := uuid.Parse(instanceIDStr)
		if err != nil {
			o.logger.Error("Orchestrator: invalid instance ID", "id", instanceIDStr, "error", err)
			msg.Term()
			return
		}

		wf, err := o.queries.GetWorkflow(ctx, pgtype.UUID{Bytes: instanceID, Valid: true})
		if err != nil {
			o.logger.Error("Orchestrator: failed to fetch workflow for FAILURE", "id", instanceIDStr, "error", err)
			msg.Nak()
			return
		}

		var state InstanceState
		if err := json.Unmarshal(wf.State, &state); err != nil {
			o.logger.Error("Orchestrator: failed to unmarshal state for FAILURE", "id", instanceIDStr, "error", err)
			msg.Term()
			return
		}
		ensureInstanceState(&state, instancePath)

		if !state.ActiveSteps[stepID] {
			o.logger.Debug("Orchestrator: ignoring FAILURE for inactive step", "step", stepID, "instance", instanceIDStr)
			msg.Ack()
			return
		}

		// Resolve blueprint to get full context for the status message
		wfDef, err := o.resolveBlueprintByName(ctx, state.WorkflowDef)
		if err != nil {
			o.logger.Error("Orchestrator: blueprint missing for FAILURE case", "blueprint", state.WorkflowDef, "error", err)
			msg.Term()
			return
		}

		// Log and suspend the workflow
		o.logger.Error("Orchestrator: received FAILURE for step", "step", stepID, "instance", instanceIDStr, "body", string(env.Body))

		state.Suspended = true
		state.SuspensionStep = stepID
		state.SuspensionRoute = -1 // Arbitrary indicator for failure route

		// Attempt to extract the error string gracefully
		var payloadMap map[string]string
		failMsg := string(env.Body)
		if err := json.Unmarshal(env.Body, &payloadMap); err == nil && payloadMap["error"] != "" {
			failMsg = payloadMap["error"]
		}
		state.SuspensionReason = fmt.Sprintf("Agent failure in step '%s': %s", stepID, failMsg)

		// Persist the suspended state
		_, err = o.persistWorkflowState(ctx, wf, state)
		if err != nil {
			o.logger.Error("Orchestrator: failed to persist suspended state", "error", err)
			msg.Nak()
			return
		}

		// Broadcast suspension to external observers (UI, notifications)
		o.publishStatus(instanceIDStr, wf.EntityID, "suspended", stepID, env.SenderDID, wfDef, nil)

		msg.Ack()

	default:
		o.logger.Warn("Orchestrator: unexpected performative on inbox", "perf", env.Performative)
		msg.Ack()
	}
}

// handleWorkflowResume processes manual resume instructions that override a suspended route.
func (o *Orchestrator) handleWorkflowResume(msg *nats.Msg) {
	var req WorkflowResumeRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		o.logger.Error("Orchestrator: malformed resume payload", "error", err)
		msg.Term()
		return
	}

	if req.InstanceID == "" {
		o.logger.Warn("Orchestrator: resume payload missing instance_id")
		msg.Term()
		return
	}

	instanceID, err := uuid.Parse(req.InstanceID)
	if err != nil {
		o.logger.Error("Orchestrator: invalid instance ID in resume", "id", req.InstanceID, "error", err)
		msg.Term()
		return
	}

	ctx := context.Background()
	wf, err := o.queries.GetWorkflow(ctx, pgtype.UUID{Bytes: instanceID, Valid: true})
	if err != nil {
		o.logger.Error("Orchestrator: resume target not found", "id", req.InstanceID, "error", err)
		msg.Nak()
		return
	}

	var state InstanceState
	if err := json.Unmarshal(wf.State, &state); err != nil {
		o.logger.Error("Orchestrator: failed to unmarshal state during resume", "id", req.InstanceID, "error", err)
		msg.Term()
		return
	}
	ensureInstanceState(&state, state.InstancePath)

	if !state.Suspended {
		o.logger.Info("Orchestrator: ignore resume request, instance not suspended", "id", req.InstanceID)
		msg.Ack()
		return
	}

	stepID := req.StepID
	if stepID == "" {
		stepID = state.SuspensionStep
	}
	if stepID == "" {
		o.logger.Error("Orchestrator: resume request missing step_id", "id", req.InstanceID)
		msg.Term()
		return
	}

	payload := req.Payload
	if len(payload) == 0 {
		existing, ok := state.Variables[stepID]
		if !ok {
			existing = []byte("{}")
		}
		payload, _ = sjson.SetBytes(existing, "route", req.Route)
	}
	state.Variables[stepID] = payload
	state.Suspended = false
	state.SuspensionStep = ""
	state.SuspensionReason = ""
	state.SuspensionRoute = 0

	wfDef, err := o.resolveBlueprintByName(ctx, state.WorkflowDef)
	if err != nil {
		o.logger.Error("Orchestrator: resume target blueprint missing", "blueprint", state.WorkflowDef, "error", err)
		msg.Term()
		return
	}

	if _, err := o.persistWorkflowState(ctx, wf, state); err != nil {
		o.logger.Error("Orchestrator: failed to persist state after resume", "id", req.InstanceID, "error", err)
		msg.Nak()
		return
	}

	ready, err := o.scheduleReadySteps(ctx, wfDef, &state, wf.EntityID, payload)
	if err != nil {
		o.logger.Error("Orchestrator: resume scheduling failed", "id", req.InstanceID, "error", err)
		msg.Nak()
		return
	}

	if _, err := o.persistWorkflowState(ctx, wf, state); err != nil {
		o.logger.Error("Orchestrator: failed to persist resumed state", "id", req.InstanceID, "error", err)
		msg.Nak()
		return
	}

	nextIDs := []string{}
	if len(ready) > 0 {
		nextIDs = stepIDsFromList(ready)
	}
	o.publishStatus(req.InstanceID, wf.EntityID, "running", state.CurrentStepID, "", wfDef, nextIDs)
	msg.Ack()
}

func (o *Orchestrator) handleStepCompletion(ctx context.Context, wf database.ToroCoreWorkflow, state *InstanceState, wfDef WorkflowDef, stepID string, proof []byte, assignedDID string) error {
	if len(state.InstancePath) == 0 {
		return fmt.Errorf("orchestrator: missing instance path for step %q", stepID)
	}
	instanceIDStr := state.InstancePath[len(state.InstancePath)-1]

	delete(state.ActiveSteps, stepID)
	state.CompletedSteps[stepID] = true
	proofCopy := append(json.RawMessage(nil), proof...)
	state.Variables[stepID] = proofCopy
	state.LastProof = proofCopy
	state.CurrentStepID = stepID

	if _, err := o.persistWorkflowState(ctx, wf, *state); err != nil {
		return err
	}

	if len(state.CompletedSteps) == len(wfDef.Steps) {
		state.CurrentStepID = "COMPLETED"
		if _, err := o.persistWorkflowState(ctx, wf, *state); err != nil {
			return err
		}
		o.publishStatus(instanceIDStr, wf.EntityID, "completed", "COMPLETED", assignedDID, wfDef, nil)
		if state.ParentStepID != "" && len(state.InstancePath) > 1 {
			parentPath := state.InstancePath[:len(state.InstancePath)-1]
			if err := o.completeParentStep(ctx, parentPath, state.ParentStepID, proofCopy); err != nil {
				return err
			}
		}
		return nil
	}

	step, ok := findStepByID(wfDef, stepID)
	if !ok {
		return fmt.Errorf("orchestrator: blueprint step %q missing", stepID)
	}

	if suspended, route, reason := o.maybeSuspendInstance(step, state, proofCopy); suspended {
		if _, err := o.persistWorkflowState(ctx, wf, *state); err != nil {
			return err
		}
		o.publishStatus(instanceIDStr, wf.EntityID, "suspended", stepID, assignedDID, wfDef, nil)
		if err := o.publishAmbiguityEvent(instanceIDStr, wf.EntityID, wfDef, step, route, reason, proofCopy); err != nil {
			o.logger.Error("Orchestrator: failed to publish ambiguity event", "error", err, "instance", instanceIDStr)
		}
		return nil
	}

	ready, err := o.scheduleReadySteps(ctx, wfDef, state, wf.EntityID, proofCopy)
	if err != nil {
		return err
	}
	if len(ready) > 0 {
		if _, err := o.persistWorkflowState(ctx, wf, *state); err != nil {
			return err
		}
		nextIDs := stepIDsFromList(ready)
		o.publishStatus(instanceIDStr, wf.EntityID, "running", strings.Join(nextIDs, ","), assignedDID, wfDef, nextIDs)
	}
	return nil
}

func (o *Orchestrator) completeParentStep(ctx context.Context, parentPath []string, parentStepID string, proof []byte) error {
	if len(parentPath) == 0 {
		return fmt.Errorf("orchestrator: missing parent path for step %q", parentStepID)
	}
	parentID := parentPath[len(parentPath)-1]
	parentUUID, err := uuid.Parse(parentID)
	if err != nil {
		return fmt.Errorf("orchestrator: invalid parent instance ID %q: %w", parentID, err)
	}
	parentWf, err := o.queries.GetWorkflow(ctx, pgtype.UUID{Bytes: parentUUID, Valid: true})
	if err != nil {
		return fmt.Errorf("orchestrator: failed to fetch parent workflow %q: %w", parentID, err)
	}

	var parentState InstanceState
	if err := json.Unmarshal(parentWf.State, &parentState); err != nil {
		return fmt.Errorf("orchestrator: failed to unmarshal parent state %q: %w", parentID, err)
	}
	ensureInstanceState(&parentState, parentPath)

	if !parentState.ActiveSteps[parentStepID] {
		if parentState.CompletedSteps[parentStepID] {
			return nil
		}
		o.logger.Warn("Orchestrator: parent step not active for completion", "step", parentStepID, "instance", parentID)
		return nil
	}

	wfDef, err := o.resolveBlueprintByName(ctx, parentState.WorkflowDef)
	if err != nil {
		return fmt.Errorf("orchestrator: parent workflow definition %q missing: %w", parentState.WorkflowDef, err)
	}

	return o.handleStepCompletion(ctx, parentWf, &parentState, wfDef, parentStepID, proof, "")
}

// sanitize converts a workflow name into a NATS-safe durable consumer name.
func sanitize(s string) string {
	r := strings.NewReplacer(" ", "-", ".", "-", ":", "-", "/", "-")
	return strings.ToLower(r.Replace(s))
}

func (o *Orchestrator) publishStatus(instanceID string, entityID pgtype.UUID, status, stepID, assignedDID string, blueprint WorkflowDef, activeSteps []string) {
	evt := map[string]interface{}{
		"instance_id":     instanceID,
		"entity_id":       uuid.UUID(entityID.Bytes).String(),
		"status":          status,
		"current_step_id": stepID,
		"assigned_did":    assignedDID,
		"blueprint":       blueprint,
		"active_steps":    activeSteps,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)
	subject := fmt.Sprintf("workflow.events.%s", status)
	if err := o.bus.Publish(subject, data); err != nil {
		o.logger.Error("Orchestrator: failed to publish status event", "error", err)
	}
}

func (o *Orchestrator) publishAmbiguityEvent(instanceID string, entityID pgtype.UUID, blueprint WorkflowDef, step WorkflowStep, route int, reason string, proof json.RawMessage) error {
	entity := ""
	if entityID.Valid {
		entity = uuid.UUID(entityID.Bytes).String()
	}
	evt := map[string]interface{}{
		"instance_id":       instanceID,
		"entity_id":         entity,
		"status":            "ambiguous",
		"step_id":           step.ID,
		"route":             route,
		"suspension_reason": reason,
		"blueprint":         blueprint,
	}
	if len(proof) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(proof, &parsed); err == nil {
			evt["proof"] = parsed
		} else {
			evt["proof"] = string(proof)
		}
	}
	if rid := findRealmIDInProof(proof); rid != "" {
		evt["realm_id"] = rid
	}
	if session := findSessionIDInProof(proof); session != "" {
		evt["session_id"] = session
	}
	data, _ := json.Marshal(evt)
	return o.bus.Publish(WorkflowAmbiguousSubject, data)
}

func findRealmIDInProof(proof json.RawMessage) string {
	if len(proof) == 0 {
		return ""
	}
	var payload interface{}
	if err := json.Unmarshal(proof, &payload); err != nil {
		return ""
	}
	return searchForRealmID(payload)
}

func searchForRealmID(value interface{}) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"realm_id", "RealmID"} {
			if raw, ok := typed[key]; ok {
				if str, ok := raw.(string); ok && str != "" {
					return str
				}
			}
		}
		for _, v := range typed {
			if str := searchForRealmID(v); str != "" {
				return str
			}
		}
	case []interface{}:
		for _, v := range typed {
			if str := searchForRealmID(v); str != "" {
				return str
			}
		}
	}
	return ""
}

func findSessionIDInProof(proof json.RawMessage) string {
	if len(proof) == 0 {
		return ""
	}
	var payload interface{}
	if err := json.Unmarshal(proof, &payload); err != nil {
		return ""
	}
	return searchForSessionID(payload)
}

func searchForSessionID(value interface{}) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"session_id", "SessionID"} {
			if raw, ok := typed[key]; ok {
				if str, ok := raw.(string); ok && str != "" {
					return str
				}
			}
		}
		for _, child := range typed {
			if result := searchForSessionID(child); result != "" {
				return result
			}
		}
	case []interface{}:
		for _, item := range typed {
			if result := searchForSessionID(item); result != "" {
				return result
			}
		}
	}
	return ""
}

func stepIDsFromList(steps []WorkflowStep) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
}

func ensureInstanceState(state *InstanceState, path []string) {
	if state.ActiveSteps == nil {
		state.ActiveSteps = make(map[string]bool)
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = make(map[string]bool)
	}
	if state.Variables == nil {
		state.Variables = make(map[string]json.RawMessage)
	}
	if len(state.InstancePath) == 0 && len(path) > 0 {
		state.InstancePath = append([]string(nil), path...)
	}
}

func (o *Orchestrator) scheduleReadySteps(ctx context.Context, def WorkflowDef, state *InstanceState, entityID pgtype.UUID, fallback []byte) ([]WorkflowStep, error) {
	if state.Suspended {
		return nil, nil
	}
	ready := readySteps(def, *state)
	if len(ready) == 0 {
		return nil, nil
	}
	for _, step := range ready {
		payload := buildStepPayload(step, *state, fallback)
		var err error
		if step.SubWorkflow != "" {
			err = o.spawnSubWorkflow(ctx, step, state, entityID, payload)
		} else {
			err = o.dispatchStep(ctx, step, state.InstancePath, payload)
		}
		if err != nil {
			return nil, err
		}
		state.ActiveSteps[step.ID] = true
	}
	return ready, nil
}

func readySteps(def WorkflowDef, state InstanceState) []WorkflowStep {
	var ready []WorkflowStep
	for _, step := range def.Steps {
		if state.CompletedSteps[step.ID] || state.ActiveSteps[step.ID] {
			continue
		}
		missing := 0
		for _, dep := range step.DependsOn {
			if !state.CompletedSteps[dep] {
				missing++
			}
		}
		if missing == 0 && routeConditionMatches(step, state) {
			ready = append(ready, step)
		}
	}
	return ready
}

func routeConditionMatches(step WorkflowStep, state InstanceState) bool {
	if step.RouteCondition == nil || len(step.RouteCondition.Values) == 0 {
		return true
	}

	cond := step.RouteCondition
	target := cond.StepID
	if target == "" {
		if len(step.DependsOn) == 0 {
			return false
		}
		target = step.DependsOn[0]
	}

	raw, ok := state.Variables[target]
	if !ok {
		return false
	}
	route, ok := extractRouteValue(raw)
	if !ok {
		return false
	}
	for _, value := range cond.Values {
		if value == route {
			return true
		}
	}
	return false
}

func extractRouteValue(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	val := gjson.ParseBytes(raw)

	// Direct root lookup (raw Redux state)
	if route := val.Get("route"); route.Exists() {
		return int(route.Int()), true
	}

	// Protocol-wrapped lookup (FIPA Proof.Data)
	if route := val.Get("data.route"); route.Exists() {
		return int(route.Int()), true
	}

	// Legacy/Other envelope structure lookup
	if val.IsArray() {
		arr := val.Array()
		if len(arr) > 0 {
			first := arr[0]
			if route := first.Get("route"); route.Exists() {
				return int(route.Int()), true
			}
		}
	}

	return 0, false
}

func buildStepPayload(step WorkflowStep, state InstanceState, fallback []byte) []byte {
	var payload []byte

	// If history is requested, bundle all specified dependencies.
	if step.IncludeHistory && len(step.DependsOn) > 0 {
		bundle := make(map[string]json.RawMessage)
		for _, dep := range step.DependsOn {
			if raw, ok := state.Variables[dep]; ok {
				bundle[dep] = raw
			}
		}
		if len(bundle) > 0 {
			merged := map[string]map[string]json.RawMessage{"dependencies": bundle}
			payload, _ = json.Marshal(merged)
		}
	}

	// Default/Fallback: If no history was bundled (or not requested), use the direct input (LastProof).
	if len(payload) == 0 {
		if len(state.LastProof) > 0 {
			payload = state.LastProof
		} else if len(fallback) > 0 {
			payload = fallback
		} else {
			payload = []byte("{}")
		}
	}
	raw := wrapPayloadWithConfig(step, payload)
	proof := core.Proof{
		Type:      core.ProofAPI,
		Data:      raw,
		Timestamp: time.Now().Unix(),
	}
	res, _ := json.Marshal(proof)
	return res
}

func wrapPayloadWithConfig(step WorkflowStep, payload []byte) []byte {
	wrapper := map[string]json.RawMessage{
		"input": json.RawMessage(payload),
	}
	if len(step.Config) > 0 {
		configBytes, _ := json.Marshal(step.Config)
		wrapper["config"] = json.RawMessage(configBytes)
	}
	final, err := json.Marshal(wrapper)
	if err != nil {
		return payload
	}
	return final
}

func (o *Orchestrator) maybeSuspendInstance(step WorkflowStep, state *InstanceState, proof json.RawMessage) (bool, int, string) {
	if len(step.SuspendRoutes) == 0 {
		return false, 0, ""
	}
	route, ok := extractRouteValue(proof)
	if !ok {
		return false, 0, ""
	}
	for _, candidate := range step.SuspendRoutes {
		if route == candidate {
			state.Suspended = true
			state.SuspensionStep = step.ID
			state.SuspensionRoute = route
			reason := ""
			if step.SuspensionReasonPath != "" {
				if extracted := gjson.GetBytes(proof, step.SuspensionReasonPath).String(); extracted != "" {
					state.SuspensionReason = extracted
					reason = extracted
				}
			}
			return true, route, reason
		}
	}
	return false, 0, ""
}

func findStepByID(def WorkflowDef, stepID string) (WorkflowStep, bool) {
	for _, step := range def.Steps {
		if step.ID == stepID {
			return step, true
		}
	}
	return WorkflowStep{}, false
}

func (o *Orchestrator) persistWorkflowState(ctx context.Context, wf database.ToroCoreWorkflow, state InstanceState) (database.ToroCoreWorkflow, error) {
	stateBytes, _ := json.Marshal(state)
	return o.queries.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
		ID:         wf.ID,
		State:      stateBytes,
		SequenceID: wf.SequenceID + 1,
	})
}

func parseConversationID(convID string) ([]string, string, error) {
	parts := strings.Split(convID, "/")
	if len(parts) < 2 {
		return nil, "", fmt.Errorf("invalid conversation id %q", convID)
	}
	path := append([]string(nil), parts[:len(parts)-1]...)
	return path, parts[len(parts)-1], nil
}

func buildConversationID(path []string, stepID string) string {
	if len(path) == 0 {
		return stepID
	}
	return fmt.Sprintf("%s/%s", strings.Join(path, "/"), stepID)
}

// WorkflowResumeRequest defines the payload for manual resume messages.
type WorkflowResumeRequest struct {
	InstanceID string          `json:"instance_id"`
	StepID     string          `json:"step_id,omitempty"`
	Route      int             `json:"route"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

func (o *Orchestrator) maybeDeadLetter(msg *nats.Msg, dlqSubject, reason string) bool {
	md, err := msg.Metadata()
	if err != nil || md == nil {
		return false
	}
	if md.NumDelivered < orchestratorDeliverLimit {
		return false
	}
	o.emitDeadLetter(msg, dlqSubject, reason, md)
	return true
}

func (o *Orchestrator) emitDeadLetter(msg *nats.Msg, dlqSubject, reason string, md *nats.MsgMetadata) {
	payload := map[string]interface{}{
		"subject":   msg.Subject,
		"reason":    reason,
		"data":      base64.StdEncoding.EncodeToString(msg.Data),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if md != nil {
		payload["metadata"] = map[string]interface{}{
			"stream":            md.Stream,
			"consumer":          md.Consumer,
			"stream_sequence":   md.Sequence.Stream,
			"consumer_sequence": md.Sequence.Consumer,
			"num_delivered":     md.NumDelivered,
			"timestamp":         md.Timestamp,
		}
	}
	encoded, _ := json.Marshal(payload)
	if err := o.bus.Publish(dlqSubject, encoded); err != nil {
		o.logger.Error("Orchestrator: failed to publish to DLQ", "subject", dlqSubject, "error", err)
	}
	if err := msg.Term(); err != nil {
		o.logger.Error("Orchestrator: failed to terminate message after DLQ", "error", err)
	}
}

func normalizeWorkflowDef(def WorkflowDef) (WorkflowDef, bool) {
	mutated := false
	for i := range def.Steps {
		step := &def.Steps[i]
		if step.Complexity == 0 {
			step.Complexity = core.ComplexityEntry
			mutated = true
		}
		if step.ID == "" {
			step.ID = fmt.Sprintf("step-%d", i)
			mutated = true
		}
		if len(step.DependsOn) == 0 && i > 0 {
			prev := def.Steps[i-1].ID
			if prev != "" {
				step.DependsOn = []string{prev}
				mutated = true
			}
		}
	}
	return def, mutated
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
// keepaliveTick returns a timer string — utility for future heartbeat loop.
func keepaliveTick() string { return time.Now().Format(time.RFC3339) }

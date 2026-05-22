package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/tidwall/gjson"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewDelegatorWorker(
			deps.Queue,
			deps.Logger,
			deps.Config,
		)
	})
}

// DelegatorWorker is a generic array-chunking dispatcher.
//
// It receives a shaped payload from the Orchestrator, finds the first JSON array
// in that payload, and splits it into batches — each dispatched as a dynamic
// sub-workflow step. The worker is completely domain-agnostic: it does not know
// what the array contains, what table it came from, or which upstream worker
// produced it. The Orchestrator threads whatever the previous step placed in
// state.LastProof directly into this worker's payload.
type DelegatorWorker struct {
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
}

// NewDelegatorWorker creates a new generic delegation worker.
func NewDelegatorWorker(
	nc *nats.Conn,
	logger *slog.Logger,
	cfg *config.Config,
) (*DelegatorWorker, error) {
	return &DelegatorWorker{nc: nc, logger: logger, cfg: cfg}, nil
}

func (d *DelegatorWorker) Init(ctx context.Context) error {
	return nil
}

func (d *DelegatorWorker) Subscriptions() []SubscriptionConfig {
	if d.cfg == nil {
		d.logger.Error("delegation worker: missing config, cannot derive subject")
		return nil
	}

	_, workerCfg := d.cfg.Workers.GetForWorker(d)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		d.logger.Error("delegation worker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			d.logger.Error("delegation worker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
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

func (d *DelegatorWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	d.logger.Info("📡 [DEBUG] delegation_worker received message", "topic", msg.Subject, "data_length", len(msg.Data))

	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		d.logger.Error("delegation worker: bad envelope", "error", err)
		msg.Term()
		return nil
	}

	perfStr, ok := env["perf"].(string)
	perf := core.Performative(perfStr)
	if !ok || !core.IsValidPerformative(perf) || perf != core.REQUEST {
		d.logger.Warn("delegation worker: dropping message, invalid performative", "perf", perfStr)
		msg.Term()
		return nil
	}

	cid, hasCid := env["cid"].(string)
	if !hasCid || cid == "" {
		d.logger.Error("delegation worker: requires cid to delegate")
		msg.Term()
		return nil
	}

	// Read Orchestrator body
	bodyBytes, _ := json.Marshal(env["body"])
	var taskDef core.TaskDefinition
	if err := json.Unmarshal(bodyBytes, &taskDef); err != nil || len(taskDef.Payload) == 0 {
		d.logger.Error("delegation worker: failed to unmarshal TaskDefinition or payload empty")
		msg.Term()
		return nil
	}

	// Unwrap all Orchestrator/protocol layers to reach the shaped payload map.
	// core.UnmarshalTaskPayload handles Proof → input → TaskDefinition nesting.
	var shapedPayload map[string]interface{}
	if err := core.UnmarshalTaskPayload(taskDef.Payload, &shapedPayload); err != nil {
		d.logger.Error("delegation worker: payload is not a json object", "error", err)
		msg.Term()
		return nil
	}

	// Read step config (embedded by Orchestrator's wrapPayloadWithConfig)
	batchSize := 50
	targetSubWorkflow := ""

	payloadStr := string(taskDef.Payload)
	if bs := gjson.Get(payloadStr, "data.config.batch_size"); bs.Exists() {
		batchSize = int(bs.Int())
	}
	if tsw := gjson.Get(payloadStr, "data.config.target_sub_workflow"); tsw.Exists() {
		targetSubWorkflow = tsw.String()
	}

	if batchSize <= 0 || batchSize > workflows.MaxDynamicDelegationSteps {
		batchSize = workflows.MaxDynamicDelegationSteps
	}
	if targetSubWorkflow == "" {
		d.logger.Error("delegation worker: missing target_sub_workflow in config")
		msg.Term()
		return nil
	}

	// Find the first JSON array in the shaped payload — completely generic.
	// Whatever the upstream step sent in its proof body lands here.
	var targetArray []interface{}
	var arrayKey string
	for k, v := range shapedPayload {
		if arr, isArr := v.([]interface{}); isArr {
			targetArray = arr
			arrayKey = k
			break
		}
	}

	if len(targetArray) == 0 {
		d.logger.Info("delegation worker: no array found in shaped payload — emitting empty INFORM.",
			"payload_keys", mapKeys(shapedPayload),
		)
		d.emitInform(cid, map[string]interface{}{})
		msg.Ack()
		return nil
	}

	var steps []workflows.WorkflowStep
	var remainingArray []interface{}

	maxItemsToProcess := workflows.MaxDynamicDelegationSteps * batchSize
	itemsToProcess := targetArray

	if len(targetArray) > maxItemsToProcess {
		itemsToProcess = targetArray[:maxItemsToProcess]
		remainingArray = targetArray[maxItemsToProcess:]
		d.logger.Info("delegation worker: array too large, paginating",
			"total", len(targetArray),
			"processing", len(itemsToProcess),
			"remaining", len(remainingArray),
		)
	}

	d.logger.Info("delegation worker: chunking",
		"array_key", arrayKey,
		"total", len(itemsToProcess),
		"batch_size", batchSize,
		"sub_workflow", targetSubWorkflow,
	)

	payloads := make(map[string]json.RawMessage)

	for i := 0; i < len(itemsToProcess); i += batchSize {
		end := i + batchSize
		if end > len(itemsToProcess) {
			end = len(itemsToProcess)
		}
		chunk := itemsToProcess[i:end]

		// Each chunk payload contains the slice plus all scalar context fields
		// (e.g. session_id, realm_id) so sub-workflows have full context.
		stepPayload := map[string]interface{}{arrayKey: chunk}
		for k, v := range shapedPayload {
			if k != arrayKey {
				stepPayload[k] = v
			}
		}
		stepPayloadBytes, _ := json.Marshal(stepPayload)

		stepID := fmt.Sprintf("chunk_%d", len(steps)+1)
		steps = append(steps, workflows.WorkflowStep{
			ID:          stepID,
			SubWorkflow: targetSubWorkflow,
		})
		payloads[stepID] = json.RawMessage(stepPayloadBytes)
	}

	// Pagination: if we couldn't fit everything, chain another delegator step
	if len(remainingArray) > 0 {
		d.logger.Info("delegation worker: injecting pagination step")

		pagePayload := map[string]interface{}{arrayKey: remainingArray}
		for k, v := range shapedPayload {
			if k != arrayKey {
				pagePayload[k] = v
			}
		}
		pagePayloadBytes, _ := json.Marshal(pagePayload)

		stepID := "pagination_step"
		steps = append(steps, workflows.WorkflowStep{
			ID:           stepID,
			ActivityType: "workers.delegator",
			Config: map[string]interface{}{
				"batch_size":          batchSize,
				"target_sub_workflow": targetSubWorkflow,
			},
			DependsOn: getStepIDs(steps),
		})
		payloads[stepID] = json.RawMessage(pagePayloadBytes)
	}

	payloadsBytes, _ := json.Marshal(payloads)

	d.emitDelegate(cid, workflows.DelegationRequest{
		Steps:   steps,
		Payload: json.RawMessage(payloadsBytes),
	})
	msg.Ack()
	return nil
}

func getStepIDs(steps []workflows.WorkflowStep) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (d *DelegatorWorker) emitInform(cid string, data map[string]interface{}) {
	d.emitReply(cid, core.INFORM, map[string]interface{}{
		"type": "proof.delegation.complete",
		"data": data,
	})
}

func (d *DelegatorWorker) emitDelegate(cid string, req workflows.DelegationRequest) {
	d.emitReply(cid, core.DELEGATE, req)
}

func (d *DelegatorWorker) emitReply(cid string, perf core.Performative, body interface{}) {
	replyEnv := map[string]interface{}{
		"id":   uuid.New().String(),
		"ts":   time.Now().UTC(),
		"src":  "did:toro:delegation-worker",
		"dst":  workflows.OrchestratorDID,
		"perf": perf,
		"cid":  cid,
		"body": body,
		"sig":  "worker-sig",
	}
	replyBytes, _ := json.Marshal(replyEnv)

	if _, err := d.nc.Request(workflows.OrchestratorInbox, replyBytes, 5*time.Second); err != nil {
		d.logger.Error("delegation worker: failed to publish reply", "error", err)
	} else {
		d.logger.Info("delegation worker: sent reply back to Orchestrator", "cid", cid, "performative", perf)
	}
}

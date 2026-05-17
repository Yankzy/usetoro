package workers

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
	"github.com/tidwall/gjson"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewDirectionRouterWorker(
			deps.Store.Queries,
			deps.Queue,
			deps.Logger,
			deps.Config,
		)
	})
}

// DirectionRouterStore is the minimal database interface required by this worker.
type DirectionRouterStore interface {
	GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetSessionRows(ctx context.Context, arg database.GetSessionRowsParams) ([]database.GetSessionRowsRow, error)
	GetBankAccounts(ctx context.Context) ([]string, error)
	GetAccountsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error)
	GetVendorsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpVendor, error)
	GetCustomersByRealm(ctx context.Context, realmID string) ([]database.ShadowErpCustomer, error)
	GetCompanyInfo(ctx context.Context, realmID string) (database.ShadowErpCompanyInfo, error)
}

// DirectionRouterWorker splits a batch of transactions by cash direction
// (outflow vs inflow) using the session's outflow_is field, chunks each
// direction into batches, and delegates each direction to the Categorize
// Batch Workflow.
//
// It replaces the generic DelegatorWorker for the batch_categorize_transactions
// step so that each sub-workflow instance receives rows of only one direction
// (cash_direction=OUTFLOW or cash_direction=INFLOW).
type DirectionRouterWorker struct {
	store  DirectionRouterStore
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
}

func NewDirectionRouterWorker(
	store DirectionRouterStore,
	nc *nats.Conn,
	logger *slog.Logger,
	cfg *config.Config,
) (*DirectionRouterWorker, error) {
	return &DirectionRouterWorker{store: store, nc: nc, logger: logger, cfg: cfg}, nil
}

func (d *DirectionRouterWorker) Init(ctx context.Context) error {
	return nil
}

func (d *DirectionRouterWorker) Subscriptions() []SubscriptionConfig {
	if d.cfg == nil {
		d.logger.Error("direction router: missing config, cannot derive subject")
		return nil
	}

	_, workerCfg := d.cfg.Workers.GetForWorker(d)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		d.logger.Error("direction router: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			d.logger.Error("direction router: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (d *DirectionRouterWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	d.logger.Info("📡 [DEBUG] direction_router received message", "topic", msg.Subject, "data_length", len(msg.Data))

	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		d.logger.Error("direction router: bad envelope", "error", err)
		msg.Term()
		return nil
	}

	perfStr, ok := env["perf"].(string)
	perf := core.Performative(perfStr)
	if !ok || !core.IsValidPerformative(perf) || perf != core.REQUEST {
		d.logger.Warn("direction router: dropping message, invalid performative", "perf", perfStr)
		msg.Term()
		return nil
	}

	cid, hasCid := env["cid"].(string)
	if !hasCid || cid == "" {
		d.logger.Error("direction router: requires cid to delegate")
		msg.Term()
		return nil
	}

	// Read Orchestrator body
	bodyBytes, _ := json.Marshal(env["body"])
	var taskDef core.TaskDefinition
	if err := json.Unmarshal(bodyBytes, &taskDef); err != nil || len(taskDef.Payload) == 0 {
		d.logger.Error("direction router: failed to unmarshal TaskDefinition or payload empty")
		msg.Term()
		return nil
	}

	// Unwrap all Orchestrator/protocol layers to reach the shaped payload map.
	var shapedPayload map[string]interface{}
	if err := core.UnmarshalTaskPayload(taskDef.Payload, &shapedPayload); err != nil {
		d.logger.Error("direction router: payload is not a json object", "error", err)
		msg.Term()
		return nil
	}

	// Read step config
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
		d.logger.Error("direction router: missing target_sub_workflow in config")
		msg.Term()
		return nil
	}

	// Extract session_id to query outflow_is and unclassified rows.
	sessionIDRaw, hasSessionID := shapedPayload["session_id"]
	if !hasSessionID {
		d.logger.Error("direction router: no session_id in payload")
		msg.Term()
		return nil
	}
	sessionIDStr, ok := sessionIDRaw.(string)
	if !ok || sessionIDStr == "" {
		d.logger.Error("direction router: session_id is not a valid string")
		msg.Term()
		return nil
	}

	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(sessionIDStr); err != nil {
		d.logger.Error("direction router: invalid session_id UUID", "session_id", sessionIDStr, "error", err)
		msg.Term()
		return nil
	}

	// Fetch outflow_is from the session.
	session, err := d.store.GetCleanupSession(ctx, sessionUUID)
	if err != nil {
		d.logger.Error("direction router: failed to fetch session", "session_id", sessionIDStr, "error", err)
		msg.Term()
		return nil
	}
	outflowIs := session.OutflowIs
	d.logger.Info("direction router: session outflow_is", "session_id", sessionIDStr, "outflow_is", outflowIs)

	// Query unclassified rows from the DB instead of extracting them from the
	// NATS payload. The rule_evaluation worker returns only session metadata.
	rawRows, err := d.store.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: sessionUUID,
		Status:    pgtype.Text{String: "ENRICHED", Valid: true},
	})
	if err != nil {
		d.logger.Error("direction router: failed to fetch session rows", "error", err)
		msg.Term()
		return nil
	}
	targetArray := make([]interface{}, 0, len(rawRows))
	for _, r := range rawRows {
		rowBytes, _ := json.Marshal(r)
		var rowMap map[string]interface{}
		if json.Unmarshal(rowBytes, &rowMap) == nil {
			targetArray = append(targetArray, rowMap)
		}
	}
	d.logger.Info("direction router: fetched session rows from DB", "count", len(targetArray))

	if len(targetArray) == 0 {
		d.logger.Info("direction router: no unclassified rows — emitting empty INFORM.")
		d.emitInform(cid, map[string]interface{}{})
		msg.Ack()
		return nil
	}

	// Fetch categorization context (bank accounts, chart of accounts, vendors,
	// customers, company info) so the categorize batch sub-workflow has real
	// values to render into its {placeholders}.
	realmIDForCtx := session.RealmID.String
	injectCategorizationContext(ctx, d.store, realmIDForCtx, shapedPayload, d.logger)

	// Split rows by direction using outflow_is sign convention.
	const arrayKey = "rows"
	outflowRows, inflowRows := splitByDirection(targetArray, outflowIs, d.logger)
	d.logger.Info("direction router: split rows",
		"total", len(targetArray),
		"outflows", len(outflowRows),
		"inflows", len(inflowRows),
	)

	// Build scalar context (everything except the array).
	scalarCtx := make(map[string]interface{})
	for k, v := range shapedPayload {
		if k != arrayKey {
			scalarCtx[k] = v
		}
	}

	var steps []workflows.WorkflowStep

	// Chunk outflows and create steps.
	steps = append(steps, createDirectionChunks(outflowRows, "OUTFLOW", arrayKey, scalarCtx, batchSize, targetSubWorkflow)...)
	// Chunk inflows and create steps.
	steps = append(steps, createDirectionChunks(inflowRows, "INFLOW", arrayKey, scalarCtx, batchSize, targetSubWorkflow)...)

	if len(steps) == 0 {
		d.logger.Info("direction router: no rows after split — emitting empty INFORM.")
		d.emitInform(cid, map[string]interface{}{})
		msg.Ack()
		return nil
	}

	d.emitDelegate(cid, workflows.DelegationRequest{
		Steps:   steps,
		Payload: []byte(`{}`),
	})
	msg.Ack()
	return nil
}

// splitByDirection divides rows into outflows and inflows using the session's
// outflow_is sign convention:
//
//	outflow_is = 'NEGATIVE': amount < 0 → outflow, amount > 0 → inflow
//	outflow_is = 'POSITIVE': amount > 0 → outflow, amount < 0 → inflow
//
// Rows with unparseable or zero amounts are skipped with a warning.
func splitByDirection(rows []interface{}, outflowIs string, logger *slog.Logger) (outflows, inflows []interface{}) {
	for i, row := range rows {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			logger.Warn("direction router: skipping non-map row in split", "index", i)
			continue
		}

		amountRaw, ok := rowMap["raw_amount"]
		if !ok {
			amountRaw = rowMap["amount"]
		}
		if amountRaw == nil {
			logger.Warn("direction router: row missing amount field, skipping", "index", i, "id", rowMap["id"])
			continue
		}

		amountStr, ok := amountRaw.(string)
		if !ok {
			logger.Warn("direction router: amount is not a string, skipping", "index", i, "id", rowMap["id"], "type", fmt.Sprintf("%T", amountRaw))
			continue
		}

		amountStr = strings.ReplaceAll(amountStr, ",", "")
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			logger.Warn("direction router: unparseable amount, skipping", "index", i, "id", rowMap["id"], "raw", amountStr, "error", err)
			continue
		}

		if amount == 0 {
			logger.Warn("direction router: zero-amount row cannot be classified by direction, skipping", "index", i, "id", rowMap["id"])
			continue
		}

		isOutflow := false
		if outflowIs == "" || outflowIs == "NEGATIVE" {
			isOutflow = amount < 0
		} else {
			isOutflow = amount > 0
		}

		if isOutflow {
			outflows = append(outflows, row)
		} else {
			inflows = append(inflows, row)
		}
	}
	return
}

// injectCategorizationContext fetches realm-level context from the database and
// injects it into the payload so the categorize batch sub-workflow's system_prompt
// {placeholders} resolve to real values instead of being passed through literally.
func injectCategorizationContext(ctx context.Context, store DirectionRouterStore, realmID string, payload map[string]interface{}, logger *slog.Logger) {
	if realmID == "" {
		logger.Warn("direction router: empty realm_id, skipping context injection")
		return
	}

	// 1. Bank accounts — used for internal transfer detection.
	if bankAccounts, err := store.GetBankAccounts(ctx); err != nil {
		logger.Warn("direction router: failed to fetch bank accounts for context", "error", err)
	} else {
		payload["json_list_of_bank_accounts"] = bankAccounts
	}

	// 2. Chart of accounts — used for account selection.
	if accounts, err := store.GetAccountsByRealm(ctx, realmID); err != nil {
		logger.Warn("direction router: failed to fetch accounts for context", "error", err)
	} else {
		// Simplify to what the AI needs: erp_id, name, account_type, classification.
		simplified := make([]map[string]interface{}, 0, len(accounts))
		for _, a := range accounts {
			simplified = append(simplified, map[string]interface{}{
				"erp_id":         a.ErpID,
				"name":           a.Name,
				"account_type":   a.AccountType,
				"classification": a.Classification,
			})
		}
		payload["json_list_of_all_filtered_accounts"] = simplified
	}

	// 3. Vendors and customers — used for entity matching.
	if vendors, err := store.GetVendorsByRealm(ctx, realmID); err != nil {
		logger.Warn("direction router: failed to fetch vendors for context", "error", err)
	} else {
		customers, custErr := store.GetCustomersByRealm(ctx, realmID)
		if custErr != nil {
			logger.Warn("direction router: failed to fetch customers for context", "error", custErr)
		}
		combined := make([]map[string]interface{}, 0, len(vendors)+len(customers))
		for _, v := range vendors {
			combined = append(combined, map[string]interface{}{
				"id":   v.ErpID,
				"name": v.DisplayName,
				"type": "vendor",
			})
		}
		for _, c := range customers {
			combined = append(combined, map[string]interface{}{
				"id":   c.ErpID,
				"name": c.DisplayName,
				"type": "customer",
			})
		}
		payload["json_list_of_existing_vendors_or_customers_with_ids"] = combined
	}

	// 4. Company info — used for industry-aware classification.
	if company, err := store.GetCompanyInfo(ctx, realmID); err != nil {
		logger.Warn("direction router: failed to fetch company info for context", "error", err)
	} else {
		parts := make([]string, 0, 3)
		if company.Industry.Valid && company.Industry.String != "" {
			parts = append(parts, company.Industry.String)
		}
		if company.BusinessModel.Valid && company.BusinessModel.String != "" {
			parts = append(parts, company.BusinessModel.String)
		}
		if company.CompanyName != "" {
			parts = append(parts, company.CompanyName)
		}
		payload["company_industry_description"] = strings.Join(parts, " — ")
	}
}

// createDirectionChunks splits a direction's rows into batches and creates
// WorkflowStep entries. Each step's context includes cash_direction so the
// categorize workflow knows which classification rules to apply.
func createDirectionChunks(
	rows []interface{},
	cashDirection string,
	arrayKey string,
	scalarCtx map[string]interface{},
	batchSize int,
	targetSubWorkflow string,
) []workflows.WorkflowStep {
	var steps []workflows.WorkflowStep
	if len(rows) == 0 {
		return steps
	}

	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		stepPayload := map[string]interface{}{arrayKey: chunk}
		for k, v := range scalarCtx {
			stepPayload[k] = v
		}
		stepPayload["cash_direction"] = cashDirection
		stepPayloadBytes, _ := json.Marshal(stepPayload)

		steps = append(steps, workflows.WorkflowStep{
			ID:          fmt.Sprintf("%s_chunk_%d", strings.ToLower(cashDirection), len(steps)+1),
			SubWorkflow: targetSubWorkflow,
			Config: map[string]interface{}{
				"batch_payload": json.RawMessage(stepPayloadBytes),
			},
		})
	}
	return steps
}

func (d *DirectionRouterWorker) emitInform(cid string, data map[string]interface{}) {
	d.emitReply(cid, core.INFORM, map[string]interface{}{
		"type": "proof.delegation.complete",
		"data": data,
	})
}

func (d *DirectionRouterWorker) emitDelegate(cid string, req workflows.DelegationRequest) {
	d.emitReply(cid, core.DELEGATE, req)
}

func (d *DirectionRouterWorker) emitReply(cid string, perf core.Performative, body interface{}) {
	replyEnv := map[string]interface{}{
		"id":   uuid.New().String(),
		"ts":   time.Now().UTC(),
		"src":  "did:toro:direction-router",
		"dst":  workflows.OrchestratorDID,
		"perf": perf,
		"cid":  cid,
		"body": body,
		"sig":  "worker-sig",
	}
	replyBytes, _ := json.Marshal(replyEnv)

	js, err := d.nc.JetStream()
	if err != nil {
		d.logger.Error("direction router: failed to get JetStream context", "error", err)
		return
	}
	if _, err := js.Publish(workflows.OrchestratorInbox, replyBytes); err != nil {
		d.logger.Error("direction router: failed to publish reply", "error", err)
	} else {
		d.logger.Info("direction router: sent reply back to Orchestrator", "cid", cid, "performative", perf)
	}
}

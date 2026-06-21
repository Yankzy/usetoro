package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/tidwall/gjson"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// The classification_stage_worker.go file contains the core logic for the AI-driven
// categorization pipeline (Fignode). It dynamically processes varying stages of financial
// transaction classification (e.g., macro class, account type, entity matching) by querying
// unclassified transactions, preparing contextual prompts, and orchestrating categorization tasks
// to LLM agents via NATS messaging.

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewClassificationStageWorker(
			deps.Store.Queries,
			deps.DBPool,
			deps.Queue,
			deps.Logger,
			deps.Config,
		)
	})
}

// StageConfig is read from the workflow step's config block (YAML).
// It defines the specific behavior for the current classification stage.
type StageConfig struct {
	Stage            string `json:"stage" mapstructure:"stage" desc:"The name of the stage (e.g., 'macro_class', 'account_type')"`
	Model            string `json:"model" mapstructure:"model" desc:"The specific LLM model to use for this stage (e.g., 'gpt-4o')"`
	GroupBy          string `json:"group_by" mapstructure:"group_by" desc:"How to group rows for batching: 'direction', 'macro_class', or 'none'"`
	DBFilter         string `json:"db_filter" mapstructure:"db_filter" desc:"SQL WHERE clause filter to select rows eligible for this stage"`
	DBWriteColumn    string `json:"db_write_column" mapstructure:"db_write_column" desc:"The primary database column to write the LLM's classification result to"`
	DBWriteReasoning bool   `json:"db_write_reasoning" mapstructure:"db_write_reasoning" desc:"Whether to persist the LLM's reasoning to the ai_reasoning column"`
	SystemPromptTmpl string `json:"system_prompt_template" mapstructure:"system_prompt_template" desc:"The template string for the LLM system prompt"`
	InjectContext    bool   `json:"inject_context" mapstructure:"inject_context" desc:"Whether to load and inject Realm-level context (Accounts, Vendors, etc.)"`
	BatchSize        int    `json:"batch_size" mapstructure:"batch_size" desc:"Maximum number of transactions to send to the LLM in a single batch"`
}

// ClassificationStageWorkerPayload defines the expected JSON payload for LLM tool invocation.
type ClassificationStageWorkerPayload struct {
	SessionID string `json:"session_id" desc:"The ID of the cleanup session to classify"`
	Data      struct {
		Config StageConfig `json:"config" desc:"Configuration for the classification stage to run"`
	} `json:"data" desc:"Wrapper object containing the stage configuration"`
}

// ToolName returns the unique LLM tool name for this worker.
func (w *ClassificationStageWorker) ToolName() string {
	return "TriggerClassificationStage"
}

// ToolDescription provides the context for the LLM.
func (w *ClassificationStageWorker) ToolDescription() string {
	return "Triggers a specific AI classification stage (e.g., macro_class, account_type, entity matching) for a cleanup session."
}

// PayloadStruct returns a typed instance to automatically generate a JSON schema.
func (w *ClassificationStageWorker) PayloadStruct() any {
	return ClassificationStageWorkerPayload{}
}

// ClassificationStageStore is the minimal database interface required by this worker.
// It abstracts away direct dependencies on the sqlc queries package for testing and flexibility.
type ClassificationStageStore interface {
	GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetBankAccounts(ctx context.Context) ([]string, error)
	GetBankAccountName(ctx context.Context, id pgtype.UUID) (string, error)
	GetAccountsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error)
	GetVendorsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpVendor, error)
	GetCustomersByRealm(ctx context.Context, realmID string) ([]database.ShadowErpCustomer, error)
	GetCompanyInfo(ctx context.Context, realmID string) (database.ShadowErpCompanyInfo, error)
	UpdateStagingTransactionCashDirection(ctx context.Context, arg database.UpdateStagingTransactionCashDirectionParams) error
	GetVendorByERPID(ctx context.Context, arg database.GetVendorByERPIDParams) (database.ShadowErpVendor, error)
	GetCustomerByERPID(ctx context.Context, arg database.GetCustomerByERPIDParams) (database.ShadowErpCustomer, error)
	GetAccountByERPID(ctx context.Context, arg database.GetAccountByERPIDParams) (database.ShadowErpAccount, error)
}

// The rules are now imported from the ase package to ensure the ASE and batch
// classification workers use the exact same logic.

// ClassificationStageWorker is a configurable worker that handles any
// classification stage (macro_class, account_type, entity, account selection).
// Each stage is defined by its workflow YAML config block. It manages fetching
// data, preparing LLM batches, dispatching to agents, and writing results.
type ClassificationStageWorker struct {
	store  ClassificationStageStore // Interface for database operations
	pool   *pgxpool.Pool            // Database connection pool for complex queries
	nc     *nats.Conn               // NATS connection for messaging
	logger *slog.Logger             // Structured logger
	cfg    *config.Config           // Application configuration
}

// NewClassificationStageWorker creates a new instance of the worker.
func NewClassificationStageWorker(
	store ClassificationStageStore,
	pool *pgxpool.Pool,
	nc *nats.Conn,
	logger *slog.Logger,
	cfg *config.Config,
) (*ClassificationStageWorker, error) {
	return &ClassificationStageWorker{store: store, pool: pool, nc: nc, logger: logger, cfg: cfg}, nil
}

// buildOutflowRules generates a prioritized list of rules for categorizing OUTFLOW
// transactions (money leaving the business). It optionally injects hints about known
// entities or intra-file duplicates to guide the LLM.
func buildOutflowRules(dbHints []string, intraFileHints []string) string {
	rules := `Evaluate each description against these rules sequentially. Stop at the FIRST match.

1. INTERNAL TRANSFER (TRANSFER): Indicates money moving between the company's own bank accounts or paying off a credit card (e.g., "Transfer to Savings", "Amex Payment", matching a known bank account/credit card).
2. EQUITY: Explicitly indicates owner movement ("Draw", "Transfer to Owner").
3. LIABILITY (HIGHEST PRIORITY DEBT): Contains debt markers ("Loan", "SBA") OR matches a Known Liability Account. (Do NOT use this for paying off the company's own credit card; use Rule 1).
4. CUSTOMER REFUND / CHARGEBACK (REVENUE): Money is being returned to a customer or a transaction is disputed. Matches if: (a) description contains refund/chargeback/return keywords ("Refund", "Chargeback", "Return", "Dispute", "Reversal"), OR (b) description matches a known customer name from the database.`

	if len(dbHints) > 0 {
		rules += "\nKnown Revenue Customers Identified in this Batch: " + strings.Join(dbHints, ", ") + ". If a description matches an entity in this list, you MUST categorize it as a contra-REVENUE, never Expense."
	}
	if len(intraFileHints) > 0 {
		rules += "\nStrings also found in INFLOW transactions (same file) — do NOT use these alone for rule 4: " + strings.Join(intraFileHints, ", ") + "."
	}
	rules += ` STOP here — this is NOT an expense.
5. ASSET: Purchase of physical equipment/vehicles > $2,500.
6. EXPENSE: Everything else that bypassed Rules 1-5.`
	return rules
}

// buildInflowRules generates a prioritized list of rules for categorizing INFLOW
// transactions (money entering the business). Similar to outflow rules, it uses
// hints to override default behavior.
func buildInflowRules(dbHints []string, intraFileHints []string) string {
	rules := `Evaluate each description against these rules sequentially. Stop at the FIRST match.

1. INTERNAL TRANSFER (TRANSFER): Money moving between own bank accounts or receiving payment to a credit card.
2. EQUITY (OWNER INVESTMENT): Owner putting personal money into the business.
3. LIABILITY (LOAN PROCEEDS): Business receiving loan funds/cash advance ("SBA Proceeds", "Fundbox").
4. VENDOR REFUND (EXPENSE): Business is receiving money back from a previous purchase. Matches if: (a) description contains refund/cashback/reversal keywords ("Refund", "Cashback", "Reversal", "Credit"), OR (b) description matches a known vendor name from the database.`

	if len(dbHints) > 0 {
		rules += "\nKnown Expense Vendors Identified in this Batch: " + strings.Join(dbHints, ", ") + ". If a description matches an entity in this list, you MUST categorize it as a contra-EXPENSE, never Revenue."
	}
	if len(intraFileHints) > 0 {
		rules += "\nStrings also found in OUTFLOW transactions (same file) — do NOT use these alone for rule 4: " + strings.Join(intraFileHints, ", ") + "."
	}
	rules += ` STOP here — this is NOT revenue.
5. ASSET SALE (ASSET): Explicit sale of a large physical asset/vehicle.
6. REVENUE (DEFAULT REVENUE): Everything else (standard revenue, deposits, Stripe payouts).`
	return rules
}

// findEntityHints scans row descriptions for matches against a list of known DB entity names
// (vendors or customers). It returns a deduplicated list of matched names to be injected
// into the LLM prompt as hints.
func findEntityHints(rows []map[string]interface{}, names []string) []string {
	var hits []string
	seen := make(map[string]bool)
	for _, row := range rows {
		desc, _ := row["raw_description"].(string)
		if desc == "" {
			continue
		}
		descLower := strings.ToLower(desc)
		for _, name := range names {
			if seen[name] {
				continue
			}
			if strings.Contains(descLower, strings.ToLower(name)) {
				hits = append(hits, name)
				seen[name] = true
			}
		}
	}
	return hits
}

// findIntraFileHints finds words from opposing-direction descriptions that appear
// in the current batch's descriptions but are NOT already known DB entities.
// These are net-new intra-file signals — directional context only, not decisive.
// It uses a stopword list to filter out common transactional terms.
func findIntraFileHints(rows []map[string]interface{}, opposingDescs []string, knownNames []string) []string {
	stopWords := map[string]bool{"payment": true, "deposit": true, "transfer": true, "credit": true, "debit": true, "check": true, "withdrawal": true, "purchase": true, "fee": true, "interest": true}

	// Build set of known names (lowercased) for exclusion.
	knownSet := make(map[string]bool)
	for _, n := range knownNames {
		knownSet[strings.ToLower(n)] = true
	}

	var hits []string
	seen := make(map[string]bool)
	for _, row := range rows {
		desc, _ := row["raw_description"].(string)
		if desc == "" {
			continue
		}
		descLower := strings.ToLower(desc)
		for _, oppDesc := range opposingDescs {
			if seen[oppDesc] {
				continue
			}
			oppLower := strings.ToLower(oppDesc)
			if len(oppLower) < 4 || stopWords[oppLower] {
				continue
			}
			// Split opposing description into words; check each against this row.
			words := strings.Fields(oppLower)
			for _, w := range words {
				if len(w) < 4 || stopWords[w] || knownSet[w] {
					continue
				}
				if strings.Contains(descLower, w) {
					hits = append(hits, oppDesc)
					seen[oppDesc] = true
					break
				}
			}
		}
	}
	return hits
}

// extractDescs collects raw_description strings from a list of row maps.
// It is primarily used to prepare lists of descriptions for cross-direction hinting.
func extractDescs(rows []map[string]interface{}) []string {
	descs := make([]string, 0, len(rows))
	for _, r := range rows {
		if d, ok := r["raw_description"].(string); ok && d != "" {
			descs = append(descs, d)
		}
	}
	return descs
}

// Init is called once when the worker starts. It can be used for any required
// initialization. Currently, it is a no-op for this worker.
func (w *ClassificationStageWorker) Init(ctx context.Context) error { return nil }

// Subscriptions defines the NATS JetStream subjects this worker listens to.
// It reads from the worker's configuration block in the global config.
func (w *ClassificationStageWorker) Subscriptions() []SubscriptionConfig {
	if w.cfg == nil {
		w.logger.Error("classification_stage: missing config")
		return nil
	}
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("classification_stage: no activity_type configured")
		return nil
	}
	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("classification_stage: failed to derive inbox", "error", err)
			return nil
		}
	}
	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}
	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{nats.Durable(durableFromSubject(subject)), nats.DeliverAll(), nats.AckExplicit()},
	}}
}

// Handle processes incoming NATS messages (CFPs/Requests) for classification tasks.
// It coordinates data fetching, context injection, batch grouping, parallel dispatching
// to LLM agents, and ultimately writing the resolved classifications back to the database.
func (w *ClassificationStageWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("classification_stage: received message", "topic", msg.Subject)

	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error("classification_stage: bad envelope", "error", err)
		msg.Term()
		return nil
	}

	perfStr, _ := env["perf"].(string)
	perf := core.Performative(perfStr)
	if !core.IsValidPerformative(perf) || perf != core.REQUEST {
		msg.Term()
		return nil
	}

	cid, _ := env["cid"].(string)
	if cid == "" {
		w.logger.Error("classification_stage: requires cid")
		msg.Term()
		return nil
	}

	bodyBytes, _ := json.Marshal(env["body"])
	var taskDef core.TaskDefinition
	if err := json.Unmarshal(bodyBytes, &taskDef); err != nil || len(taskDef.Payload) == 0 {
		w.logger.Error("classification_stage: failed to unmarshal TaskDefinition")
		msg.Term()
		return nil
	}

	// Read stage config from the proof's data.config.
	payloadStr := string(taskDef.Payload)
	var stageCfg StageConfig
	if cfgRaw := gjson.Get(payloadStr, "data.config"); cfgRaw.Exists() {
		if err := json.Unmarshal([]byte(cfgRaw.Raw), &stageCfg); err != nil {
			w.logger.Error("classification_stage: failed to parse stage config", "error", err)
			msg.Term()
			return nil
		}
	}
	if stageCfg.Stage == "" {
		w.logger.Error("classification_stage: missing stage in config")
		msg.Term()
		return nil
	}

	// Unwrap the input payload for session_id etc.
	var shapedPayload map[string]interface{}
	if err := core.UnmarshalTaskPayload(taskDef.Payload, &shapedPayload); err != nil {
		w.logger.Error("classification_stage: payload is not a json object", "error", err)
		msg.Term()
		return nil
	}

	sessionIDRaw, ok := shapedPayload["session_id"]
	if !ok {
		w.logger.Error("classification_stage: no session_id in payload")
		msg.Term()
		return nil
	}
	sessionIDStr, _ := sessionIDRaw.(string)
	if sessionIDStr == "" {
		w.logger.Error("classification_stage: session_id is not a valid string")
		msg.Term()
		return nil
	}
	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(sessionIDStr); err != nil {
		w.logger.Error("classification_stage: invalid session_id UUID", "error", err)
		msg.Term()
		return nil
	}

	// Fetch session for outflow_is (needed for direction grouping).
	session, err := w.store.GetCleanupSession(ctx, sessionUUID)
	if err != nil {
		w.logger.Error("classification_stage: failed to fetch session", "error", err)
		msg.Term()
		return nil
	}
	outflowIs := session.OutflowIs
	realmID := session.RealmID.String

	// Query unmatched rows using the stage's db_filter.
	rows, err := w.queryRows(ctx, sessionUUID, stageCfg.DBFilter)
	if err != nil {
		w.logger.Error("classification_stage: failed to query rows", "error", err)
		msg.Term()
		return nil
	}
	if len(rows) == 0 {
		w.logger.Info("classification_stage: no rows match filter, emitting empty INFORM", "stage", stageCfg.Stage)
		w.emitInform(cid, map[string]interface{}{"stage": stageCfg.Stage, "written": 0})
		msg.Ack()
		return nil
	}
	w.logger.Info("classification_stage: fetched rows", "stage", stageCfg.Stage, "count", len(rows))

	// Inject categorization context if this stage needs it (COA, bank accounts, etc.).
	var ctxMap map[string]interface{}
	if stageCfg.InjectContext && realmID != "" {
		ctxMap = make(map[string]interface{})
		w.injectContext(ctx, realmID, ctxMap)
	}

	if stageCfg.Stage == "macro_class" {
		w.resolveIntraFileTwins(ctx, rows, session)
	}

	// Group rows.
	groups := w.groupRows(rows, stageCfg.GroupBy, outflowIs)
	w.logger.Info("classification_stage: grouped rows", "stage", stageCfg.Stage, "groups", len(groups))

	// Extract opposing direction descriptions for cross-hinting within the session.
	if stageCfg.GroupBy == "direction" {
		ctxMap["opposing_descs_outflow"] = extractDescs(groups["INFLOW"])
		ctxMap["opposing_descs_inflow"] = extractDescs(groups["OUTFLOW"])
	}

	// Find agent task queue.
	agentTaskQueue := "tasks.accounting.1.batch_categorization"
	for _, a := range w.cfg.Agents {
		if a.ActivityType == "agents.accounting.batch_categorization" && a.TaskQueue != "" {
			agentTaskQueue = a.TaskQueue
			break
		}
	}

	// Dispatch parallel agent calls per group, chunked into batch_size sub-batches.
	batchSize := stageCfg.BatchSize
	if batchSize <= 0 {
		batchSize = 20
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allPatches []map[string]interface{}

	for groupKey, groupRows := range groups {
		for i := 0; i < len(groupRows); i += batchSize {
			end := i + batchSize
			if end > len(groupRows) {
				end = len(groupRows)
			}
			chunk := groupRows[i:end]
			// Single-option class? Skip LLM, assign directly.
			if opts, ok := ase.AccountTypeOptions[groupKey]; ok && !strings.Contains(opts, ",") {
				wg.Add(1)
				go func(gk string, chunkRows []map[string]interface{}) {
					defer wg.Done()
					var patches []map[string]interface{}
					for _, row := range chunkRows {
						patches = append(patches, map[string]interface{}{
							"id":           row["id"],
							"account_type": opts,
							"reasoning":    "Single account type for macro class " + gk,
						})
					}
					mu.Lock()
					allPatches = append(allPatches, patches...)
					mu.Unlock()
				}(groupKey, chunk)
				continue
			}

			wg.Add(1)
			go func(gk string, chunkRows []map[string]interface{}) {
				defer wg.Done()
				patches, err := w.dispatchGroup(ctx, agentTaskQueue, chunkRows, gk, stageCfg, ctxMap)
				if err != nil {
					w.logger.Error("classification_stage: dispatch failed",
						"stage", stageCfg.Stage, "group", gk, "error", err)
					return
				}
				mu.Lock()
				allPatches = append(allPatches, patches...)
				mu.Unlock()
			}(groupKey, chunk)
		}
	}
	wg.Wait()

	// Write results to DB.
	written := 0
	for _, patch := range allPatches {
		rowID, _ := patch["id"].(string)
		valueStr, ok := patch[stageCfg.DBWriteColumn].(string)
		if !ok {
			// Try to parse candidates object
			if obj, isObj := patch[stageCfg.DBWriteColumn].(map[string]interface{}); isObj {
				if cands, hasCands := obj["candidates"].(map[string]interface{}); hasCands {
					var maxConf float64 = -1
					var bestVal string
					var bestReasoning string
					for _, cand := range cands {
						candObj, isCandObj := cand.(map[string]interface{})
						if !isCandObj {
							continue
						}
						var conf float64
						switch v := candObj["confidence"].(type) {
						case float64:
							conf = v
						case int:
							conf = float64(v)
						case string:
							conf, _ = strconv.ParseFloat(v, 64)
						}
						if conf > maxConf {
							maxConf = conf
							bestVal, _ = candObj["value"].(string)
							bestReasoning, _ = candObj["reasoning"].(string)
						}
					}
					if maxConf >= 0 {
						valueStr = bestVal
						patch["match_confidence"] = maxConf
						if bestReasoning != "" {
							patch["reasoning"] = bestReasoning
						}
					}
				}
			}
		}

		if rowID == "" || valueStr == "" {
			continue
		}
		if err := w.updateColumn(ctx, rowID, stageCfg.DBWriteColumn, valueStr, patch, stageCfg.DBWriteReasoning, realmID, stageCfg.Stage); err != nil {
			w.logger.Warn("classification_stage: failed to write column",
				"stage", stageCfg.Stage, "column", stageCfg.DBWriteColumn, "id", rowID, "error", err)
			continue
		}
		written++
	}

	// Persist cash_direction for the macro stage with direction grouping.
	if stageCfg.GroupBy == "direction" {
		for groupKey, gr := range groups {
			for _, r := range gr {
				w.persistCashDirection(ctx, r, groupKey)
			}
		}
	}

	w.emitInform(cid, map[string]interface{}{
		"stage":   stageCfg.Stage,
		"written": written,
	})
	msg.Ack()
	return nil
}

// isValidDBFilter acts as a security measure against SQL injection by strictly
// allowing only pre-approved db_filter strings defined in the workflow YAML.
func (w *ClassificationStageWorker) isValidDBFilter(filter string) bool {
	allowedFilters := map[string]bool{
		"rule_group_id IS NULL AND macro_class IS NULL AND status = 'ENRICHED'":                 true,
		"rule_group_id IS NULL AND macro_class IS NOT NULL AND account_type IS NULL":            true,
		"rule_group_id IS NULL AND account_type IS NOT NULL AND merchant_name IS NULL":          true,
		"rule_group_id IS NULL AND account_type IS NOT NULL AND predicted_account_name IS NULL": true,
	}
	return allowedFilters[strings.TrimSpace(filter)]
}

// queryRows fetches staging_transactions rows matching the specific session ID
// and the current stage's DB filter. It handles robust date parsing and scanning.
func (w *ClassificationStageWorker) queryRows(ctx context.Context, sessionID pgtype.UUID, filter string) ([]map[string]interface{}, error) {
	if !w.isValidDBFilter(filter) {
		return nil, fmt.Errorf("invalid db_filter provided in config: %s", filter)
	}
	query := fmt.Sprintf(
		`SELECT id, raw_description, raw_amount, merchant_name, macro_class, account_type, raw_date, parsed_date, erp_transaction_id
		 FROM fignode.staging_transactions
		 WHERE session_id = $1 AND %s`, filter,
	)
	rows, err := w.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query rows: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id, rawAmount string
		var desc, merchant, macroClass, accountType, erpTransactionID pgtype.Text
		var rawDateText pgtype.Text
		var parsedDate pgtype.Date
		if err := rows.Scan(&id, &desc, &rawAmount, &merchant, &macroClass, &accountType, &rawDateText, &parsedDate, &erpTransactionID); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		row := map[string]interface{}{
			"id":         id,
			"raw_amount": rawAmount,
		}
		if rawDateText.Valid {
			row["raw_date"] = rawDateText.String
		} else if parsedDate.Valid {
			row["raw_date"] = parsedDate.Time.Format("2006-01-02")
		}
		if parsedDate.Valid {
			row["parsed_date"] = parsedDate.Time
		}
		if desc.Valid {
			row["raw_description"] = desc.String
		}
		if merchant.Valid {
			row["merchant_name"] = merchant.String
		}
		if macroClass.Valid {
			row["macro_class"] = macroClass.String
		}
		if accountType.Valid {
			row["account_type"] = accountType.String
		}
		if erpTransactionID.Valid {
			row["erp_transaction_id"] = erpTransactionID.String
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// splitByDirection segregates rows into OUTFLOWs and INFLOWs based on the row's
// numeric amount and the session's outflow_is rule (whether negative or positive
// amounts represent money leaving the business).
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
		amountStr = strings.ReplaceAll(amountStr, "$", "")
		amountStr = strings.TrimSpace(amountStr)
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

// groupRows splits rows into batches based on the stage's group_by strategy
// (e.g., "direction" groups by OUTFLOW/INFLOW, "macro_class" groups by ASSET/EXPENSE).
// This determines how transactions are batched before being sent to the LLM.
func (w *ClassificationStageWorker) groupRows(rows []map[string]interface{}, groupBy string, outflowIs string) map[string][]map[string]interface{} {
	groups := make(map[string][]map[string]interface{})

	switch groupBy {
	case "direction":
		allIface := make([]interface{}, len(rows))
		for i, r := range rows {
			allIface[i] = r
		}
		outflows, inflows := splitByDirection(allIface, outflowIs, w.logger)
		for _, r := range outflows {
			if rm, ok := r.(map[string]interface{}); ok {
				groups["OUTFLOW"] = append(groups["OUTFLOW"], rm)
			}
		}
		for _, r := range inflows {
			if rm, ok := r.(map[string]interface{}); ok {
				groups["INFLOW"] = append(groups["INFLOW"], rm)
			}
		}

	case "macro_class":
		for _, r := range rows {
			mc, _ := r["macro_class"].(string)
			if mc == "" {
				mc = "UNKNOWN"
			}
			groups[mc] = append(groups[mc], r)
		}

	case "transaction_type":
		for _, r := range rows {
			mc, _ := r["macro_class"].(string)
			if mc == "REVENUE" {
				groups["INFLOW"] = append(groups["INFLOW"], r)
			} else {
				groups["OUTFLOW"] = append(groups["OUTFLOW"], r)
			}
		}

	default: // "none" or empty
		groups["default"] = rows
	}

	return groups
}

// resolveIntraFileTwins identifies identical offsetting transactions within the same file
// (e.g., a $500 OUTFLOW and $500 INFLOW on the same day). It attempts to deterministically
// assign EXPENSE/REVENUE labels based on temporal order or the type of bank account
// (e.g., Stripe payouts) to prevent the LLM from misclassifying matched pairs.
func (w *ClassificationStageWorker) resolveIntraFileTwins(ctx context.Context, rows []map[string]interface{}, session database.GetCleanupSessionRow) {
	// Find bank account name if valid
	var bankAccountName string
	if session.BankAccountID.Valid {
		var err error
		bankAccountName, err = w.store.GetBankAccountName(ctx, session.BankAccountID)
		if err != nil {
			w.logger.Warn("classification_stage: failed to fetch bank account name for twin logic", "error", err)
		}
	}
	bankAccountName = strings.ToLower(bankAccountName)
	isMerchantProcessor := strings.Contains(bankAccountName, "stripe") || strings.Contains(bankAccountName, "square") || strings.Contains(bankAccountName, "shopify")

	// Group rows by absolute amount
	byAmount := make(map[string][]map[string]interface{})
	for _, r := range rows {
		amtStr, ok := r["raw_amount"].(string)
		if !ok {
			continue
		}
		amtStr = strings.ReplaceAll(amtStr, ",", "")
		amtStr = strings.ReplaceAll(amtStr, "$", "")
		amtStr = strings.TrimSpace(amtStr)
		amt, err := strconv.ParseFloat(amtStr, 64)
		if err != nil || amt == 0 {
			continue
		}
		absStr := fmt.Sprintf("%.2f", math.Abs(amt))
		byAmount[absStr] = append(byAmount[absStr], r)
	}

	for _, twinGroup := range byAmount {
		if len(twinGroup) < 2 {
			continue // Need at least 2 items to form a pair
		}

		// Separate into positive and negative
		var posRows, negRows []map[string]interface{}
		for _, r := range twinGroup {
			amtStr, _ := r["raw_amount"].(string)
			amtStr = strings.ReplaceAll(amtStr, ",", "")
			amtStr = strings.ReplaceAll(amtStr, "$", "")
			amtStr = strings.TrimSpace(amtStr)
			amt, _ := strconv.ParseFloat(amtStr, 64)
			if amt > 0 {
				posRows = append(posRows, r)
			} else {
				negRows = append(negRows, r)
			}
		}

		// Match as many pairs as possible
		pairsToMatch := len(posRows)
		if len(negRows) < pairsToMatch {
			pairsToMatch = len(negRows)
		}

		for i := 0; i < pairsToMatch; i++ {
			rPos := posRows[i]
			rNeg := negRows[i]

			var outflowRow, inflowRow map[string]interface{}
			isOutflowPos := false
			if session.OutflowIs == "POSITIVE" {
				isOutflowPos = true
			}

			if isOutflowPos {
				outflowRow = rPos
				inflowRow = rNeg
			} else {
				outflowRow = rNeg
				inflowRow = rPos
			}

			date1, hasDate1 := rPos["parsed_date"].(time.Time)
			date2, hasDate2 := rNeg["parsed_date"].(time.Time)

			forcedMacro := ""
			if hasDate1 && hasDate2 && !date1.Equal(date2) {
				outflowDate, _ := outflowRow["parsed_date"].(time.Time)
				inflowDate, _ := inflowRow["parsed_date"].(time.Time)

				if outflowDate.Before(inflowDate) {
					forcedMacro = "EXPENSE"
				} else if inflowDate.Before(outflowDate) {
					forcedMacro = "REVENUE"
				}
			} else {
				// Same-day tiebreaker
				if isMerchantProcessor {
					forcedMacro = "REVENUE"
				} else {
					forcedMacro = "EXPENSE"
				}
			}

			if forcedMacro != "" {
				rPos["system_forced_macro"] = forcedMacro
				rNeg["system_forced_macro"] = forcedMacro
				w.logger.Info("classification_stage: applied intra-file twin matching", "forced_macro", forcedMacro, "idPos", rPos["id"], "idNeg", rNeg["id"])
			}
		}
	}
}

// dispatchGroup sends a Call for Proposal (CFP) to the LLM agent via NATS for a specific
// batch of rows. It blocks and waits for an INFORM reply containing the agent's classifications,
// or errors out on timeout.
func (w *ClassificationStageWorker) dispatchGroup(
	ctx context.Context,
	agentTaskQueue string,
	rows []map[string]interface{},
	groupKey string,
	stageCfg StageConfig,
	ctxMap map[string]interface{},
) ([]map[string]interface{}, error) {
	replyDID := fmt.Sprintf("did:toro:reply:%s", uuid.New().String())
	replySubject := fmt.Sprintf("agents.%s.inbox", replyDID)

	sub, err := w.nc.SubscribeSync(replySubject)
	if err != nil {
		return nil, fmt.Errorf("subscribe reply: %w", err)
	}
	defer sub.Unsubscribe()

	// Build payload and system prompt for this group.
	rowsIface := make([]interface{}, len(rows))
	for i, r := range rows {
		rowsIface[i] = r
	}
	payload := map[string]interface{}{"rows": rowsIface}

	// Merge context into payload.
	for k, v := range ctxMap {
		payload[k] = v
	}

	// For direction grouping, inject direction-specific rules before rendering prompt.
	if stageCfg.GroupBy == "direction" {
		payload["cash_direction"] = groupKey
		if groupKey == "OUTFLOW" {
			customerNames, _ := ctxMap["customer_names"].([]string)
			dbHints := findEntityHints(rows, customerNames)
			intraHints := findIntraFileHints(rows, ctxMap["opposing_descs_outflow"].([]string), customerNames)
			payload["classification_rules"] = buildOutflowRules(dbHints, intraHints)
			ctxMap["classification_rules"] = payload["classification_rules"].(string)
		} else {
			vendorNames, _ := ctxMap["vendor_names"].([]string)
			dbHints := findEntityHints(rows, vendorNames)
			intraHints := findIntraFileHints(rows, ctxMap["opposing_descs_inflow"].([]string), vendorNames)
			payload["classification_rules"] = buildInflowRules(dbHints, intraHints)
			ctxMap["classification_rules"] = payload["classification_rules"].(string)
		}
	}

	// Build the system prompt from template.
	systemPrompt := w.renderPrompt(stageCfg.SystemPromptTmpl, groupKey, ctxMap)

	payloadBytes, _ := json.Marshal(payload)

	taskDef := core.TaskDefinition{
		ID:           uuid.New().String(),
		Domain:       "agents.accounting.batch_categorization",
		Payload:      json.RawMessage(payloadBytes),
		SystemPrompt: systemPrompt,
		Model:        stageCfg.Model,
	}

	cfpEnv, err := core.NewEnvelope(
		uuid.New().String(),
		replyDID,
		"",
		uuid.New().String(),
		core.CFP,
		taskDef,
	)
	if err != nil {
		return nil, fmt.Errorf("build cfp: %w", err)
	}

	cfpBytes, _ := json.Marshal(cfpEnv)
	if err := w.nc.Publish(agentTaskQueue, cfpBytes); err != nil {
		return nil, fmt.Errorf("publish cfp: %w", err)
	}

	timeout := time.After(120 * time.Second)
	for {
		reply, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			select {
			case <-timeout:
				return nil, fmt.Errorf("timeout waiting for agent reply for group %s", groupKey)
			default:
				continue
			}
		}

		var replyEnv core.Envelope
		if err := json.Unmarshal(reply.Data, &replyEnv); err != nil {
			continue
		}

		switch replyEnv.Performative {
		case core.PROPOSE:
			w.logger.Info("classification_stage: agent proposed", "group", groupKey)
		case core.FAILURE:
			var failurePayload map[string]interface{}
			if err := json.Unmarshal(replyEnv.Body, &failurePayload); err == nil {
				if errMsg, ok := failurePayload["error"].(string); ok {
					return nil, fmt.Errorf("agent failed for group %s: %s", groupKey, errMsg)
				}
			}
			return nil, fmt.Errorf("agent failed for group %s", groupKey)
		case core.INFORM:
			var proof core.Proof
			if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
				return nil, fmt.Errorf("unmarshal proof: %w", err)
			}
			var patchWrapper map[string]interface{}
			if err := json.Unmarshal(proof.Data, &patchWrapper); err != nil {
				var patches []map[string]interface{}
				if err2 := json.Unmarshal(proof.Data, &patches); err2 != nil {
					return nil, fmt.Errorf("unmarshal patch data: %w", err)
				}
				for _, p := range patches {
					if path, _ := p["path"].(string); path == "/rows" {
						patchWrapper = p
						break
					}
				}
				if patchWrapper == nil && len(patches) > 0 {
					patchWrapper = patches[0]
				}
			}
			rowsValue, ok := patchWrapper["value"]
			if !ok {
				return nil, fmt.Errorf("patch missing 'value' key")
			}
			rowsMap, ok := rowsValue.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("patch value is not a map")
			}
			var patches []map[string]interface{}
			for _, v := range rowsMap {
				if rowMap, ok := v.(map[string]interface{}); ok {
					patches = append(patches, rowMap)
				}
			}
			return patches, nil
		}
	}
}

// renderPrompt fills {placeholders} in the system prompt template with values from ctxMap,
// group-specific rules, and valid account type options.
func (w *ClassificationStageWorker) renderPrompt(tmpl string, groupKey string, ctxMap map[string]interface{}) string {
	result := tmpl

	result = strings.ReplaceAll(result, "{cash_direction}", groupKey)
	result = strings.ReplaceAll(result, "{macro_class}", groupKey)

	if opts, ok := ase.AccountTypeOptions[groupKey]; ok {
		result = strings.ReplaceAll(result, "{account_type_options}", opts)
	}

	if rules, ok := ase.MacroClassSpecificRules[groupKey]; ok {
		result = strings.ReplaceAll(result, "{macro_class_specific_rules}", rules)
	}

	// Generic: iterate ctxMap and replace {key} with JSON-marshaled value.
	for k, v := range ctxMap {
		placeholder := "{" + k + "}"
		if !strings.Contains(result, placeholder) {
			continue
		}
		if s, ok := v.(string); ok {
			result = strings.ReplaceAll(result, placeholder, s)
		} else if b, err := json.Marshal(v); err == nil {
			result = strings.ReplaceAll(result, placeholder, string(b))
		}
	}

	// Direction-specific rules.

	// Per-group entity list: OUTFLOW/EXPENSE → vendors only, INFLOW/REVENUE → customers only.
	if strings.Contains(result, "{entity_list}") {
		if groupKey == "OUTFLOW" || groupKey == "EXPENSE" || groupKey == "LIABILITY" || groupKey == "ASSET" || groupKey == "EQUITY" {
			if vendors, ok := ctxMap["entity_list_vendors"].(string); ok {
				result = strings.ReplaceAll(result, "{entity_list}", vendors)
			}
		} else if groupKey == "INFLOW" || groupKey == "REVENUE" {
			if customers, ok := ctxMap["entity_list_customers"].(string); ok {
				result = strings.ReplaceAll(result, "{entity_list}", customers)
			}
		}
	}
	return result
}

// erpIDTagPattern matches display-format tags like [erp_id:15] or [id:XYZ]
// that the LLM is instructed to return based on the provided context lists.
var erpIDTagPattern = regexp.MustCompile(`^\[(?:erp_id|id):(.+?)\]$`)

// extractErpID strips the display-format wrapper from values returned by the LLM.
// For example, "[erp_id:15]" becomes "15".
func extractErpID(raw string) string {
	if m := erpIDTagPattern.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return raw
}

// patchColumnMap maps JSON field names outputted by the LLM agent to their
// corresponding database column names in staging_transactions.
var patchColumnMap = map[string]string{
	"macro_class":      "macro_class",
	"account_type":     "account_type",
	"reasoning":        "ai_reasoning",
	"new_clean_name":   "merchant_name",
	"match_confidence": "confidence_score",
	"requires_split":   "split_suggestion",
}

// formatStageName converts a snake_case stage key (e.g., "macro_class") to a
// human-readable label (e.g., "Macro Class") for use in reasoning logs.
func formatStageName(stage string) string {
	words := strings.Split(stage, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// updateColumn writes the mapped fields from the agent's patch to the database row.
// It dynamically handles resolving erp_ids into UUIDs for entities and accounts,
// and appends stage-specific reasoning without overwriting previous stages.
func (w *ClassificationStageWorker) updateColumn(ctx context.Context, rowID, column, value string, patch map[string]interface{}, writeReasoning bool, realmID, stageName string) error {
	// Build SET clauses for every mapped field present in the patch.
	var setClauses []string
	var args []interface{}
	argIdx := 1

	args = append(args, rowID)
	argIdx++

	// Always include the primary column.
	if dbCol, ok := patchColumnMap[column]; ok && value != "" {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", dbCol, argIdx))
		args = append(args, value)
		argIdx++
	}

	// Dynamic handler for account_id (convert ERP ID to UUID)
	if acctErpID, ok := patch["account_id"]; ok {
		sErp := extractErpID(fmt.Sprint(acctErpID))
		if sErp != "" {
			if acct, err := w.store.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{RealmID: realmID, ErpID: sErp}); err == nil {
				setClauses = append(setClauses, fmt.Sprintf("predicted_account_id = $%d", argIdx))
				args = append(args, acct.ID)
				argIdx++
				setClauses = append(setClauses, fmt.Sprintf("predicted_account_name = $%d", argIdx))
				args = append(args, acct.Name)
				argIdx++
			} else {
				w.logger.Warn("classification_stage: account_id resolution failed", "erp_id", sErp, "error", err)
			}
		}
	}

	// Dynamic handler for entity_id (convert ERP ID to vendor/customer UUID)
	if entErpID, ok := patch["entity_id"]; ok {
		sErp := extractErpID(fmt.Sprint(entErpID))
		if sErp != "" {
			// Try vendor first
			if vendor, err := w.store.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{RealmID: realmID, ErpID: sErp}); err == nil {
				setClauses = append(setClauses, fmt.Sprintf("predicted_vendor_id = $%d", argIdx))
				args = append(args, vendor.ID)
				argIdx++
			} else if customer, err := w.store.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{RealmID: realmID, ErpID: sErp}); err == nil {
				setClauses = append(setClauses, fmt.Sprintf("predicted_customer_id = $%d", argIdx))
				args = append(args, customer.ID)
				argIdx++
			} else {
				w.logger.Warn("classification_stage: entity_id resolution failed", "erp_id", sErp, "error", err)
			}
		}
	}

	// Scan patch for other mapped fields.
	for patchField, dbCol := range patchColumnMap {
		if patchField == column {
			continue // already handled
		}
		if patchField == "reasoning" {
			// Concatenate with stage name prefix instead of overwriting prior stages.
			if v, ok := patch[patchField]; ok {
				s := fmt.Sprint(v)
				if s != "" && s != "false" && s != "0" {
					formattedReasoning := fmt.Sprintf("[%s]: %s", formatStageName(stageName), s)
					setClauses = append(setClauses, fmt.Sprintf("%s = CASE WHEN %s IS NULL OR %s = '' THEN $%d ELSE %s || E'\\n' || $%d END", dbCol, dbCol, dbCol, argIdx, dbCol, argIdx))
					args = append(args, formattedReasoning)
					argIdx++
				}
			}
			continue
		}
		if patchField == "requires_split" {
			// Convert bool to JSON for split_suggestion column.
			if rs, ok := patch[patchField]; ok {
				splitJSON, _ := json.Marshal(map[string]interface{}{"requires_split": rs})
				setClauses = append(setClauses, fmt.Sprintf("%s = $%d", dbCol, argIdx))
				args = append(args, string(splitJSON))
				argIdx++
			}
			continue
		}
		if patchField == "match_confidence" {
			// Store as numeric; convert string labels.
			if mc, ok := patch[patchField]; ok {
				var numericVal float64
				switch v := mc.(type) {
				case float64:
					numericVal = v
				case int:
					numericVal = float64(v)
				case string:
					switch strings.ToUpper(v) {
					case "HIGH":
						numericVal = 0.9
					case "MEDIUM":
						numericVal = 0.5
					case "LOW":
						numericVal = 0.1
					default:
						numericVal, _ = strconv.ParseFloat(v, 64)
					}
				}
				setClauses = append(setClauses, fmt.Sprintf("%s = $%d", dbCol, argIdx))
				args = append(args, numericVal)
				argIdx++
			}
			continue
		}
		if v, ok := patch[patchField]; ok {
			s := fmt.Sprint(v)
			if s != "" && s != "false" && s != "0" {
				setClauses = append(setClauses, fmt.Sprintf("%s = $%d", dbCol, argIdx))
				args = append(args, s)
				argIdx++
			}
		}
	}

	if len(setClauses) == 0 {
		return nil
	}

	setClauses = append(setClauses, "updated_at = NOW()")
	query := fmt.Sprintf("UPDATE fignode.staging_transactions SET %s WHERE id = $1",
		strings.Join(setClauses, ", "))

	_, err := w.pool.Exec(ctx, query, args...)
	return err
}

// persistCashDirection writes the derived cash_direction (OUTFLOW or INFLOW)
// to the database. This is usually called during the first grouping stage.
func (w *ClassificationStageWorker) persistCashDirection(ctx context.Context, row map[string]interface{}, direction string) {
	idRaw, ok := row["id"]
	if !ok {
		return
	}
	idStr, ok := idRaw.(string)
	if !ok {
		return
	}
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		return
	}
	if err := w.store.UpdateStagingTransactionCashDirection(ctx, database.UpdateStagingTransactionCashDirectionParams{
		ID:            id,
		CashDirection: pgtype.Text{String: direction, Valid: true},
	}); err != nil {
		w.logger.Warn("classification_stage: failed to persist cash_direction", "id", idStr, "error", err)
	}
}

// injectContext fetches realm-level context (Chart of Accounts, Bank Accounts,
// Vendors, Customers, Company Info) from the database and prepares it for
// injection into the LLM system prompt.
func (w *ClassificationStageWorker) injectContext(ctx context.Context, realmID string, m map[string]interface{}) {
	if realmID == "" {
		return
	}
	if bankAccounts, err := w.store.GetBankAccounts(ctx); err != nil {
		w.logger.Warn("classification_stage: failed to fetch bank accounts", "error", err)
	} else {
		m["json_list_of_bank_accounts"] = bankAccounts
	}
	if accounts, err := w.store.GetAccountsByRealm(ctx, realmID); err != nil {
		w.logger.Warn("classification_stage: failed to fetch accounts", "error", err)
	} else {
		byType := make(map[string][]string)
		for _, a := range accounts {
			line := fmt.Sprintf("[erp_id:%s] %s", a.ErpID, a.Name)
			at := a.AccountType
			if at == "" {
				at = "Other"
			}
			byType[at] = append(byType[at], line)
		}
		var parts []string
		for at, lines := range byType {
			parts = append(parts, fmt.Sprintf("### %s\n%s", at, strings.Join(lines, "\n")))
		}
		m["json_list_of_all_filtered_accounts"] = strings.Join(parts, "\n\n")
	}
	if vendors, err := w.store.GetVendorsByRealm(ctx, realmID); err != nil {
		w.logger.Warn("classification_stage: failed to fetch vendors", "error", err)
	} else {
		customers, _ := w.store.GetCustomersByRealm(ctx, realmID)
		var vendorLines, customerLines []string
		var vendorNames, customerNames []string
		for _, v := range vendors {
			vendorLines = append(vendorLines, fmt.Sprintf("[id:%s] %s", v.ErpID, v.DisplayName))
			vendorNames = append(vendorNames, v.DisplayName)
		}
		for _, cu := range customers {
			customerLines = append(customerLines, fmt.Sprintf("[id:%s] %s", cu.ErpID, cu.DisplayName))
			customerNames = append(customerNames, cu.DisplayName)
		}
		m["entity_list_vendors"] = "### EXISTING VENDORS\n" + strings.Join(vendorLines, "\n")
		m["entity_list_customers"] = "### EXISTING CUSTOMERS\n" + strings.Join(customerLines, "\n")
		m["vendor_names"] = vendorNames
		m["customer_names"] = customerNames
	}
	if company, err := w.store.GetCompanyInfo(ctx, realmID); err != nil {
		w.logger.Warn("classification_stage: failed to fetch company info", "error", err)
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
		m["company_industry_description"] = strings.Join(parts, " — ")
	}
}

// emitInform sends an INFORM message back to the orchestrator (Hive) to signal
// that this classification stage has completed its processing.
func (w *ClassificationStageWorker) emitInform(cid string, data map[string]interface{}) {
	replyEnv := map[string]interface{}{
		"id":   uuid.New().String(),
		"ts":   time.Now().UTC(),
		"src":  "did:toro:classification-stage",
		"dst":  "did:toro:hive",
		"perf": core.INFORM,
		"cid":  cid,
		"body": map[string]interface{}{
			"type": "proof.delegation.complete",
			"data": data,
		},
		"sig": "worker-sig",
	}
	replyBytes, _ := json.Marshal(replyEnv)
	js, err := w.nc.JetStream()
	if err != nil {
		w.logger.Error("classification_stage: failed to get JetStream context", "error", err)
		return
	}
	if _, err := js.Publish("orchestrator.inbox", replyBytes); err != nil {
		w.logger.Error("classification_stage: failed to publish inform", "error", err)
	} else {
		w.logger.Info("classification_stage: sent inform to orchestrator", "cid", cid)
	}
}

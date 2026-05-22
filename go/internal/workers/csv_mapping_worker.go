package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// CSVMappingWorker subscribes to NATS CDC events and enriches staging rows
// using the existing EntityResolver + CoAMapper AI services.
type CSVMappingWorker struct {
	db             *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	nc             *nats.Conn
	logger         *slog.Logger
	cfg            *config.Config
}

// NewCSVMappingWorker creates a new worker for csv mapping ingestion and enrichment.
func NewCSVMappingWorker(
	db *database.Queries,
	entityResolver *ai.EntityResolver,
	coaMapper *ai.CoAMapper,
	nc *nats.Conn,
	logger *slog.Logger,
	cfg *config.Config,
) (*CSVMappingWorker, error) {
	return &CSVMappingWorker{
		db:             db,
		entityResolver: entityResolver,
		coaMapper:      coaMapper,
		nc:             nc,
		logger:         logger,
		cfg:            cfg,
	}, nil
}

func (e *CSVMappingWorker) Init(ctx context.Context) error {
	return nil
}

func (e *CSVMappingWorker) Subscriptions() []SubscriptionConfig {
	if e.cfg == nil {
		e.logger.Error("csv mapping worker: missing config, cannot derive subject")
		return nil
	}

	_, workerCfg := e.cfg.Workers.GetForWorker(e)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		e.logger.Error("csv mapping worker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			e.logger.Error("csv mapping worker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (e *CSVMappingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	e.logger.Info("📡 [DEBUG] csv_mapping_worker received JetStream TAP message", "topic", msg.Subject, "data_length", len(msg.Data))
	if err := e.handleProof(ctx, msg); err != nil {
		e.logger.Error("csv mapping worker transient error", "error", err)
		msg.Nak() // Tell JetStream to redeliver immediately
		return err
	}

	msg.Ack() // We are completely done, explicitly ack the message
	return nil
}

func extractString(raw interface{}) string {
	if raw == nil {
		return ""
	}
	if str, ok := raw.(string); ok {
		return strings.TrimSpace(str)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw))
}

func searchStringValue(value interface{}, keys ...string) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range keys {
			if raw, ok := typed[key]; ok {
				if str := extractString(raw); str != "" {
					return str
				}
			}
		}
		for _, child := range typed {
			if str := searchStringValue(child, keys...); str != "" {
				return str
			}
		}
	case []interface{}:
		for _, item := range typed {
			if str := searchStringValue(item, keys...); str != "" {
				return str
			}
		}
	}
	return ""
}

// RawRow matches the struct output by the TAP Cleanup Agent
type RawRow struct {
	SessionID   string `json:"SessionID"`
	RealmID     string `json:"RealmID"`
	Date        string `json:"Date"`
	Description string `json:"Description"`
	Amount      string `json:"Amount"`
	Vendor      string `json:"Vendor"`
	Customer    string `json:"Customer"`
}

// handleProof processes the finalized mapping output from the AI TAP Agent.
func (e *CSVMappingWorker) handleProof(ctx context.Context, msg *nats.Msg) error {
	// 1. Unmarshal TAP Envelope
	e.logger.Info("📡 [DEBUG] csv_mapping_worker received message", "data_len", len(msg.Data), "subject", msg.Subject)
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		e.logger.Error("csv mapping worker: bad proof envelope", "error", err)
		return nil
	}

	// Check performative
	perfStr, ok := env["perf"].(string)
	perf := core.Performative(perfStr)
	if !ok || !core.IsValidPerformative(perf) {
		e.logger.Warn("csv mapping worker: dropping message, invalid performative", "perf_val", env["perf"])
		return nil
	}
	if perf != core.REQUEST {
		e.logger.Warn("csv mapping worker: dropping message, perf mismatch", "perf_val", env["perf"])
		return nil
	}

	bodyBytes, _ := json.Marshal(env["body"])
	var rows []map[string]interface{}
	var err error

	// 1. Try extracting rows from the body directly (handles switch worker outputs and raw dependency maps)
	rows, err = ExtractRows(bodyBytes)
	if err != nil || len(rows) == 0 {
		// 2. Fallback: try interpreting as a traditional Proof (INFORM) or TaskDefinition (ACCEPT_PROPOSAL)
		var proof core.Proof
		if err := json.Unmarshal(bodyBytes, &proof); err == nil && len(proof.Data) > 0 {
			rows, _ = ExtractRows(proof.Data)
		} else {
			var taskDef core.TaskDefinition
			if err := json.Unmarshal(bodyBytes, &taskDef); err == nil && len(taskDef.Payload) > 0 {
				rows, _ = ExtractRows(taskDef.Payload)
			}
		}
	}

	if len(rows) == 0 {
		e.logger.Error("csv mapping worker: failed to extract rows from any part of the envelope", "body", string(bodyBytes))
		return nil
	}

	if len(rows) == 0 {
		e.logger.Warn("csv mapping worker: proof contained 0 rows")
		return nil
	}

	// Get session/realm from the first row
	sessionID := core.RowString(rows[0], "SessionID", "session_id")
	realmID := core.RowString(rows[0], "RealmID", "realm_id")

	if sessionID == "" {
		if fallback := searchForSessionID(rows[0]); fallback != "" {
			sessionID = fallback
		} else {
			e.logger.Warn("csv mapping worker: missing sessionID in rows", "rows_count", len(rows), "first_row_keys", getMapKeys(rows[0]))
			return nil
		}
	}

	e.logger.Info("📡 [DEBUG] csv_mapping_worker extracted metadata", "session_id", sessionID, "realm_id", realmID)

	var pgSessionID pgtype.UUID
	pgSessionID.Scan(sessionID)

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.logger.Error("csv mapping worker: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	e.logger.Info("csv mapping worker: inserting AI-mapped rows", "session_id", sessionID, "count", len(rows))

	// Update session to PROCESSING
	statusErr := e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
		ID:     pgSessionID,
		Status: "PROCESSING",
	})
	if statusErr != nil {
		return fmt.Errorf("failed to update session status: %w", statusErr)
	}

	// Insert rows
	for i, r := range rows {
		// Ping NATS every 50 rows to reset the AckWait timer and prevent timeout redeliveries.
		if i > 0 && i%50 == 0 {
			if err := msg.InProgress(); err != nil {
				e.logger.Warn("csv mapping worker: failed to ping nats in-progress", "error", err)
			}
		}

		rawDescription := core.RowString(r, "Description", "description")
		rawAmount := core.RowString(r, "Amount", "amount")
		rawDateStr := core.RowString(r, "Date", "date")
		vendorName := core.RowString(r, "Vendor", "vendor")
		customerName := core.RowString(r, "Customer", "customer")

		var dDate pgtype.Date
		if rawDateStr != "" {
			if parsedDate, err := parseCSVDate(rawDateStr); err == nil {
				dDate = pgtype.Date{Time: parsedDate, Valid: true}
			} else {
				e.logger.Warn("csv mapping worker: failed to parse date, storing raw string only", "raw_date", rawDateStr, "error", err)
			}
		}

		_, err := e.db.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
			SessionID:             pgSessionID,
			RowIndex:              pgtype.Int4{Int32: int32(i), Valid: true}, // Uniquely identifies the row position to prevent dupe inserts on retry
			SourceType:            "CSV",
			RawDescription:        pgtype.Text{String: rawDescription, Valid: rawDescription != ""},
			RawAmount:             rawAmount,
			RawDate:               pgtype.Text{String: rawDateStr, Valid: rawDateStr != ""},
			ParsedDate:            dDate,
			Status:                "PENDING", // Match EnrichmentWorker query
			PredictedVendorName:   pgtype.Text{String: vendorName, Valid: vendorName != ""},
			PredictedCustomerName: pgtype.Text{String: customerName, Valid: customerName != ""},
		})
		if err != nil {
			e.logger.Error("csv mapping worker: row insert failed", "error", err)
			return fmt.Errorf("transient db error inserting row: %w", err)
		}
	}

	e.logger.Info("csv mapping worker: fully completed TAP DB inserts. Waiting for Enrichment Agent.")

	js, jsErr := e.nc.JetStream()
	if jsErr != nil {
		e.logger.Error("csv mapping worker: failed to get jetstream context", "error", jsErr)
		return nil
	}

	// Check if this was dispatched by Orchestrator with a conversation ID
	cid, hasCid := env["cid"].(string)
	if hasCid && cid != "" {
		replyEnv := map[string]interface{}{
			"id":   uuid.New().String(),
			"ts":   time.Now().UTC(),
			"src":  "did:toro:csv-mapping-worker",
			"dst":  workflows.OrchestratorDID,
			"perf": core.INFORM,
			"cid":  cid,
			"body": map[string]interface{}{
				"type": "proof.accounting.cleanup.inserted",
				"data": json.RawMessage(bodyBytes), // pass through actual mapping data
			},
			"sig": "worker-sig",
		}
		replyBytes, _ := json.Marshal(replyEnv)

		if _, pubErr := js.Publish(workflows.OrchestratorInbox, replyBytes); pubErr != nil {
			e.logger.Error("csv mapping worker: failed to notify orchestrator", "error", pubErr)
		} else {
			e.logger.Info("csv mapping worker: sent explicit INFORM back to Orchestrator", "cid", cid)
		}
	} else {
		// Legacy global broadcast if not part of a guided conversation
		if _, pubErr := js.Publish("proof.accounting.cleanup.inserted", msg.Data); pubErr != nil {
			e.logger.Error("csv mapping worker: failed to publish inserted proof", "error", pubErr)
		}
	}

	return nil
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewCSVMappingWorker(deps.Store.Queries, deps.EntityResolver, deps.CoAMapper, deps.Queue, deps.Logger, deps.Config)
	})
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func extractStringValue(raw interface{}) string {
	if raw == nil {
		return ""
	}
	if str, ok := raw.(string); ok {
		return strings.TrimSpace(str)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw))
}

func searchForSessionID(value interface{}) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"session_id", "SessionID"} {
			if raw, ok := typed[key]; ok {
				if str := extractStringValue(raw); str != "" {
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

// sanitizeDateString strips invisible Unicode characters, normalizes separators,
// and removes artifacts commonly found in bank statement CSV exports that cause
// time.Parse to silently fail. This includes BOM markers (\uFEFF), non-breaking
// spaces (\u00A0), zero-width spaces (\u200B), Windows carriage returns (\r),
// and en-dash/em-dash characters used in place of hyphens.
func sanitizeDateString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		// Drop: BOM, zero-width spaces, direction marks
		case r == '\uFEFF' || r == '\u200B' || r == '\u200C' || r == '\u200D' ||
			r == '\u200E' || r == '\u200F' || r == '\u2060' ||
			r == '\uFFFE':
			continue
		// Drop carriage return (Windows CSV line endings)
		case r == '\r':
			continue
		// Normalize non-breaking space and other Unicode whitespace to regular space
		case r == '\u00A0' || r == '\u2007' || r == '\u202F':
			b.WriteByte(' ')
		// Normalize en-dash / em-dash / minus sign / soft hyphen to ASCII hyphen
		case r == '\u2013' || r == '\u2014' || r == '\u2212' || r == '\u00AD':
			b.WriteByte('-')
		// Normalize fullwidth solidus to ASCII slash
		case r == '\uFF0F':
			b.WriteByte('/')
		// Normalize fullwidth period to ASCII period
		case r == '\uFF0E':
			b.WriteByte('.')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func parseCSVDate(dateStr string) (time.Time, error) {
	dateStr = sanitizeDateString(dateStr)
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}

	layouts := []string{
		// ISO 8601 (most unambiguous — try first)
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05",
		time.RFC3339,
		// Slash-separated (US then EU 4-digit year, then 2-digit)
		"01/02/2006",
		"01/02/2006 15:04:05",
		"02/01/2006",
		"2006/01/02",
		"01/02/06",
		"02/01/06",
		"1/2/2006",
		"2/1/2006",
		"1/2/06",
		"2/1/06",
		// Dot-separated (common in European banks: DD.MM.YYYY)
		"02.01.2006",
		"2.1.2006",
		"02.01.06",
		"2006.01.02",
		// Dash-separated numeric (MM-DD-YYYY, DD-MM-YYYY)
		"01-02-2006",
		"02-01-2006",
		"1-2-2006",
		"01-02-06",
		// Compact
		"20060102",
		// Named months
		"02-Jan-2006",
		"02-Jan-06",
		"Jan 02, 2006",
		"Jan 2, 2006",
		"Jan 02, 06",
		"January 02, 2006",
		"January 2, 2006",
		"02 Jan 2006",
		"2 Jan 2006",
		"02 January 2006",
		"2 January 2006",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date string: %s", dateStr)
}

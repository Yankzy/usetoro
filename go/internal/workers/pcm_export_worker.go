package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type PcmExportWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PcmExportWorker{
			logger: deps.Logger.With("worker", "pcm_export"),
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
		}, nil
	})
}

func (w *PcmExportWorker) Init(ctx context.Context) error {
	w.logger.Info("PcmExportWorker initialized")
	return nil
}

func (w *PcmExportWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.pcm_export"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			subject = "worker.inbox.pcm_export"
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "worker-inbox-pcm_export-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

type PcmExportPayload struct {
	SessionID          string                   `json:"session_id"`
	AccountingStandard string                   `json:"accounting_standard,omitempty"` // "PCM" or "US_GAAP"
	ExportToEmail      *bool                    `json:"export_to_email,omitempty"`
	ExportToCSV        *bool                    `json:"export_to_csv,omitempty"`
	ExportToPNM        *bool                    `json:"export_to_pnm,omitempty"`
	FromHandle         string                   `json:"from_handle,omitempty"`
	ToHandle           string                   `json:"to_handle,omitempty"`
	Attachments        []map[string]interface{} `json:"attachments,omitempty"`
}

func (w *PcmExportWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error("PcmExportWorker: failed to unmarshal envelope", "error", err)
		return err
	}

	if env.Performative != core.REQUEST {
		return nil
	}

	var payload PcmExportPayload
	if err := json.Unmarshal(env.Body, &payload); err != nil {
		w.logger.Error("PcmExportWorker: failed to unmarshal body", "error", err)
		return err
	}

	if payload.SessionID == "" {
		w.logger.Error("PcmExportWorker: missing session_id")
		return fmt.Errorf("missing session_id")
	}

	exportToEmail := payload.ExportToEmail == nil || *payload.ExportToEmail
	exportToCSV := payload.ExportToCSV == nil || *payload.ExportToCSV
	exportToPNM := payload.ExportToPNM == nil || *payload.ExportToPNM

	if !exportToEmail || (!exportToCSV && !exportToPNM) {
		w.logger.Info("PcmExportWorker: export disabled by parameters/config", "session_id", payload.SessionID, "export_to_email", exportToEmail, "export_to_csv", exportToCSV, "export_to_pnm", exportToPNM)
		return nil
	}

	w.logger.Info("PcmExportWorker: processing export", "session_id", payload.SessionID, "standard", payload.AccountingStandard)

	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(payload.SessionID); err != nil {
		w.logger.Error("PcmExportWorker: invalid session_id format", "session_id", payload.SessionID, "error", err)
		return fmt.Errorf("invalid session_id format: %w", err)
	}

	attachments := payload.Attachments
	if len(attachments) == 0 && w.db != nil {
		txs, err := w.db.GetPcmSessionTransactions(ctx, sessionUUID)
		if err != nil || len(txs) == 0 {
			w.logger.Error("PcmExportWorker: no transactions found to export", "session_id", payload.SessionID, "error", err)
			return fmt.Errorf("no transactions found to export for session %s: %v", payload.SessionID, err)
		}

		session, err := w.db.GetCleanupSession(ctx, sessionUUID)
		if err != nil {
			w.logger.Error("PcmExportWorker: failed to fetch cleanup session", "session_id", payload.SessionID, "error", err)
			return fmt.Errorf("failed to fetch cleanup session: %w", err)
		}

		realmID := ""
		if session.RealmID.Valid {
			realmID = session.RealmID.String
		}

		accounts, err := w.db.GetAccountsByRealm(ctx, realmID)
		accMap := make(map[string]string)
		if err == nil {
			for _, acc := range accounts {
				accCode := acc.ID.String()
				if acc.AccountCode.Valid && acc.AccountCode.String != "" {
					accCode = acc.AccountCode.String
				}
				accMap[acc.ID.String()] = accCode
			}
		}

		std := strings.ToUpper(payload.AccountingStandard)
		var csvData string
		if std == "US_GAAP" {
			csvData = generateUSGAAPCSV(txs, accMap)
		} else {
			csvData = generatePCMCSV(txs, accMap)
		}

		base64Data := base64.StdEncoding.EncodeToString([]byte(csvData))
		attachments = []map[string]interface{}{}

		if exportToCSV {
			filename := "Bank_Reconciliation_Export.csv"
			if std == "US_GAAP" {
				filename = "Bank_Reconciliation_Export_GAAP.csv"
			}
			attachments = append(attachments, map[string]interface{}{
				"Name":        filename,
				"ContentType": "text/csv",
				"Content":     base64Data,
			})
		}

		if exportToPNM && (std == "" || std == "PCM") {
			attachments = append(attachments, map[string]interface{}{
				"Name":        "Bank_Reconciliation_Export_Sage100.pnm",
				"ContentType": "application/octet-stream",
				"Content":     base64Data,
			})
		}

		_ = w.db.MarkPcmSessionExported(ctx, sessionUUID)
	}

	if len(attachments) == 0 {
		w.logger.Error("PcmExportWorker: no attachments/data found for export payload, skipping email dispatch", "session_id", payload.SessionID)
		return nil
	}

	toHandle := payload.ToHandle
	fromHandle := payload.FromHandle

	// Resolve missing handles from the conversation session if not explicitly provided
	if (toHandle == "" || fromHandle == "") && payload.SessionID != "" && w.db != nil {
		if sess, err := w.db.GetConversationSession(ctx, sessionUUID); err == nil {
			if toHandle == "" {
				toHandle = sess.ParticipantHandle
			}
			if fromHandle == "" {
				fromHandle = sess.ToroHandle
			}
		}
	}

	subject := "Bank Reconciliation Export"
	if strings.ToUpper(payload.AccountingStandard) == "US_GAAP" {
		subject = "US GAAP Export - Bank Reconciliation"
	} else {
		subject = "Sage 100 PNM Export - Bank Reconciliation"
	}

	response := map[string]interface{}{
		"body_text":     "Hello,\n\nPlease find attached your bank reconciliation export file.\n\nBest regards,\nToro AI Engine",
		"subject":       subject,
		"session_id":    payload.SessionID,
		"from_handle":   fromHandle,
		"to_handle":     toHandle,
		"source":        "email",
		"custom_msg_id": fmt.Sprintf("ase:pcm_export:%s", payload.SessionID),
		"attachments":   attachments,
	}

	proofData, _ := json.Marshal(response)
	proof := core.Proof{
		Type: "outgoing_chat",
		Data: proofData,
	}

	proofBytes, _ := json.Marshal(proof)

	envelope := core.Envelope{
		ID:             uuid.New().String(),
		ConversationID: env.ConversationID,
		Performative:   core.INFORM,
		Body:           proofBytes,
	}
	envelopeBytes, _ := json.Marshal(envelope)

	if w.nc != nil {
		if err := w.nc.Publish("proof.outgoing.chat", envelopeBytes); err != nil {
			w.logger.Error("PcmExportWorker: failed to publish to proof.outgoing.chat", "error", err)
			return err
		}
	}

	w.logger.Info("PcmExportWorker: successfully triggered email dispatch")
	return nil
}

func generatePCMCSV(txs []database.GetPcmSessionTransactionsRow, accMap map[string]string) string {
	var csvLines []string
	csvLines = append(csvLines, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit")

	for itemIdx, tx := range txs {
		accountID := ""
		counterparty := ""
		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" {
						if edge, ok := step["selected_edge"].(string); ok {
							accountID = edge
						}
					}
					if step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor" {
						if edge, ok := step["selected_edge"].(string); ok && edge != "" {
							counterparty = edge
						}
					}
				}
			}
		}

		accountCode := ""
		if accountID != "" {
			if code, ok := accMap[accountID]; ok {
				accountCode = code
			} else {
				accountCode = accountID
			}
		}
		if accountCode == "" {
			accountCode = "471000"
		}

		dateStr := ""
		if tx.ParsedDate.Valid {
			dateStr = tx.ParsedDate.Time.Format("020106")
		} else if tx.RawDate.Valid && tx.RawDate.String != "" {
			parsed := false
			for _, layout := range []string{"02/01/2006", "2006-01-02", "02-01-2006", "02/01/06"} {
				if t, err := time.Parse(layout, tx.RawDate.String); err == nil {
					dateStr = t.Format("020106")
					parsed = true
					break
				}
			}
			if !parsed {
				dateStr = strings.ReplaceAll(tx.RawDate.String, "/", "")
			}
		}
		if dateStr == "" {
			dateStr = time.Now().Format("020106")
		}

		desc := ""
		if tx.RawDescription.Valid {
			desc = tx.RawDescription.String
		}

		amountStr := tx.RawAmount
		amountStr = strings.ReplaceAll(amountStr, ",", "")
		amountStr = strings.ReplaceAll(amountStr, " ", "")

		var amount float64
		if f, err := strconv.ParseFloat(amountStr, 64); err == nil {
			amount = f
		}
		if amount < 0 {
			amount = -amount
		}

		direction := ""
		if tx.CashDirection.Valid {
			direction = strings.ToUpper(tx.CashDirection.String)
		}
		if direction == "" {
			lower := strings.ToLower(desc)
			if strings.Contains(lower, "client") || strings.Contains(lower, "virement recu") || strings.Contains(lower, "recu") {
				direction = "INFLOW"
			} else {
				direction = "OUTFLOW"
			}
		}

		desc = strings.ReplaceAll(desc, ";", " ")
		desc = strings.ReplaceAll(desc, "\"", "")
		desc = strings.TrimSpace(desc)
		if len(desc) > 35 {
			desc = desc[:35]
		}

		compteA := extractAuxAccount(desc, counterparty, direction, accountCode)

		if direction == "INFLOW" && (accountCode == "711100" || accountCode == "471000") {
			accountCode = "342100"
		} else if direction == "OUTFLOW" && accountCode == "471000" {
			if strings.HasPrefix(compteA, "F_") {
				accountCode = "441100"
			} else {
				accountCode = "619000"
			}
		}

		journal := "BQ"
		piece := fmt.Sprintf("BNK%03d", itemIdx+1)

		if direction == "OUTFLOW" {
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, amount, 0.00))
		} else {
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, 0.00, amount))
		}
	}

	return strings.Join(csvLines, "\r\n")
}

func generateUSGAAPCSV(txs []database.GetPcmSessionTransactionsRow, accMap map[string]string) string {
	var csvLines []string
	csvLines = append(csvLines, "Journal;Date;Account;Auxiliary;Piece;Description;Debit;Credit")

	for itemIdx, tx := range txs {
		accountID := ""
		counterparty := ""
		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" {
						if edge, ok := step["selected_edge"].(string); ok {
							accountID = edge
						}
					}
					if step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor" {
						if edge, ok := step["selected_edge"].(string); ok && edge != "" {
							counterparty = edge
						}
					}
				}
			}
		}

		accountName := accountID
		if name, ok := accMap[accountID]; ok && name != "" {
			accountName = name
		}
		if accountName == "" {
			accountName = "Uncategorized Transaction"
		}

		dateStr := ""
		if tx.ParsedDate.Valid {
			dateStr = tx.ParsedDate.Time.Format("2006-01-02")
		} else if tx.RawDate.Valid && tx.RawDate.String != "" {
			dateStr = tx.RawDate.String
		} else {
			dateStr = time.Now().Format("2006-01-02")
		}

		desc := ""
		if tx.RawDescription.Valid {
			desc = tx.RawDescription.String
		}
		desc = strings.ReplaceAll(desc, ";", " ")
		desc = strings.ReplaceAll(desc, "\"", "")

		amountStr := tx.RawAmount
		amountStr = strings.ReplaceAll(amountStr, ",", "")
		amountStr = strings.ReplaceAll(amountStr, " ", "")

		var amount float64
		if f, err := strconv.ParseFloat(amountStr, 64); err == nil {
			amount = f
		}
		if amount < 0 {
			amount = -amount
		}

		direction := ""
		if tx.CashDirection.Valid {
			direction = strings.ToUpper(tx.CashDirection.String)
		}
		if direction == "" {
			direction = "OUTFLOW"
		}

		journal := "BANK"
		piece := fmt.Sprintf("REF%03d", itemIdx+1)

		if direction == "OUTFLOW" {
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountName, counterparty, piece, desc, amount, 0.00))
		} else {
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountName, counterparty, piece, desc, 0.00, amount))
		}
	}

	return strings.Join(csvLines, "\r\n")
}

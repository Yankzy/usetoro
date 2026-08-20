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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

type PcmExportWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
	dbPool *pgxpool.Pool
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PcmExportWorker{
			logger: deps.Logger.With("worker", "pcm_export"),
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			dbPool: deps.DBPool,
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
	ExportToExcel      *bool                    `json:"export_to_excel,omitempty"`
	FromHandle         string                   `json:"from_handle,omitempty"`
	ToHandle           string                   `json:"to_handle,omitempty"`
	Attachments        []map[string]interface{} `json:"attachments,omitempty"`
}

type PcmExportTxRow struct {
	ID                   pgtype.UUID
	ParsedDate           pgtype.Date
	RawDate              pgtype.Text
	RawDescription       pgtype.Text
	RawAmount            string
	CashDirection        pgtype.Text
	AseExecutionTrace    []byte
	PredictedAccountName pgtype.Text
	PredictedAccountID   pgtype.UUID
	PredictedVendorName  pgtype.Text
	MerchantName         pgtype.Text
	MoroccanEnrichment   []byte
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
		w.publishCompletionProof(&env, &payload, 0, 0, "MISSING_SESSION_ID")
		return fmt.Errorf("missing session_id")
	}

	exportToEmail := payload.ExportToEmail == nil || *payload.ExportToEmail
	exportToCSV := payload.ExportToCSV == nil || *payload.ExportToCSV
	exportToPNM := payload.ExportToPNM == nil || *payload.ExportToPNM
	exportToExcel := payload.ExportToExcel == nil || *payload.ExportToExcel

	if !exportToEmail || (!exportToCSV && !exportToPNM && !exportToExcel) {
		w.logger.Info("PcmExportWorker: export disabled by parameters/config",
			"session_id", payload.SessionID,
			"export_to_email", exportToEmail,
			"export_to_csv", exportToCSV,
			"export_to_pnm", exportToPNM,
			"export_to_excel", exportToExcel,
		)
		w.publishCompletionProof(&env, &payload, 0, 0, "EXPORT_DISABLED")
		return nil
	}

	w.logger.Info("PcmExportWorker: processing export", "session_id", payload.SessionID, "standard", payload.AccountingStandard)

	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(payload.SessionID); err != nil {
		w.logger.Error("PcmExportWorker: invalid session_id format", "session_id", payload.SessionID, "error", err)
		w.publishCompletionProof(&env, &payload, 0, 0, "INVALID_SESSION_ID")
		return fmt.Errorf("invalid session_id format: %w", err)
	}

	attachments := payload.Attachments
	var txs []PcmExportTxRow

	if len(attachments) == 0 {
		var err error
		txs, err = w.fetchSessionTransactions(ctx, sessionUUID)
		if err != nil || len(txs) == 0 {
			w.logger.Info("PcmExportWorker: 0 transactions found to export for session", "session_id", payload.SessionID, "error", err)
			w.publishCompletionProof(&env, &payload, 0, 0, "NO_TRANSACTIONS")
			return nil
		}

		realmID := ""
		if w.db != nil {
			if session, errSess := w.db.GetCleanupSession(ctx, sessionUUID); errSess == nil && session.RealmID.Valid {
				realmID = session.RealmID.String
			}
		}

		accMap := make(map[string]string)
		if w.db != nil && realmID != "" {
			if accounts, errAcc := w.db.GetAccountsByRealm(ctx, realmID); errAcc == nil {
				for _, acc := range accounts {
					accCode := acc.ID.String()
					if acc.AccountCode.Valid && acc.AccountCode.String != "" {
						accCode = acc.AccountCode.String
					}
					accMap[acc.ID.String()] = accCode
				}
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

		if exportToExcel && (std == "" || std == "PCM") {
			var exportRecs []pcm_cash.ExportRecord
			for _, tx := range txs {
				accountCode := ""
				counterparty := ""

				if tx.PredictedAccountName.Valid && tx.PredictedAccountName.String != "" {
					accountCode = tx.PredictedAccountName.String
				}
				if tx.PredictedVendorName.Valid && tx.PredictedVendorName.String != "" {
					counterparty = tx.PredictedVendorName.String
				} else if tx.MerchantName.Valid && tx.MerchantName.String != "" {
					counterparty = tx.MerchantName.String
				}

				if len(tx.MoroccanEnrichment) > 0 && string(tx.MoroccanEnrichment) != "{}" {
					var enr map[string]interface{}
					if errJSON := json.Unmarshal(tx.MoroccanEnrichment, &enr); errJSON == nil {
						if pcgm, ok := enr["pcgm_accounting"].(map[string]interface{}); ok {
							if acc, ok := pcgm["suggested_account"].(string); ok && acc != "" && accountCode == "" {
								accountCode = acc
							}
						}
						if cp, ok := enr["counterparty"].(map[string]interface{}); ok {
							if name, ok := cp["normalized_name"].(string); ok && name != "" && counterparty == "" {
								counterparty = name
							}
						}
					}
				}

				if len(tx.AseExecutionTrace) > 0 {
					var trace []map[string]interface{}
					if errTrace := json.Unmarshal(tx.AseExecutionTrace, &trace); errTrace == nil {
						for _, step := range trace {
							if step["dag_node_id"] == "account_selection" && accountCode == "" {
								if edge, ok := step["selected_edge"].(string); ok {
									if code, ok := accMap[edge]; ok {
										accountCode = code
									} else {
										accountCode = edge
									}
								}
							}
							if (step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor") && counterparty == "" {
								if edge, ok := step["selected_edge"].(string); ok && edge != "" {
									counterparty = edge
								}
							}
						}
					}
				}

				if accountCode == "" {
					accountCode = "471000"
				}

				dateStr := ""
				if tx.ParsedDate.Valid {
					dateStr = tx.ParsedDate.Time.Format("02/01/2006")
				} else if tx.RawDate.Valid && tx.RawDate.String != "" {
					dateStr = tx.RawDate.String
				} else {
					dateStr = time.Now().Format("02/01/2006")
				}

				desc := ""
				if tx.RawDescription.Valid {
					desc = tx.RawDescription.String
				}

				amountStr := tx.RawAmount
				amountStr = strings.ReplaceAll(amountStr, ",", "")
				amountStr = strings.ReplaceAll(amountStr, " ", "")
				var amount float64
				if f, errParse := strconv.ParseFloat(amountStr, 64); errParse == nil {
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

				stmtType := "BANK_STATEMENT"
				if strings.HasPrefix(accountCode, "5161") {
					stmtType = "PETTY_CASH"
				}

				txUUIDStr := ""
				if tx.ID.Valid {
					txUUIDStr = uuid.UUID(tx.ID.Bytes).String()
				}

				exportRecs = append(exportRecs, pcm_cash.ExportRecord{
					ID:                 txUUIDStr,
					DateStr:            dateStr,
					RawDescription:     desc,
					Amount:             amount,
					Direction:          direction,
					AccountCode:        accountCode,
					AuxiliaryCode:      compteA,
					Counterparty:       counterparty,
					StatementType:      stmtType,
					MoroccanEnrichment: tx.MoroccanEnrichment,
				})
			}

			if xlsxBytes, errXlsx := pcm_cash.GenerateMoroccanBookkeepingWorkbook(exportRecs); errXlsx == nil {
				xlsxBase64 := base64.StdEncoding.EncodeToString(xlsxBytes)
				attachments = append(attachments, map[string]interface{}{
					"Name":        "Bank_Reconciliation_Workbook.xlsx",
					"ContentType": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
					"Content":     xlsxBase64,
				})
			} else {
				w.logger.Error("PcmExportWorker: failed to generate Moroccan Excel workbook", "error", errXlsx)
			}
		}

		if w.db != nil {
			_ = w.db.MarkPcmSessionExported(ctx, sessionUUID)
		}
	}

	if len(attachments) == 0 {
		w.logger.Error("PcmExportWorker: no attachments/data generated for export", "session_id", payload.SessionID)
		w.publishCompletionProof(&env, &payload, len(txs), 0, "EMPTY_ATTACHMENTS")
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

	outgoingEnvelope := core.Envelope{
		ID:             uuid.New().String(),
		ConversationID: env.ConversationID,
		Performative:   core.INFORM,
		Body:           proofBytes,
	}
	envelopeBytes, _ := json.Marshal(outgoingEnvelope)

	if w.nc != nil {
		if err := w.nc.Publish("proof.outgoing.chat", envelopeBytes); err != nil {
			w.logger.Error("PcmExportWorker: failed to publish to proof.outgoing.chat", "error", err)
		}
	}

	// Always emit completion proof back to TAP Orchestrator to unblock subsequent workflow steps
	w.publishCompletionProof(&env, &payload, len(txs), len(attachments), "COMPLETED")

	w.logger.Info("PcmExportWorker: successfully triggered email dispatch and notified TAP Orchestrator",
		"session_id", payload.SessionID,
		"attachments", len(attachments),
		"txs", len(txs),
	)
	return nil
}

func (w *PcmExportWorker) publishCompletionProof(env *core.Envelope, payload *PcmExportPayload, txCount int, attachCount int, status string) {
	if env == nil || env.ConversationID == "" || w.nc == nil {
		return
	}

	proofData := map[string]interface{}{
		"session_id":       payload.SessionID,
		"from_handle":      payload.FromHandle,
		"to_handle":        payload.ToHandle,
		"status":           status,
		"tx_count":         txCount,
		"attachment_count": attachCount,
		"standard":         payload.AccountingStandard,
	}
	proofDataBytes, _ := json.Marshal(proofData)
	proof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      json.RawMessage(proofDataBytes),
	}

	replyEnv, envErr := core.NewEnvelope(
		uuid.New().String(),
		"workers.pcm_export",
		workflows.OrchestratorDID,
		env.ConversationID,
		core.INFORM,
		proof,
	)
	if envErr == nil {
		replyBytes, _ := json.Marshal(replyEnv)
		_ = w.nc.Publish(workflows.OrchestratorInbox, replyBytes)
		w.logger.Info("PcmExportWorker: sent completion proof to TAP Orchestrator", "cid", env.ConversationID, "status", status)
	}
}

func (w *PcmExportWorker) fetchSessionTransactions(ctx context.Context, sessionUUID pgtype.UUID) ([]PcmExportTxRow, error) {
	if w.dbPool != nil {
		rows, err := w.dbPool.Query(ctx, `
			SELECT id, parsed_date, raw_date, raw_description, raw_amount, cash_direction,
			       ase_execution_trace, predicted_account_name, predicted_account_id,
			       predicted_vendor_name, merchant_name, moroccan_enrichment
			FROM fignode.staging_transactions
			WHERE session_id = $1
			ORDER BY row_index ASC, created_at ASC
		`, sessionUUID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var txs []PcmExportTxRow
		for rows.Next() {
			var r PcmExportTxRow
			if err := rows.Scan(
				&r.ID, &r.ParsedDate, &r.RawDate, &r.RawDescription, &r.RawAmount, &r.CashDirection,
				&r.AseExecutionTrace, &r.PredictedAccountName, &r.PredictedAccountID,
				&r.PredictedVendorName, &r.MerchantName, &r.MoroccanEnrichment,
			); err != nil {
				return nil, err
			}
			txs = append(txs, r)
		}
		return txs, nil
	}

	if w.db != nil {
		dbRows, err := w.db.GetPcmSessionTransactions(ctx, sessionUUID)
		if err != nil {
			return nil, err
		}
		var txs []PcmExportTxRow
		for _, dr := range dbRows {
			txs = append(txs, PcmExportTxRow{
				ID:                dr.ID,
				ParsedDate:        dr.ParsedDate,
				RawDate:           dr.RawDate,
				RawDescription:    dr.RawDescription,
				RawAmount:         dr.RawAmount,
				CashDirection:     dr.CashDirection,
				AseExecutionTrace: dr.AseExecutionTrace,
			})
		}
		return txs, nil
	}

	return nil, fmt.Errorf("no database connection available")
}

func generatePCMCSV(txs []PcmExportTxRow, accMap map[string]string) string {
	var csvLines []string
	csvLines = append(csvLines, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit")

	for itemIdx, tx := range txs {
		accountCode := ""
		counterparty := ""

		// 1. Check direct predicted PCGM account from CEE-MA / DAG
		if tx.PredictedAccountName.Valid && tx.PredictedAccountName.String != "" {
			accountCode = tx.PredictedAccountName.String
		}

		// 2. Check direct predicted vendor name
		if tx.PredictedVendorName.Valid && tx.PredictedVendorName.String != "" {
			counterparty = tx.PredictedVendorName.String
		} else if tx.MerchantName.Valid && tx.MerchantName.String != "" {
			counterparty = tx.MerchantName.String
		}

		// 3. Check Moroccan enrichment JSON if available
		if len(tx.MoroccanEnrichment) > 0 && string(tx.MoroccanEnrichment) != "{}" {
			var enr map[string]interface{}
			if err := json.Unmarshal(tx.MoroccanEnrichment, &enr); err == nil {
				if pcgm, ok := enr["pcgm_accounting"].(map[string]interface{}); ok {
					if acc, ok := pcgm["suggested_account"].(string); ok && acc != "" && accountCode == "" {
						accountCode = acc
					}
				}
				if cp, ok := enr["counterparty"].(map[string]interface{}); ok {
					if name, ok := cp["normalized_name"].(string); ok && name != "" && counterparty == "" {
						counterparty = name
					}
				}
			}
		}

		// 4. Fallback to ASE execution trace
		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" && accountCode == "" {
						if edge, ok := step["selected_edge"].(string); ok {
							if code, ok := accMap[edge]; ok {
								accountCode = code
							} else {
								accountCode = edge
							}
						}
					}
					if (step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor") && counterparty == "" {
						if edge, ok := step["selected_edge"].(string); ok && edge != "" {
							counterparty = edge
						}
					}
				}
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

func generateUSGAAPCSV(txs []PcmExportTxRow, accMap map[string]string) string {
	var csvLines []string
	csvLines = append(csvLines, "Journal;Date;Account;Auxiliary;Piece;Description;Debit;Credit")

	for itemIdx, tx := range txs {
		accountName := ""
		counterparty := ""

		if tx.PredictedAccountName.Valid && tx.PredictedAccountName.String != "" {
			accountName = tx.PredictedAccountName.String
		}
		if tx.PredictedVendorName.Valid && tx.PredictedVendorName.String != "" {
			counterparty = tx.PredictedVendorName.String
		} else if tx.MerchantName.Valid && tx.MerchantName.String != "" {
			counterparty = tx.MerchantName.String
		}

		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" && accountName == "" {
						if edge, ok := step["selected_edge"].(string); ok {
							if name, ok := accMap[edge]; ok && name != "" {
								accountName = name
							} else {
								accountName = edge
							}
						}
					}
					if (step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor") && counterparty == "" {
						if edge, ok := step["selected_edge"].(string); ok && edge != "" {
							counterparty = edge
						}
					}
				}
			}
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

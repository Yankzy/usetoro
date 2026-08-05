package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"strconv"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
)

type PcmExportCronWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
	done   chan struct{}
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewPcmExportCronWorker(deps), nil
	})
}

func NewPcmExportCronWorker(deps Dependencies) *PcmExportCronWorker {
	return &PcmExportCronWorker{
		logger: deps.Logger.With("worker", "pcm_export_cron"),
		cfg:    deps.Config,
		nc:     deps.Queue,
		db:     deps.Store.Queries,
		done:   make(chan struct{}),
	}
}

func (w *PcmExportCronWorker) Init(ctx context.Context) error {
	w.logger.Info("PcmExportCronWorker initialized")
	go w.runLoop(ctx)
	return nil
}

func (w *PcmExportCronWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{} // purely a background loop worker
}

func (w *PcmExportCronWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	return nil
}

func (w *PcmExportCronWorker) Stop() {
	close(w.done)
}

func (w *PcmExportCronWorker) runLoop(ctx context.Context) {
	// Every 10 seconds, check for fully classified bank statement sessions.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.ProcessPendingSessions(ctx)
		}
	}
}

func (w *PcmExportCronWorker) ProcessPendingSessions(ctx context.Context) {
	sessions, err := w.db.GetCompletedPcmSessions(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch completed PCM sessions", "error", err)
		return
	}

	for _, session := range sessions {
		err := w.exportSession(ctx, session)
		if err != nil {
			w.logger.Error("Failed to export PCM session", "session_id", session.ID.String(), "error", err)
		}
	}
}

func (w *PcmExportCronWorker) exportSession(ctx context.Context, session database.GetCompletedPcmSessionsRow) error {
	txs, err := w.db.GetPcmSessionTransactions(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("fetch transactions: %w", err)
	}

	if len(txs) == 0 {
		return fmt.Errorf("no transactions found for session %s", session.ID.String())
	}

	accMap := make(map[string]string)
	if w.db != nil && session.RealmID.Valid && session.RealmID.String != "" {
		if accounts, err := w.db.GetAccountsByRealm(ctx, session.RealmID.String); err == nil {
			for _, acc := range accounts {
				accCode := acc.ID.String() // fallback
				if acc.AccountCode.Valid {
					accCode = acc.AccountCode.String
				}
				accMap[acc.ID.String()] = accCode
			}
		} else {
			w.logger.Debug("No DB accounts table lookup, falling back to direct JSON/code lookup", "error", err)
		}
	}

	bankAccountCode := "514100" // Fallback standard Moroccan bank account code
	if session.BankAccountID.Valid {
		if code, ok := accMap[session.BankAccountID.String()]; ok {
			bankAccountCode = code
		}
	}
	_ = bankAccountCode

	// Generate Sage format CSV (Paramétrable PNM style)
	var csvLines []string
	// Use semicolon separator for European/Moroccan Excel compatibility
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

		// Resolve Auxiliary Account (CompteA)
		compteA := extractAuxAccount(desc, counterparty, direction, accountCode)

		// General Account refinement for Bank Journal counterparties
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
			// Outflow (Expense / Payment): Debit the counterpart account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, amount, 0.00))
		} else {
			// Inflow (Revenue / Receipt): Credit the counterpart account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, 0.00, amount))
		}
	}

	csvData := strings.Join(csvLines, "\r\n")
	base64Data := base64.StdEncoding.EncodeToString([]byte(csvData))
	
	payload := map[string]interface{}{
		"session_id": session.ID.String(),
		"attachments": []map[string]interface{}{
			{
				"Name":         "Bank_Reconciliation_Export.csv",
				"ContentType":  "text/csv",
				"Content":      base64Data, 
			},
		},
	}
	
	if session.UserEmail.Valid {
		payload["to_handle"] = session.UserEmail.String
	}

	payloadBytes, _ := json.Marshal(payload)
	envPayload := map[string]interface{}{
		"id":             "cron_" + session.ID.String(),
		"conversation_id": session.ID.String(),
		"performative":   "request",
		"body":           json.RawMessage(payloadBytes),
	}
	envBytes, _ := json.Marshal(envPayload)
	
	if err := w.nc.Publish("worker.inbox.pcm_export", envBytes); err != nil {
		return fmt.Errorf("publish to pcm_export: %w", err)
	}

	if err := w.db.MarkPcmSessionExported(ctx, session.ID); err != nil {
		_ = w.db.MarkPcmSessionExported(ctx, session.ID)
		w.logger.Info("Exported PCM Session", "session_id", session.ID, "user_email", payload["to_handle"])
	}

	return nil
}

// extractAuxAccount formats a string into a valid Sage auxiliary account code (max 10 uppercase alphanumeric chars)
func extractAuxAccount(desc string, counterparty string, direction string, accountCode string) string {
	lowerDesc := strings.ToLower(desc)
	if strings.Contains(lowerDesc, "frais tenue") || strings.Contains(lowerDesc, "agios") || strings.Contains(lowerDesc, "frais dossier") {
		return ""
	}

	rawEntity := counterparty
	if rawEntity == "" {
		cleaned := desc
		for _, prefix := range []string{
			"Virement Client ", "Virement Recu ", "Virement Recu", "Virement ",
			"Paiement CB ", "Paiement ", "Prelevement Facture ", "Prelevement Mensuel ", "Prelevement ",
			"Achats Fournitures ", "Achats ", "Honoraires Cabinet ", "Honoraires ",
		} {
			if strings.HasPrefix(strings.ToLower(cleaned), strings.ToLower(prefix)) {
				cleaned = cleaned[len(prefix):]
				break
			}
		}
		rawEntity = strings.TrimSpace(cleaned)
	}

	for _, suffix := range []string{" SA", " SARL", " IT Solutions", " Casablanca", " Business", " Construction SA", " Industrie SA"} {
		if idx := strings.Index(strings.ToLower(rawEntity), strings.ToLower(suffix)); idx > 0 {
			rawEntity = rawEntity[:idx]
		}
	}
	rawEntity = strings.TrimSpace(rawEntity)

	if rawEntity == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range strings.ToUpper(rawEntity) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	code := b.String()

	prefix := "F_"
	if direction == "INFLOW" || strings.HasPrefix(accountCode, "3") || strings.Contains(lowerDesc, "client") || strings.Contains(lowerDesc, "virement recu") {
		prefix = "C_"
	}

	code = strings.TrimPrefix(code, "CLIENT")


	if len(code) > 8 {
		code = code[:8]
	}
	if code == "" {
		return ""
	}

	res := prefix + code
	if len(res) > 10 {
		res = res[:10]
	}
	return res
}

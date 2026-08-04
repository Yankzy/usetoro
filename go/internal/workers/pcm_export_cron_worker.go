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

	// Fetch Accounts to map to account codes
	accounts, err := w.db.GetAccountsByRealm(ctx, session.RealmID.String)
	if err != nil {
		return fmt.Errorf("fetch accounts: %w", err)
	}
	accMap := make(map[string]string)
	for _, acc := range accounts {
		accCode := acc.ID.String() // fallback
		if acc.AccountCode.Valid {
			accCode = acc.AccountCode.String
		}
		accMap[acc.ID.String()] = accCode
	}

	bankAccountCode := "514100" // Fallback standard Moroccan bank account code
	if session.BankAccountID.Valid {
		if code, ok := accMap[session.BankAccountID.String()]; ok {
			bankAccountCode = code
		}
	}

	// Generate Sage format CSV (Paramétrable PNM style)
	var csvLines []string
	// Use semicolon separator for European/Moroccan Excel compatibility
	csvLines = append(csvLines, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit")

	for _, tx := range txs {
		accountID := ""
		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" {
						if edge, ok := step["selected_edge"].(string); ok {
							accountID = edge
						}
						break
					}
				}
			}
		}
		
		accountCode := ""
		if accountID != "" {
			accountCode = accMap[accountID]
		}

		dateStr := ""
		if tx.ParsedDate.Valid {
			dateStr = tx.ParsedDate.Time.Format("020106") // Sage expects DDMMYY
		} else if tx.RawDate.Valid {
			dateStr = tx.RawDate.String
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
			amount = -amount // Ensure absolute value
		}

		direction := ""
		if tx.CashDirection.Valid {
			direction = strings.ToUpper(tx.CashDirection.String)
		}

		// Clean up description for CSV safety
		desc = strings.ReplaceAll(desc, ";", " ") 
		desc = strings.ReplaceAll(desc, "\"", "")

		journal := "BQ"
		compteA := ""
		piece := "BNK"

		if direction == "OUTFLOW" {
			// Line 1: Debit Expense Account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, amount, 0.00))
			// Line 2: Credit Bank Account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, bankAccountCode, compteA, piece, desc, 0.00, amount))
		} else {
			// Line 1: Debit Bank Account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, bankAccountCode, compteA, piece, desc, amount, 0.00))
			// Line 2: Credit Revenue Account
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
		return fmt.Errorf("mark exported: %w", err)
	}

	w.logger.Info("Exported PCM Session", "session_id", session.ID.String(), "rows", len(txs))
	return nil
}

package reconcile_expense

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"

	"strconv"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type ExpenseReconciliationAgent struct {
	*agent.BaseAgent
	db             *database.Queries
	entityResolver *ai.EntityResolver
}

func init() {
	agents.Register("reconcile-expense-agent", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a ExpenseReconciliationAgent
	a.db = env.Queries
	a.entityResolver = env.EntityResolver

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] reconcile-expense received JetStream message", "topic", msg.Subject)
		if err := a.handleEnrichmentProof(context.Background(), msg); err != nil {
			a.Logger.Error("expense reconcile agent transient error", "error", err)
			if strings.Contains(err.Error(), "insufficient funds") {
				_, _ = a.db.LogStalledMessage(context.Background(), database.LogStalledMessageParams{
					AgentDid:        env.Config.DID,
					OriginalSubject: msg.Subject,
					Payload:         msg.Data,
					ErrorReason:     "Paywall deadlocked: " + err.Error(),
				})
				msg.Term()
				return
			}
			msg.Nak()
			return
		}
		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, env.Memory, "accounting.reconciliation.expense", "proof.accounting.cleanup.enrichment", "reconcile-expense-group", "reconcile-expense-durable", handler)
	return &a
}


func (e *ExpenseReconciliationAgent) handleEnrichmentProof(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return nil
	}
	if env.Performative != core.INFORM {
		return nil
	}
	var proof core.Proof
	if err := json.Unmarshal(env.Body, &proof); err != nil || proof.Type != core.ProofAPI {
		return nil
	}

	sessionID := proof.TaskID
	if sessionID == "" {
		return nil
	}

	var pgSessionID pgtype.UUID
	pgSessionID.Scan(sessionID)

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		e.Logger.Error("expense reconcile: poison pill message exceeded max retries", "session", sessionID)
		e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "ERROR",
		})
		msg.Term()
		return nil
	}

	// Fetch ENRICHED rows for this session
	status := pgtype.Text{String: "ENRICHED", Valid: true}
	rows, err := e.db.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: pgSessionID,
		Status:    status,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch enriched rows: %w", err)
	}

	var expenseRows []database.GetSessionRowsRow
	for _, r := range rows {
		amt := parseDirtyAmount(r.RawAmount)
		if amt <= 0.0 {
			expenseRows = append(expenseRows, r)
		}
	}

	if len(expenseRows) == 0 {
		e.Logger.Info("expense reconcile: no expense rows to process in session", "session", sessionID)
		return nil
	}

	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, 5)

	type updatedRow struct {
		id                  pgtype.UUID
		vendorID            pgtype.UUID
		vendorName          pgtype.Text
		customerID          pgtype.UUID
		customerName        pgtype.Text
		accountID           pgtype.UUID
		accountName         pgtype.Text
		score               pgtype.Numeric
		reasoning           pgtype.Text
		duplicateOf         pgtype.UUID
		isRecurring         bool
		split               []byte
        merchantName        pgtype.Text
        plaidCategory       pgtype.Text
	}

	ch := make(chan updatedRow, len(expenseRows))

	for _, row := range expenseRows {
		row := row
		sem <- struct{}{}
		g.Go(func() error {
			defer func() { <-sem }()

			rowRealm := ""
			if row.RealmID.Valid {
				rowRealm = row.RealmID.String
			}

			aiInput := row.RawDescription.String
			entityInput := aiInput
			if row.PredictedVendorName != "" {
				entityInput = row.PredictedVendorName
			}

			var vID, aID pgtype.UUID
			var vName, aName, reasoning pgtype.Text
			conf := 0.0

			if e.entityResolver != nil && rowRealm != "" && entityInput != "" {
				vMatch, err := e.entityResolver.ResolveVendor(gCtx, rowRealm, entityInput)
				if err == nil && vMatch != nil {
					_ = vID.Scan(vMatch.ID)
					vName = pgtype.Text{String: vMatch.Name, Valid: true}
					conf += vMatch.Score

					aMatch, aErr := e.entityResolver.ResolveAccount(gCtx, rowRealm, "money_out", aiInput, vMatch.Name)
					if aErr == nil && aMatch != nil {
						_ = aID.Scan(aMatch.ID)
						aName = pgtype.Text{String: aMatch.Name, Valid: true}
						reasoning = pgtype.Text{String: fmt.Sprintf("Matched historical '%s' mappings to '%s' with %.0f%% spatial confidence", vMatch.Name, aMatch.Name, aMatch.Score * 100), Valid: true}
						conf += aMatch.Score
					}
					conf = conf / 2.0
				}
			}

			var confScore pgtype.Numeric
			_ = confScore.Scan(fmt.Sprintf("%.4f", conf))

			// Pass through deduplication flags set by Enrichment Agent
			ch <- updatedRow{
				id:                  row.ID,
				vendorID:            vID,
				vendorName:          vName,
				customerID:          row.PredictedCustomerID,
				customerName:        pgtype.Text{String: row.PredictedCustomerName, Valid: row.PredictedCustomerName != ""},
				accountID:           aID,
				accountName:         aName,
				score:               confScore,
				reasoning:           reasoning,
				duplicateOf:         row.DuplicateOf,
				isRecurring:         row.IsRecurring,
				split:               row.SplitSuggestion,
				merchantName:        row.MerchantName,
				plaidCategory:       row.PlaidCategory,
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		e.Logger.Error("expense reconcile: error during concurrency phase", "err", err)
	}
	close(ch)

	for er := range ch {
		err := e.db.UpdateRowEnrichment(ctx, database.UpdateRowEnrichmentParams{
			ID:                    er.id,
			PredictedVendorID:     er.vendorID,
			PredictedVendorName:   er.vendorName,
			PredictedCustomerID:   er.customerID,
			PredictedCustomerName: er.customerName,
			PredictedAccountID:    er.accountID,
			PredictedAccountName:  er.accountName,
			ConfidenceScore:       er.score,
			AiReasoning:           er.reasoning,
			DuplicateOf:           er.duplicateOf,
			IsRecurring:           er.isRecurring,
			SplitSuggestion:       er.split,
			MerchantName:          er.merchantName,
			PlaidCategory:         er.plaidCategory,
		})
		if err != nil {
			e.Logger.Error("expense reconcile: persist row failed", "err", err)
		}
	}

	e.Logger.Info("✅ expense reconciliation complete!", "session", sessionID)

	proofEnv, _ := core.NewEnvelope(uuid.New().String(), e.Cfg.DID, "did:toro:hive", env.ConversationID, core.INFORM, proof)
	proofEnv.Signature = e.KP.Sign(proofEnv.Body)
	finalBytes, _ := json.Marshal(proofEnv)
	return e.Bus.Publish("proof.accounting.cleanup.reconcile.expense", finalBytes)
}

func parseDirtyAmount(amt string) float64 {
	cleaned := strings.ReplaceAll(amt, "*", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(cleaned, 64)
	return v
}

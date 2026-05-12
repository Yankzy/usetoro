package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/workers"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with:
// go run -tags "dev" tap/cmd/rule_engine/main.go --realm <realm_id> --mock-count 4

type editableMockTransaction struct {
	RawAmount      string
	RawDescription string
	MerchantName   string
	Category       string
	SourceAccount  string // 🚨 NEW: To match the double-entry rule requirement
}

var editableTransactions = []editableMockTransaction{
	{
		RawAmount:      "-68.17",
		RawDescription: "Fuel and service stopper",
		MerchantName:   "Chin's Gas and Oil",
		Category:       "General",
		SourceAccount:  "Checking", // Should match Rule #1
	},
	{
		RawAmount:      "-68.17",
		RawDescription: "Fuel and service stopper",
		MerchantName:   "Chin's Gas and Oil",
		Category:       "General",
		SourceAccount:  "Mastercard", // Should match Rule #2
	},
	{
		RawAmount:      "-142.40",
		RawDescription: "Plants and landscaping supplies",
		MerchantName:   "Tania's Nursery",
		Category:       "General",
		SourceAccount:  "Checking", // Should match Rule #4
	},
	{
		RawAmount:      "-33.50",
		RawDescription: "Team lunch",
		MerchantName:   "Bob's Burger Joint",
		Category:       "Food and Drink",
		SourceAccount:  "Checking", // Should match Rule #6
	},
}

type realRuleEngineAdapter struct {
	service      *accounting.RuleEngineService
	persistAudit bool
}

func (a *realRuleEngineAdapter) EvaluateTransaction(ctx context.Context, tx ruleEngine.Transaction) (*accounting.RuleResult, error) {
	return a.service.EvaluateTransaction(ctx, tx)
}

func (a *realRuleEngineAdapter) PersistAuditLog(ctx context.Context, realmID string, transactionID pgtype.UUID, result *accounting.RuleResult) {
	if !a.persistAudit {
		return
	}
	a.service.PersistAuditLog(ctx, realmID, transactionID, result)
}

func main() {
	realmID := flag.String("realm", "", "Realm ID whose real rule groups should be used")
	mockCount := flag.Int("mock-count", 0, "Number of mocked transactions to evaluate")
	persistAudit := flag.Bool("persist-audit", false, "Persist audit logs to DB")
	flag.Parse()

	if *realmID == "" {
		fmt.Println("Usage: rule_engine --realm <realm_id> [--mock-count 4] [--persist-audit]")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	dbCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to parse db config", "error", err)
		os.Exit(1)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbCfg)
	if err != nil {
		logger.Error("failed to connect db", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	queries := database.New(dbPool)
	cache, err := ristretto.NewCache(&ristretto.Config{NumCounters: 1000, MaxCost: 1000, BufferItems: 64})
	if err != nil {
		logger.Error("failed to create cache", "error", err)
		os.Exit(1)
	}
	ruleService := accounting.NewRuleEngineService(logger, queries, cache)

	sessionID := randomUUID()
	store := newMockStagingStore(logger, sessionID, *realmID, *mockCount)
	engine := &realRuleEngineAdapter{service: ruleService, persistAudit: *persistAudit}

	// Create the worker
	worker := workers.NewRuleEvaluationWorker(store, logger, cfg, nil, engine)

	envData, err := buildEnvelope(sessionID)
	if err != nil {
		logger.Error("failed to build envelope", "error", err)
		os.Exit(1)
	}

	msg := &nats.Msg{Subject: "workers.rule_evaluation", Data: envData}
	if err := worker.Handle(ctx, msg); err != nil {
		logger.Error("rule evaluation failed", "error", err)
		os.Exit(1)
	}
}

func buildEnvelope(sessionID pgtype.UUID) ([]byte, error) {
	payload := struct {
		SessionID string `json:"session_id"`
	}{SessionID: sessionID.String()}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	env := core.Envelope{
		ID:             uuid.NewString(),
		Timestamp:      time.Now().UTC(),
		SenderDID:      "did:toro:orchestrator",
		ReceiverDID:    "workers.rule_evaluation",
		Performative:   core.ACCEPT_PROPOSAL,
		ConversationID: "manual-rule-engine",
		Body:           body,
	}
	return json.Marshal(env)
}

type mockStagingStore struct {
	logger       *slog.Logger
	transactions []database.FignodeStagingTransaction
	updates      []database.UpdateStagingTransactionWithRuleParams
	realmID      string
	sessionID    pgtype.UUID
}

func newMockStagingStore(logger *slog.Logger, sessionID pgtype.UUID, realmID string, count int) *mockStagingStore {
	source := editableTransactions
	limit := len(source)
	if count > 0 && count < limit {
		limit = count
	}

	txns := make([]database.FignodeStagingTransaction, 0, limit)
	for i := 0; i < limit; i++ {
		txns = append(txns, newMockTransaction(sessionID, realmID, source[i]))
	}
	return &mockStagingStore{logger: logger, transactions: txns, realmID: realmID, sessionID: sessionID}
}

// These methods satisfy the interface your Worker expects from `database.Queries`
func (s *mockStagingStore) GetPendingStagingTransactions(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
	return s.transactions, nil
}

func (s *mockStagingStore) UpdateStagingTransactionWithRule(ctx context.Context, arg database.UpdateStagingTransactionWithRuleParams) error {
	s.updates = append(s.updates, arg)
	s.logger.Info("MATCH FOUND!",
		"transaction_id", arg.ID,
		"rule_group_id", arg.RuleGroupID.Int32,
		"reasoning", arg.AiReasoning.String,
	)
	return nil
}

func (s *mockStagingStore) GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
	return database.GetCleanupSessionRow{
		ID:      s.sessionID,
		RealmID: pgtype.Text{String: s.realmID, Valid: s.realmID != ""},
	}, nil
}

func newMockTransaction(sessionID pgtype.UUID, realmID string, tx editableMockTransaction) database.FignodeStagingTransaction {
	return database.FignodeStagingTransaction{
		ID:             randomUUID(),
		SessionID:      sessionID,
		RawAmount:      tx.RawAmount,
		RawDescription: pgtype.Text{String: tx.RawDescription, Valid: true},
		RawDate:        pgtype.Date{Time: time.Now().UTC(), Valid: true},
		MerchantName:   pgtype.Text{String: tx.MerchantName, Valid: true},
		Category:       pgtype.Text{String: tx.Category, Valid: true},
	}
}

func randomUUID() pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(uuid.NewString()); err != nil {
		panic(err)
	}
	return id
}

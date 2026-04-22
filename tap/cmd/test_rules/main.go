package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: go run -tags "dev" tap/cmd/test_rules/main.go --realm <realm_id>

func main() {
	realmID := flag.String("realm", "", "The QBO Realm ID to test rules for")
	flag.Parse()

	if *realmID == "" {
		fmt.Println("Usage: test_rules --realm <realm_id>")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("db config error", "error", err)
		os.Exit(1)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		logger.Error("db connection error", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	q := database.New(dbPool)
	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1000,
		MaxCost:     1000,
		BufferItems: 64,
	})

	ruleService := accounting.NewRuleEngineService(logger, q, cache)

	// 1. Fetch Orphaned Purchases
	purchases, err := q.GetOrphanedPurchases(ctx, *realmID)
	if err != nil {
		logger.Error("failed to fetch orphaned purchases", "error", err)
		os.Exit(1)
	}

	logger.Info("Fetched orphaned purchases to test", "count", len(purchases))

	matchCount := 0
	for _, p := range purchases {
		amount, _ := p.TotalAmount.Float64Value()
		tx := ruleEngine.Transaction{
			ID:        fmt.Sprintf("%v", p.ID),
			EntityID:  p.RealmID,
			Amount:    amount.Float64,
			Direction: ruleEngine.Outflow,
			Date:      p.TxnDate.Time,
			Vendor:    p.VendorName.String,
		}

		result, err := ruleService.EvaluateTransaction(ctx, tx)
		if err != nil {
			logger.Warn("Evaluation failed", "tx_id", tx.ID, "error", err)
			continue
		}

		if result != nil && result.MatchedRuleGroupID != nil {
			matchCount++
			logger.Info("✅ Purchase Matched",
				"id", p.ID,
				"amount", amount.Float64,
				"rule_group_id", *result.MatchedRuleGroupID,
				"reason", result.Explanation.HumanReadableReason(),
			)
		} else {
			logger.Debug("❌ Purchase Missed",
				"id", p.ID,
				"amount", amount.Float64,
			)
		}
	}

	logger.Info("Rule Engine Purchase Test Complete", "total_purchases", len(purchases), "total_matches", matchCount)

	// 2. Fetch Orphaned Deposits
	deposits, err := q.GetOrphanedDeposits(ctx, *realmID)
	if err != nil {
		logger.Error("failed to fetch orphaned deposits", "error", err)
		os.Exit(1)
	}

	logger.Info("Fetched orphaned deposits to test", "count", len(deposits))
	depositMatchCount := 0

	for _, d := range deposits {
		amount, _ := d.TotalAmount.Float64Value()
		tx := ruleEngine.Transaction{
			ID:        fmt.Sprintf("%v", d.ID),
			EntityID:  d.RealmID,
			Amount:    amount.Float64,
			Direction: ruleEngine.Inflow,
			Date:      d.TxnDate.Time,
			Customer:  d.CustomerName.String,
		}

		result, err := ruleService.EvaluateTransaction(ctx, tx)
		if err != nil {
			logger.Warn("Evaluation failed", "tx_id", tx.ID, "error", err)
			continue
		}

		if result != nil && result.MatchedRuleGroupID != nil {
			depositMatchCount++
			logger.Info("✅ Deposit Matched",
				"id", d.ID,
				"amount", amount.Float64,
				"rule_group_id", *result.MatchedRuleGroupID,
				"reason", result.Explanation.HumanReadableReason(),
			)
		} else {
			logger.Debug("❌ Deposit Missed",
				"id", d.ID,
				"amount", amount.Float64,
			)
		}
	}

	logger.Info("Rule Engine Deposit Test Complete", "total_deposits", len(deposits), "total_matches", depositMatchCount)
}

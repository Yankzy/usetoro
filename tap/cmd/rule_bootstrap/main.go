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

func main() {
	realmID := flag.String("realm", "", "Realm ID to bootstrap")
	targetRank := flag.Int("rank", 1, "Target rank for consensus")
	minUsage := flag.Int("min", 1, "Minimum usage count")
	flag.Parse()

	if *realmID == "" {
		fmt.Println("Usage: go run -tags 'dev' tap/cmd/rule_bootstrap/main.go --realm <realm_id>")
		os.Exit(1)
	}

	// Use LevelDebug to see everything
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect db", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	queries := database.New(dbPool)
	cache, _ := ristretto.NewCache(&ristretto.Config{NumCounters: 1e7, MaxCost: 1 << 30, BufferItems: 64})
	ruleService := accounting.NewRuleEngineService(logger, queries, cache)

	// Initialize the bootstrapper with our manual config
	bootstrapper := ruleEngine.NewBootstrapper(logger, queries, ruleService).
		WithConfig(*targetRank, *minUsage)

	logger.Info("Starting manual DEPOSIT bootstrap", "realm_id", *realmID)

	err = bootstrapper.RunRuleEngineForDeposits(ctx, *realmID)
	if err != nil {
		logger.Error("Deposit bootstrap failed", "error", err)
		os.Exit(1)
	}

	logger.Info("Manual DEPOSIT bootstrap complete")
}

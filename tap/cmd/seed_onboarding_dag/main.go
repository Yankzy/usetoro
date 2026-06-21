package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

type ConfigFile struct {
	Prompts         map[string]any `yaml:"prompts"`
	DAG             map[string]any `yaml:"dag"`
	HyperParameters map[string]any `yaml:"hyper_parameters"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to parse db config", "error", err)
		os.Exit(1)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	queries := database.New(dbPool)

	// Read the YAML file
	data, err := os.ReadFile("go/internal/erp/ase/onboarding.yml")
	if err != nil {
		logger.Error("failed to read onboarding.yml", "error", err)
		os.Exit(1)
	}

	var parsed ConfigFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		logger.Error("failed to unmarshal yaml", "error", err)
		os.Exit(1)
	}

	dagConfig, _ := json.Marshal(parsed.DAG)
	hyperParams, _ := json.Marshal(parsed.HyperParameters)
	prompts, _ := json.Marshal(parsed.Prompts)

	// Insert into DB as a global DAG (null tenant/realm)
	_, err = queries.UpsertASEConfig(ctx, database.UpsertASEConfigParams{
		TenantID:        pgtype.UUID{Valid: false},
		RealmID:         pgtype.Text{Valid: false},
		Name:            "onboarding",
		DagConfig:       dagConfig,
		HyperParameters: hyperParams,
		Prompts:         prompts,
	})

	if err != nil {
		logger.Error("failed to upsert ase_dags", "error", err)
		os.Exit(1)
	}

	fmt.Println("Successfully seeded onboarding into ase_dags!")
}

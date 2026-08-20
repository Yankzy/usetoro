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
	data, err := os.ReadFile("go/internal/erp/ase/default_inbound_email.yml")
	if err != nil {
		logger.Error("failed to read default_inbound_email.yml", "error", err)
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

	// Insert into DB as a global DAG (null user_id)
	_, err = queries.UpsertASEConfig(ctx, database.UpsertASEConfigParams{
		UserID:          pgtype.UUID{Valid: false},
		Name:            "default_inbound_email",
		DagConfig:       dagConfig,
		HyperParameters: hyperParams,
		Prompts:         prompts,
	})

	if err != nil {
		logger.Error("failed to upsert ase_dags", "error", err)
		os.Exit(1)
	}

	fmt.Println("Successfully seeded default_inbound_email into ase_dags!")
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/toro?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer pool.Close()

	dagNodes := make(map[string]ase.DAGNodeConfig)

	dagNodes["root"] = ase.DAGNodeConfig{
		Kind:              "terminal",
		Name:              "Email Triage Node",
		BatchSize:         50,
		BatchFlushSeconds: 5,
		PromptKey:         "email_specialist",
		EdgeType:          "static",
		Children:          make(map[string]string),
	}

	dagConfig := ase.DAGConfig{
		EntryNode: "root",
		Nodes:     dagNodes,
	}

	dagConfigBytes, err := json.Marshal(dagConfig)
	if err != nil {
		log.Fatalf("failed to marshal dag config: %v", err)
	}

	hyperParams := map[string]interface{}{
		"confidence_threshold":     0.80,
		"auto_advance":             true,
		"max_llm_retries":          3,
		"llm_timeout_seconds":      120,
		"batch_flush_seconds":      5,
		"active_agent_ttl_minutes": 10,
		"lock_ttl_seconds":         30,
	}
	hyperParamsBytes, _ := json.Marshal(hyperParams)

	prompts := map[string]string{
		"email_specialist": "You are an email triage specialist. Classify the intent of this inbound email. Respond with a JSON patch per the rules.",
	}
	promptsBytes, _ := json.Marshal(prompts)

	db := database.New(pool)
	_, err = db.UpsertASEConfig(ctx, database.UpsertASEConfigParams{
		TenantID:        pgtype.UUID{Valid: false},
		RealmID:         pgtype.Text{Valid: false},
		Name:            "email_inbound",
		DagConfig:       dagConfigBytes,
		HyperParameters: hyperParamsBytes,
		Prompts:         promptsBytes,
	})
	if err != nil {
		log.Fatalf("failed to upsert email_inbound config: %v", err)
	}

	fmt.Println("✅ Successfully seeded 'email_inbound' DAG into database!")
}

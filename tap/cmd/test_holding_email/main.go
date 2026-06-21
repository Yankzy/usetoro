package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	q, err := queue.NewClient(cfg.NATS.URL, nats.Name("test-holding-email-cli"), nats.MaxReconnects(-1))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

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

	// Fetch a user from the DB to use as the entity_id
	user, err := queries.GetUserByEmail(ctx, "yankz@fignode.com")
	if err != nil {
		logger.Error("failed to fetch user", "error", err)
		os.Exit(1)
	}

	// Fake an ASE holding event payload to send to general agent ingress
	prompt := fmt.Sprintf("The following transaction requires context gathering to resolve ambiguity:\n\nDescription: %s\nAmount: %s\nCash Direction: %s\nHolding Reason: %s\nTenant ID: %s\nRealm ID: %s\n\nPlease reach out to the user to get clarity on how to categorize this.",
		"Test Holding TXN", "100.00", "OUTFLOW", "Unified Confidence Score below 0.98 structural threshold.", uuid.UUID(user.ID.Bytes).String(), "test-realm")

	payload := map[string]interface{}{
		"prompt":         prompt,
		"entity_id":      uuid.UUID(user.EntityID.Bytes).String(),
		"source":         "email",
		"from_handle":    user.Email,
		"to_handle":      "toro@usetoro.io", // Represents the agent side
		"subject":        "Action Required: Transaction on Hold (Test Holding TXN)",
		"agent_alias":    "general-agent",
		"session_id":     uuid.New().String(),
		"node_id":        uuid.New().String(),
		"hold_reason":    "Unified Confidence Score below 0.98 structural threshold.",
		"cash_direction": "OUTFLOW",
		"description":    "Test Holding TXN",
		"amount":         "100.00",
		"dag_name":       "default",
	}

	payloadBytes, _ := json.Marshal(payload)

	targetSubject := "worker.inbox.general_agent_ingress"
	if err := q.Conn().Publish(targetSubject, payloadBytes); err != nil {
		logger.Error("failed to publish to general agent ingress", "error", err)
		os.Exit(1)
	}

	logger.Info("published holding event payload to general agent ingress", "subject", targetSubject, "payload", string(payloadBytes))
	fmt.Println("Test script complete. Check your email or worker logs to verify email was sent.")
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/internal/workers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("Starting PCM Export CSV Worker Test...")

	if os.Getenv("DATABASE_URL") == "" || strings.Contains(os.Getenv("DATABASE_URL"), "yankz.local") {
		os.Setenv("DATABASE_URL", "postgres://toro:toro_password@localhost:5435/toro?sslmode=disable")
	}
	if os.Getenv("NATS_URL") == "" || strings.Contains(os.Getenv("NATS_URL"), "yankz.local") {
		os.Setenv("NATS_URL", "nats://localhost:4222")
	}
	if os.Getenv("ENCRYPTION_KEY") == "" {
		os.Setenv("ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") // 32 bytes base64 encoded
	}

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Connect to Database
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

	// Connect to NATS
	q, err := nats.Connect(cfg.NATS.URL, nats.Name("test-pcm-export-csv"))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

	// Subscribe to the NATS channel to catch the outgoing email
	sub, err := q.SubscribeSync("worker.inbox.pcm_export")
	if err != nil {
		logger.Error("failed to subscribe to worker.inbox.pcm_export", "error", err)
		os.Exit(1)
	}

	logger.Info("Instantiating PcmExportCronWorker (mock data already in DB)...")

	// Instantiate the worker using the factory pattern manually for testing
	deps := workers.Dependencies{
		Logger: logger,
		Config: cfg,
		Queue:  q,
		Store: &store.Store{
			Queries: database.New(dbPool),
		},
	}

	cronWorker := workers.NewPcmExportCronWorker(deps)

	// Trigger the session processing logic
	logger.Info("Triggering ProcessPendingSessions()...")
	cronWorker.ProcessPendingSessions(ctx)

	// Wait for the NATS message from the worker
	logger.Info("Waiting for NATS message on worker.inbox.pcm_export (up to 15 seconds)...")
	msg, err := sub.NextMsg(15 * time.Second)
	if err != nil {
		logger.Error("Failed to receive NATS message (is there a PENDING completed session in the DB?)", "error", err)
		os.Exit(1)
	}

	logger.Info("Received NATS message!")

	var envPayload struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(msg.Data, &envPayload); err != nil {
		logger.Error("failed to unmarshal envelope", "error", err)
		os.Exit(1)
	}

	var payload struct {
		Attachments []map[string]interface{} `json:"attachments"`
	}
	if err := json.Unmarshal(envPayload.Body, &payload); err != nil {
		logger.Error("failed to unmarshal body", "error", err)
		os.Exit(1)
	}

	if len(payload.Attachments) == 0 {
		logger.Error("No attachments found in payload")
		os.Exit(1)
	}

	contentBase64, _ := payload.Attachments[0]["Content"].(string)
	csvBytes, err := base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		logger.Error("failed to decode base64", "error", err)
		os.Exit(1)
	}

	err = os.WriteFile("test_export_sage.csv", csvBytes, 0644)
	if err != nil {
		logger.Error("Failed to write CSV", "error", err)
		os.Exit(1)
	}

	logger.Info("Successfully created test_export_sage.csv!")
	fmt.Println("\nCSV Contents:\n" + string(csvBytes))
}

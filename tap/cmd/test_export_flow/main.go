package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("Starting Real Export & Email Dispatch Test for yankz@fignode.com...")

	if os.Getenv("DATABASE_URL") == "" || strings.Contains(os.Getenv("DATABASE_URL"), "yankz.local") {
		os.Setenv("DATABASE_URL", "postgres://toro:toro_password@localhost:5435/toro?sslmode=disable")
	}
	if os.Getenv("NATS_URL") == "" || strings.Contains(os.Getenv("NATS_URL"), "yankz.local") {
		os.Setenv("NATS_URL", "nats://localhost:4222")
	}
	if os.Getenv("ENCRYPTION_KEY") == "" {
		os.Setenv("ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	}

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 1. Connect to PostgreSQL
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

	// 2. Connect to NATS
	nc, err := nats.Connect(cfg.NATS.URL, nats.Name("test-export-flow"))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	// 3. Resolve user: yankz@fignode.com
	targetEmail := "yankz@fignode.com"
	user, err := queries.GetUserByEmail(ctx, targetEmail)
	if err != nil {
		logger.Error("failed to find user by email", "email", targetEmail, "error", err)
		os.Exit(1)
	}
	logger.Info("Found user", "email", user.Email, "id", user.ID.String())

	// 4. Fetch the latest staging session for this user
	var sessionID pgtype.UUID
	var realmID string
	err = dbPool.QueryRow(ctx, `
		SELECT id, COALESCE(realm_id, '')
		FROM fignode.staging_sessions
		WHERE created_by = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, user.ID).Scan(&sessionID, &realmID)
	if err != nil {
		logger.Warn("no staging session found for user, fetching latest overall staging session...", "error", err)
		err = dbPool.QueryRow(ctx, `
			SELECT id, COALESCE(realm_id, '')
			FROM fignode.staging_sessions
			ORDER BY created_at DESC
			LIMIT 1
		`).Scan(&sessionID, &realmID)
		if err != nil {
			logger.Error("failed to find any staging session in DB", "error", err)
			os.Exit(1)
		}
	}

	sessionUUIDStr := uuid.UUID(sessionID.Bytes).String()
	logger.Info("Found latest staging session", "session_id", sessionUUIDStr, "realm_id", realmID)

	// 5. Fetch staging transactions for this session
	txs, err := queries.GetPcmSessionTransactions(ctx, sessionID)
	if err != nil {
		logger.Error("failed to fetch staging transactions", "session_id", sessionUUIDStr, "error", err)
		os.Exit(1)
	}
	logger.Info("Fetched staging transactions", "count", len(txs))

	fromAddr := "rap_atlas_sarl@usetoro.io"
	if realmID != "" {
		fromAddr = fmt.Sprintf("%s@usetoro.io", realmID)
	}

	// 6. Build PcmExportPayload for workers.pcm_export
	exportPayload := map[string]interface{}{
		"session_id":          sessionUUIDStr,
		"accounting_standard": "PCM",
		"from_handle":         fromAddr,
		"to_handle":           targetEmail,
		"export_to_email":     true,
		"export_to_csv":       true,
		"export_to_pnm":       true,
	}

	// 7. Publish correctly formatted core.Envelope to NATS for PcmExportWorker processing
	payloadBytes, _ := json.Marshal(exportPayload)
	reqEnvelope := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.REQUEST,
		Body:         payloadBytes,
	}
	reqEnvBytes, _ := json.Marshal(reqEnvelope)

	if err := nc.Publish("worker.inbox.pcm_export", reqEnvBytes); err != nil {
		logger.Error("failed to publish envelope to worker.inbox.pcm_export", "error", err)
		os.Exit(1)
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("  ✅ PCM EXPORT REQUEST PUBLISHED FOR SESSION %s!\n", sessionUUIDStr)
	fmt.Println("=======================================================")
}

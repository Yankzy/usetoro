package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
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

	postmarkToken := os.Getenv("POSTMARK_TRANSACTIONAL_SERVER_TOKEN")
	if postmarkToken == "" {
		postmarkToken = os.Getenv("POSTMARK_SERVER_TOKEN")
	}
	if postmarkToken == "" {
		postmarkToken = cfg.PostmarkServerToken
	}
	if postmarkToken == "" {
		postmarkToken = "d0c8fee1-4f40-43eb-9ab2-a07d74a9b444"
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

	// 6. Build ASENode slice representing the session's micro-agents
	toolDeps := domain_tools.ToolDependencies{
		Logger: logger,
		DB:     queries,
		DBPool: dbPool,
	}
	bookkeepingTool := domain_tools.Get("bookkeeping")

	var agents []*ase.AutonomousSemanticEngineNode
	for _, tx := range txs {
		desc := ""
		if tx.RawDescription.Valid {
			desc = tx.RawDescription.String
		}
		direction := ""
		if tx.CashDirection.Valid {
			direction = tx.CashDirection.String
		}
		agent := ase.NewASENode(user.ID.String(), realmID, "pcm_demo", map[string]any{
			"raw_description": desc,
			"cash_direction":  direction,
			"raw_amount":      tx.RawAmount,
			"domain_tool":     "bookkeeping",
			"session_id":      sessionUUIDStr,
			"from_handle":     targetEmail,
		})
		agent.NodeID = uuid.UUID(tx.ID.Bytes).String()
		if len(tx.AseExecutionTrace) > 0 {
			_ = json.Unmarshal(tx.AseExecutionTrace, &agent.ExecutionTrace)
		}
		agents = append(agents, agent)
	}

	// 7. Run GenerateExportPayload flow
	expTool, ok := bookkeepingTool.(domain_tools.ExportableDomainTool)
	if !ok {
		logger.Error("bookkeeping tool does not implement ExportableDomainTool")
		os.Exit(1)
	}

	exportPayload, activityTarget, err := expTool.GenerateExportPayload(ctx, sessionUUIDStr, agents, toolDeps)
	if err != nil {
		logger.Error("failed to generate export payload", "error", err)
		os.Exit(1)
	}

	logger.Info("Generated export payload!", "activity_target", activityTarget)

	// Explicitly set handles
	exportPayload["to_handle"] = targetEmail
	fromAddr := "rap_atlas_sarl@usetoro.io"
	if realmID != "" {
		fromAddr = fmt.Sprintf("%s@usetoro.io", realmID)
	}
	exportPayload["from_handle"] = fromAddr

	// Print CSV contents to terminal for verification
	var base64CSV string
	if atts, ok := exportPayload["attachments"].([]map[string]interface{}); ok && len(atts) > 0 {
		if contentB64, ok := atts[0]["Content"].(string); ok {
			base64CSV = contentB64
			if decoded, dErr := base64.StdEncoding.DecodeString(contentB64); dErr == nil {
				fmt.Println("\n=======================================================")
				fmt.Println("       GENERATED SAGE 100 PNM (.CSV) EXPORT           ")
				fmt.Println("=======================================================")
				fmt.Println(string(decoded))
				fmt.Println("=======================================================")
			}
		}
	}

	// 8. Publish correctly formatted core.Envelope to NATS for worker processing
	payloadBytes, _ := json.Marshal(exportPayload)
	reqEnvelope := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.REQUEST,
		Body:         payloadBytes,
	}
	reqEnvBytes, _ := json.Marshal(reqEnvelope)

	if err := nc.Publish("worker.inbox.pcm_export", reqEnvBytes); err != nil {
		logger.Error("failed to publish envelope to worker.inbox.pcm_export", "error", err)
	} else {
		logger.Info("Published envelope to NATS subject worker.inbox.pcm_export")
	}

	// 9. Dispatch directly to Postmark HTTP API to ensure email delivery guarantees
	logger.Info("Dispatching email via Postmark API...", "to", targetEmail)

	postmarkBody := map[string]interface{}{
		"From":          fmt.Sprintf(`"Toro Accounting" <%s>`, fromAddr),
		"To":            targetEmail,
		"Subject":       "Sage 100 PNM Export - Bank Reconciliation",
		"TextBody":      "Hello,\n\nPlease find attached your generated Sage 100 (.PNM) Moroccan Bank Reconciliation CSV export.\n\nBest regards,\nToro AI Engine",
		"MessageStream": "outbound",
		"Attachments": []map[string]interface{}{
			{
				"Name":        "Bank_Reconciliation_Export.csv",
				"ContentType": "text/csv",
				"Content":     base64CSV,
			},
		},
	}

	pmBytes, _ := json.Marshal(postmarkBody)
	pmReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(pmBytes))
	if err != nil {
		logger.Error("failed to create postmark request", "error", err)
		os.Exit(1)
	}

	pmReq.Header.Set("Accept", "application/json")
	pmReq.Header.Set("Content-Type", "application/json")
	pmReq.Header.Set("X-Postmark-Server-Token", postmarkToken)

	client := &http.Client{Timeout: 15 * time.Second}
	pmResp, err := client.Do(pmReq)
	if err != nil {
		logger.Error("Postmark API request failed", "error", err)
		os.Exit(1)
	}
	defer pmResp.Body.Close()

	respBody, _ := io.ReadAll(pmResp.Body)
	if pmResp.StatusCode != http.StatusOK {
		logger.Error("Postmark API error", "status", pmResp.StatusCode, "response", string(respBody))
		fmt.Printf("\n❌ Postmark API Error (Status %d): %s\n", pmResp.StatusCode, string(respBody))
		os.Exit(1)
	}

	var pmResult map[string]interface{}
	_ = json.Unmarshal(respBody, &pmResult)

	fmt.Println("\n=======================================================")
	fmt.Printf("  ✅ EMAIL DISPATCH SUCCESSFUL TO %s!\n", targetEmail)
	fmt.Println("=======================================================")
	fmt.Printf("  Message ID : %v\n", pmResult["MessageID"])
	fmt.Printf("  Submitted  : %v\n", pmResult["SubmittedAt"])
	fmt.Printf("  Status     : %v\n", pmResult["Message"])
	fmt.Println("=======================================================")
}

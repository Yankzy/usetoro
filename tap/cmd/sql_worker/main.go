package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Run with: go run tap/cmd/sql_worker/main.go --query "SELECT count(*) FROM shadow_erp.purchases"

func main() {
	query := flag.String("query", "", "The SQL query to execute")
	subject := flag.String("subject", "worker.inbox.sql.execute", "The NATS subject for the SQL worker")
	wait := flag.Bool("wait", true, "Wait for a response")
	flag.Parse()

	if *query == "" {
		fmt.Println("Usage: sql_worker --query \"SELECT ...\" [--subject worker.inbox.sql.execute] [--wait]")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	q, err := queue.NewClient(cfg.NATS.URL, nats.Name("sql-worker-cli"), nats.MaxReconnects(-1))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

	returnSubject := "sql_worker.results." + uuid.New().String()
	var sub *nats.Subscription
	if *wait {
		sub, err = q.Conn().SubscribeSync(returnSubject)
		if err != nil {
			logger.Error("failed to subscribe to return subject", "error", err)
			os.Exit(1)
		}
	}

	payload := struct {
		Query         string `json:"query"`
		ReturnSubject string `json:"return_subject"`
	}{
		Query:         *query,
		ReturnSubject: returnSubject,
	}

	// Wrap in a TAP envelope to test robust parsing
	env, err := core.NewEnvelope(uuid.New().String(), "did:toro:cli", "did:toro:sql-worker", uuid.New().String(), core.INFORM, payload)
	if err != nil {
		logger.Error("failed to create envelope", "error", err)
		os.Exit(1)
	}

	envBytes, _ := json.Marshal(env)

	if err := q.Conn().Publish(*subject, envBytes); err != nil {
		logger.Error("failed to publish sql task", "error", err)
		os.Exit(1)
	}

	logger.Info("published sql task", "query", *query, "subject", *subject, "waiting_on", returnSubject)

	if *wait {
		msg, err := sub.NextMsg(10 * time.Second)
		if err != nil {
			logger.Error("failed to get response", "error", err)
			os.Exit(1)
		}

		var result struct {
			Success bool            `json:"success"`
			Error   string          `json:"error,omitempty"`
			Data    json.RawMessage `json:"data,omitempty"`
		}

		if err := json.Unmarshal(msg.Data, &result); err != nil {
			logger.Error("failed to unmarshal result", "error", err)
			fmt.Println("Raw response:", string(msg.Data))
			os.Exit(1)
		}

		if !result.Success {
			fmt.Printf("❌ Query failed: %s\n", result.Error)
			os.Exit(1)
		}

		fmt.Println("✅ Query successful!")
		fmt.Println(string(result.Data))
	}
}

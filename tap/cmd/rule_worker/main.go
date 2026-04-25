package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Run with: go run -tags "dev" tap/cmd/rule_worker/main.go --realm <realm_id>
// For local mocked rule-evaluation runs, use tap/cmd/rule_engine/main.go.

func main() {
	realmID := flag.String("realm", "", "The QBO Realm ID to bootstrap")
	flag.Parse()

	if *realmID == "" {
		fmt.Println("Usage: rule_worker --realm <realm_id>")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	q, err := queue.NewClient(cfg.NATS.URL, nats.Name("rule-worker-cli"), nats.MaxReconnects(-1))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

	subject, err := core.BuildWorkerInboxFromActivity("workers.rule_bootstrap")
	if err != nil {
		logger.Error("failed to build inbox subject", "error", err)
		os.Exit(1)
	}

	payload := struct {
		RealmID string `json:"realm_id"`
	}{
		RealmID: *realmID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logger.Error("failed to marshal payload", "error", err)
		os.Exit(1)
	}

	if err := q.Conn().Publish(subject, data); err != nil {
		logger.Error("failed to publish bootstrap task", "error", err)
		os.Exit(1)
	}

	logger.Info("published rule bootstrap task", "realm_id", *realmID, "subject", subject)
}

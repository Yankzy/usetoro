// Command dag_test sends an existing PCM staging session directly to ASE.
//
// It deliberately bypasses TAP workflow orchestration and PcmWorker, so it
// never creates a staging session or staging transaction rows.
//
// Example:
//
//	go run tap/cmd/dag_test/main.go -session-id <fignode.staging_transactions.session_id>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

const (
	defaultDAGName    = "pcm_bank_cash_accounting_dag"
	defaultDomainTool = "pcm_cash_accounting"
)

func main() {
	sessionID := flag.String("session-id", "", "Required fignode.staging_transactions.session_id UUID")
	dagName := flag.String("dag-name", defaultDAGName, "ASE DAG configuration name")
	domainTool := flag.String("domain-tool", defaultDomainTool, "ASE domain tool")
	realmID := flag.String("realm-id", "", "Optional realm ID forwarded to ASE")
	entityID := flag.String("entity-id", "", "Optional entity ID forwarded to ASE")
	debugStartNode := flag.String("debug-start-node", "", "Optional DAG node at which to begin")
	debugStopAfterNode := flag.String("debug-stop-after-node", "", "Optional DAG node after which to stop")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *sessionID == "" {
		logger.Error("missing required staging session ID")
		flag.Usage()
		os.Exit(2)
	}
	if _, err := uuid.Parse(*sessionID); err != nil {
		logger.Error("session-id must be a UUID from fignode.staging_transactions.session_id", "session_id", *sessionID, "error", err)
		os.Exit(2)
	}

	// Match the host-friendly default used by the workflow replay command.
	if os.Getenv("NATS_URL") == "" {
		_ = os.Setenv("NATS_URL", "nats://localhost:4222")
	}

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	nc, err := nats.Connect(
		cfg.NATS.URL,
		nats.Name("dag-test-direct-ase-bridge"),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		logger.Error("failed to connect to NATS", "url", cfg.NATS.URL, "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	payload := map[string]any{
		"session_id":            *sessionID,
		"realm_id":              *realmID,
		"entity_id":             *entityID,
		"debug_start_node":      *debugStartNode,
		"debug_stop_after_node": *debugStopAfterNode,
	}
	body, err := json.Marshal(map[string]any{
		"config": map[string]string{
			"dag_name":    *dagName,
			"domain_tool": *domainTool,
		},
		"input": payload,
	})
	if err != nil {
		logger.Error("failed to marshal ASE bridge body", "error", err)
		os.Exit(1)
	}

	envelope, err := core.NewEnvelope(
		uuid.NewString(),
		"did:toro:cli:dag-test",
		"did:toro:worker:ase-bridge",
		*sessionID,
		core.REQUEST,
		json.RawMessage(body),
	)
	if err != nil {
		logger.Error("failed to construct ASE bridge envelope", "error", err)
		os.Exit(1)
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		logger.Error("failed to marshal ASE bridge envelope", "error", err)
		os.Exit(1)
	}

	subject, err := core.BuildWorkerInboxFromActivity("workers.ase_bridge")
	if err != nil {
		logger.Error("failed to derive ASE bridge inbox", "error", err)
		os.Exit(1)
	}
	if err := nc.Publish(subject, envelopeBytes); err != nil {
		logger.Error("failed to publish direct ASE bridge request", "subject", subject, "error", err)
		os.Exit(1)
	}
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		logger.Error("failed to flush direct ASE bridge request", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Published staging session %s directly to %s (DAG: %s, domain tool: %s).\n", *sessionID, subject, *dagName, *domainTool)
}

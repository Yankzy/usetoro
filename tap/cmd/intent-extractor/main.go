package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/agents/intent_extractor"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// noopMemory satisfies core.MemoryStore for agents that don't need RAG/stateful recall.
// IntentExtractor declares dependencies.database=false in defaults.yaml, so a lightweight
// stub keeps the runtime happy without requiring Postgres during local testing.
type noopMemory struct{}

func (noopMemory) Recall(ctx context.Context, realmID, query string) (string, error)     { return "", nil }
func (noopMemory) Learn(ctx context.Context, realmID, trigger, instruction string) error { return nil }

func main() {
	// ── Mode dispatch ──────────────────────────────────────────────────────────
	// Running with --trigger fires an integration CFP instead of starting the agent.
	//   go run ./tap/cmd/intent-extractor/ --trigger -phone "+1..." -text "..."
	if len(os.Args) > 1 && os.Args[1] == "--trigger" {
		os.Args = append(os.Args[:1], os.Args[2:]...) // strip --trigger so flag.Parse works
		runTrigger()
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	natsURL := cfg.NATS.URL
	if override := os.Getenv("NATS_URL"); override != "" {
		natsURL = override
	}
	// Fallback to localhost for dev if config points to a cluster hostname that isn't resolvable.
	connect := func(url string) (*queue.Client, error) {
		return queue.NewClient(url, nats.Name("intent-extractor-test"), nats.MaxReconnects(-1))
	}

	q, err := connect(natsURL)
	if err != nil {
		logger.Warn("primary NATS connection failed, trying localhost", "url", natsURL, "error", err)
		if natsURL != "nats://localhost:4222" {
			if qLocal, errLocal := connect("nats://localhost:4222"); errLocal == nil {
				q = qLocal
			} else {
				logger.Error("failed to connect to localhost NATS", "error", errLocal)
				os.Exit(1)
			}
		} else {
			os.Exit(1)
		}
	}
	defer q.Close()

	// Bootstrap the minimal streams the agent relies on (TASKS, WORKFLOWS, ALMANAC, etc.).
	var streamNames []string
	keys := make([]string, 0, len(cfg.NATS.Services))
	for k := range cfg.NATS.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		svc := cfg.NATS.Services[name]
		if svc.StreamName != "" && len(svc.JetStream.Subjects) > 0 {
			streamCfg := &nats.StreamConfig{
				Name:        svc.StreamName,
				Subjects:    svc.JetStream.Subjects,
				Storage:     nats.FileStorage,
				MaxAge:      svc.JetStream.MaxAge,
				Replicas:    svc.JetStream.Replicas,
				DenyDelete:  svc.JetStream.DenyDelete,
				DenyPurge:   svc.JetStream.DenyPurge,
				AllowRollup: svc.JetStream.AllowRollup,
				AllowDirect: svc.JetStream.AllowDirect,
			}
			if streamCfg.Replicas == 0 {
				streamCfg.Replicas = 1
			}

			if name == "workflows" {
				err = q.EnsureStreamExists(streamCfg)
			} else {
				err = q.EnsureStream(streamCfg)
			}
			if err != nil {
				logger.Error("failed to ensure stream", "name", name, "error", err)
				os.Exit(1)
			}
			streamNames = append(streamNames, svc.StreamName)
		}

		for compName, compCfg := range svc.Components {
			if compCfg.StreamName == "" || len(compCfg.JetStream.Subjects) == 0 {
				continue
			}
			compStream := &nats.StreamConfig{
				Name:        compCfg.StreamName,
				Subjects:    compCfg.JetStream.Subjects,
				Storage:     nats.FileStorage,
				MaxAge:      compCfg.JetStream.MaxAge,
				Replicas:    compCfg.JetStream.Replicas,
				DenyDelete:  compCfg.JetStream.DenyDelete,
				DenyPurge:   compCfg.JetStream.DenyPurge,
				AllowRollup: compCfg.JetStream.AllowRollup,
				AllowDirect: compCfg.JetStream.AllowDirect,
			}
			if compStream.Replicas == 0 {
				compStream.Replicas = 1
			}
			if err := q.EnsureStream(compStream); err != nil {
				logger.Error("failed to ensure component stream", "service", name, "component", compName, "error", err)
				os.Exit(1)
			}
			streamNames = append(streamNames, compCfg.StreamName)
		}
	}

	if err := q.WaitForStreamsReady(streamNames); err != nil {
		logger.Error("jetstream streams not ready", "error", err)
		os.Exit(1)
	}

	// Pull the intent extractor config from defaults.yaml
	var intentCfg *core.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].InternalModule == intentextractor.AgentName {
			intentCfg = &cfg.Agents[i]
			break
		}
	}
	if intentCfg == nil {
		logger.Error("intent extractor config not found in defaults.yaml")
		os.Exit(1)
	}

	normalizedQueue, err := core.NormalizeTaskQueue(intentCfg.ActivityType, intentCfg.TaskQueue)
	if err != nil {
		logger.Error("invalid task queue", "error", err)
		os.Exit(1)
	}
	intentCfg.TaskQueue = normalizedQueue

	bus := agent.NewNatsAdapter(q.Conn(), q.JetStream())

	env := core.Environment{
		Logger: logger.With("agent", intentCfg.InternalModule),
		Bus:    bus,
		Config: *intentCfg,
		Memory: noopMemory{},
	}

	runnable := intentextractor.NewAgent(env)
	if err := runnable.Start(); err != nil {
		logger.Error("agent start failed", "error", err)
		os.Exit(1)
	}
	logger.Info("Intent Extractor agent started", "did", env.Config.DID, "task_queue", env.Config.TaskQueue)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	if err := runnable.Stop(); err != nil {
		logger.Warn("agent shutdown warning", "error", err)
	}
	logger.Info("Intent Extractor agent stopped")
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/workers"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func main() {
	var natsURL string
	flag.StringVar(&natsURL, "nats-url", "", "NATS server URL")
	flag.Parse()

	if natsURL == "" {
		if envURL := os.Getenv("NATS_URL"); envURL != "" {
			natsURL = envURL
		} else {
			natsURL = "nats://127.0.0.1:4222"
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("starting ASE book categorizer parity worker", "nats_url", natsURL)

	// Connect to NATS with retries
	var nc *nats.Conn
	var err error
	for i := 0; i < 10; i++ {
		nc, err = nats.Connect(natsURL, nats.Name("go-parity-worker"), nats.Timeout(2*time.Second))
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	execMode := os.Getenv("PARITY_WORKER_EXECUTION_MODE")
	if execMode == "" {
		execMode = "REAL"
	}

	cfg := &config.Config{}
	worker := workers.NewBookkeepingAseBookCategorizerWorker(logger, cfg, nc)

	var bankWorker *workers.BookkeepingAseBankCategorizerWorker

	if execMode == "STUB" {
		logger.Info("running parity worker in explicit STUB test double mode")
		worker.SetTestClassifier(workers.NewStubTestClassifier())
		worker.SetIdempotencyStoreForTesting(workers.NewMemoryIdempotencyStore())

		bankWorker = workers.NewBookkeepingAseBankCategorizerWorker(logger, cfg, nc)
		bankWorker.SetIdempotencyStoreForTesting(workers.NewMemoryIdempotencyStore())
		if exec, err := workers.NewBankCategorizationASEExecutorWithClassifier(logger, workers.NewStubTestClassifier()); err == nil {
			bankWorker.SetExecutor(exec)
		} else {
			logger.Error("failed to create stub bank executor", "error", err)
		}
	} else {
		logger.Info("running parity worker in REAL MODEL / domain_tools mode")

		var dbPool *pgxpool.Pool
		var dbQueries *database.Queries
		if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
			// If running locally outside docker, replace container hostname torodb:5432 with 127.0.0.1:5435
			if strings.Contains(dbURL, "@torodb:5432") {
				dbURL = strings.Replace(dbURL, "@torodb:5432", "@127.0.0.1:5435", 1)
			} else if strings.Contains(dbURL, "@db:5432") {
				dbURL = strings.Replace(dbURL, "@db:5432", "@127.0.0.1:5435", 1)
			} else if strings.Contains(dbURL, "@torodb:") {
				dbURL = strings.Replace(dbURL, "@torodb:", "@127.0.0.1:", 1)
			}
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			var poolErr error
			dbPool, poolErr = pgxpool.New(dbCtx, dbURL)
			dbCancel()
			if poolErr == nil {
				defer dbPool.Close()
				dbQueries = database.New(dbPool)
				worker.SetDBPool(dbPool)
				worker.SetDB(dbQueries)
				logger.Info("connected to PostgreSQL for candidate account lookup", "candidate_source", "DJANGO_LEDGER_DEFAULT_COA")
			} else {
				logger.Error("failed to connect to PostgreSQL; candidate accounts will fail closed without authoritative CoA", "error", poolErr)
			}
		}

		modelName := os.Getenv("BOOKKEEPING_MODEL_NAME")
		if modelName == "" {
			modelName = os.Getenv("ASE_MODEL_NAME")
		}
		if modelName == "" {
			modelName = "gpt-5.4-mini"
		}

		js, _ := nc.JetStream()
		adapter := agent.NewNatsAdapter(nc, js)
		rt := agent.NewRuntime(logger, adapter, core.AgentConfig{
			Model: modelName,
			DID:   "did:toro:parity:worker",
		})
		worker.SetRuntime(rt)

		bankDeps := workers.Dependencies{
			Logger:  logger,
			Config:  cfg,
			Queue:   nc,
			DBPool:  dbPool,
			Runtime: rt,
		}
		bankWorker = workers.NewBookkeepingAseBankCategorizerWorkerWithDeps(bankDeps)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if execMode != "STUB" {
		if err := worker.Init(ctx); err != nil {
			logger.Warn("failed to initialize book worker idempotency store", "error", err)
		}
		if err := bankWorker.Init(ctx); err != nil {
			logger.Warn("failed to initialize bank worker idempotency store", "error", err)
		}
	}

	// Subscribe to BookCategorizeSubject
	sub, err := nc.QueueSubscribe(workers.BookCategorizeSubject, workers.BookCategorizeQueueGroup, func(msg *nats.Msg) {
		if handleErr := worker.Handle(ctx, msg); handleErr != nil {
			logger.Error("error handling book categorize message", "error", handleErr)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to book categorize subject", "error", err)
		os.Exit(1)
	}
	defer sub.Unsubscribe()

	// Subscribe to BankCategorizeSubject
	bankSub, err := nc.QueueSubscribe(workers.BankCategorizeSubject, workers.BankCategorizeQueueGroup, func(msg *nats.Msg) {
		if handleErr := bankWorker.Handle(ctx, msg); handleErr != nil {
			logger.Error("error handling bank categorize message", "error", handleErr)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to bank categorize subject", "error", err)
		os.Exit(1)
	}
	defer bankSub.Unsubscribe()

	// Flush connection to ensure subscription is registered on the broker
	if err := nc.Flush(); err != nil {
		logger.Error("failed to flush NATS connection", "error", err)
		os.Exit(1)
	}

	// Output exact ready marker for process orchestrators
	fmt.Println("PARITY_WORKER_READY")
	os.Stdout.Sync()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutting down ASE book categorizer parity worker")
}

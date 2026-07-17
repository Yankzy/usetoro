package workers

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"log/slog"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
)

type WarmupOrchestratorWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewWarmupOrchestratorWorker(deps.Store.Queries, deps.Queue, deps.Logger), nil
	})
}

func NewWarmupOrchestratorWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger) *WarmupOrchestratorWorker {
	return &WarmupOrchestratorWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "warmup_orchestrator"),
	}
}

func (w *WarmupOrchestratorWorker) Init(ctx context.Context) error {
	return nil
}

func (w *WarmupOrchestratorWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "ase.events.warmup.orchestrate",
			Group:   "warmup-orchestrator-group",
		},
	}
}

var anchorEmails = []string{
	"qubzen@gmail.com",
	"jakefaniogst@gmail.com",
	"dojopayroll@gmail.com",
	"yankz@fignode.com",
	"afroswaps@gmail.com",
}

type WarmupDispatchEvent struct {
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToEmail       string    `json:"to_email"`
}

func (w *WarmupOrchestratorWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("running daily warmup orchestrator check")

	accounts, err := w.db.GetAllWarmingEmailAccounts(ctx)
	if err != nil {
		w.logger.Error("failed to get warming accounts", "err", err)
		return nil
	}

	for _, account := range accounts {
		var ageDays int
		if account.DomainCreatedAt.Valid {
			ageDays = int(time.Since(account.DomainCreatedAt.Time).Hours() / 24)
		} else {
			ageDays = 0
		}

		if ageDays < 14 {
			w.logger.Info("domain too young for warmup", "account", account.Email, "age_days", ageDays)
			continue
		}

		warmDays := ageDays - 14
		var phase int
		var dailyLimit int32

		if warmDays <= 7 {
			phase = 1
			dailyLimit = int32(rand.Intn(6) + 5) // 5-10
		} else if warmDays <= 14 {
			phase = 2
			dailyLimit = int32(rand.Intn(11) + 10) // 10-20
		} else if warmDays <= 21 {
			phase = 3
			dailyLimit = int32(rand.Intn(11) + 20) // 20-30
		} else if warmDays <= 28 {
			phase = 4
			dailyLimit = int32(rand.Intn(21) + 30) // 30-50
		} else {
			phase = 5
			dailyLimit = 50 // Cap at 50 for max phase
		}

		err = w.db.UpdateEmailAccountWarmup(ctx, database.UpdateEmailAccountWarmupParams{
			DailySendLimit: dailyLimit,
			WarmupPhase:    int32(phase),
			BounceCount:    account.BounceCount,
			SpamComplaints: account.SpamComplaints,
			Status:         account.Status,
			ID:             account.ID,
		})
		if err != nil {
			w.logger.Error("failed to update warmup account", "err", err)
			continue
		}

		remainingSends := dailyLimit - account.DailySendCount
		if remainingSends <= 0 {
			w.logger.Info("warmup daily limit reached", "account", account.Email)
			continue
		}

		for i := 0; int32(i) < remainingSends; i++ {
			targetEmail := anchorEmails[rand.Intn(len(anchorEmails))]

			payload := WarmupDispatchEvent{
				FromAccountID: account.ID.Bytes,
				ToEmail:       targetEmail,
			}

			payloadBytes, _ := json.Marshal(payload)
			err = w.nc.Publish("email.warmup.generate", payloadBytes)
			if err != nil {
				w.logger.Error("failed to publish warmup event", "err", err)
			}
		}

		w.logger.Info("queued warmup emails", "account", account.Email, "count", remainingSends)
	}

	return nil
}

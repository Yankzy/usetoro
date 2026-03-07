package fignode

import (
	"context"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
)

type Workers struct {
	db          *database.Queries
	leaderboard *LeaderboardService
	streak      *StreakService
	logger      *slog.Logger
}

func NewWorkers(
	db *database.Queries,
	leaderboard *LeaderboardService,
	streak *StreakService,
	logger *slog.Logger,
) *Workers {
	return &Workers{db: db, leaderboard: leaderboard, streak: streak, logger: logger}
}

func (w *Workers) Start(ctx context.Context) {
	go w.runTicker(ctx, "leaderboard-refresh", 5*time.Minute, w.leaderboard.RefreshAll)
	go w.runDailyMidnight(ctx)
}

func (w *Workers) runTicker(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.logger.Info("worker started", "worker", name, "interval", interval)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker stopped", "worker", name)
			return
		case <-ticker.C:
			if err := fn(ctx); err != nil {
				w.logger.Error("worker tick failed", "worker", name, "error", err)
			}
		}
	}
}

// runDailyMidnight ticks every minute and fires the reset once per UTC day.
func (w *Workers) runDailyMidnight(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastReset time.Time
	w.logger.Info("worker started", "worker", "streak-reset")
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker stopped", "worker", "streak-reset")
			return
		case t := <-ticker.C:
			today := t.UTC().Truncate(24 * time.Hour)
			if today.After(lastReset) {
				if err := w.streak.MidnightReset(ctx); err != nil {
					w.logger.Error("midnight reset failed", "error", err)
					continue
				}
				lastReset = today
				w.logger.Info("midnight reset completed")
			}
		}
	}
}

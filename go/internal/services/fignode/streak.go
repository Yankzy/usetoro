package fignode

import (
	"context"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
)

type StreakService struct {
	db     *database.Queries
	logger *slog.Logger
}

func NewStreakService(db *database.Queries, logger *slog.Logger) *StreakService {
	return &StreakService{db: db, logger: logger}
}

// MidnightReset zeroes stale streaks and resets daily cleared counters.
func (s *StreakService) MidnightReset(ctx context.Context) error {
	if err := s.db.ResetStaleStreaks(ctx); err != nil {
		s.logger.Error("reset stale streaks failed", "error", err)
		return err
	}
	if err := s.db.ResetTodayCleared(ctx); err != nil {
		s.logger.Error("reset today_cleared failed", "error", err)
		return err
	}
	return nil
}

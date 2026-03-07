package fignode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type LeaderboardService struct {
	db     *database.Queries
	logger *slog.Logger
}

func NewLeaderboardService(db *database.Queries, logger *slog.Logger) *LeaderboardService {
	return &LeaderboardService{db: db, logger: logger}
}

func (s *LeaderboardService) RefreshAll(ctx context.Context) error {
	for _, period := range []string{"daily", "weekly", "all-time"} {
		if err := s.refreshPeriod(ctx, period); err != nil {
			s.logger.Error("leaderboard refresh failed", "period", period, "error", err)
			continue
		}
	}
	return nil
}

func (s *LeaderboardService) refreshPeriod(ctx context.Context, period string) error {
	var items []LeaderboardItem

	switch period {
	case "all-time":
		rows, err := s.db.ComputeAllTimeLeaderboard(ctx)
		if err != nil {
			return err
		}

		userIDs := make([]pgtype.UUID, len(rows))
		for i, r := range rows {
			userIDs[i] = r.UserID
		}
		badgeMap := s.buildBadgeMap(ctx, userIDs)

		for i, r := range rows {
			uid := uuid.UUID(r.UserID.Bytes).String()
			items = append(items, LeaderboardItem{
				Rank:    i + 1,
				Email:   r.Email,
				Cleared: int(r.Cleared),
				Streak:  int(r.Streak),
				Badges:  badgeMap[uid],
			})
		}

	case "daily", "weekly":
		var since time.Time
		now := time.Now().UTC()
		if period == "daily" {
			since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		} else {
			since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -7)
		}

		rows, err := s.db.ComputePeriodLeaderboard(ctx, pgtype.Timestamptz{Time: since, Valid: true})
		if err != nil {
			return err
		}

		userIDs := make([]pgtype.UUID, len(rows))
		for i, r := range rows {
			userIDs[i] = r.UserID
		}
		badgeMap := s.buildBadgeMap(ctx, userIDs)

		for i, r := range rows {
			uid := uuid.UUID(r.UserID.Bytes).String()
			items = append(items, LeaderboardItem{
				Rank:    i + 1,
				Email:   r.Email,
				Cleared: int(r.Cleared),
				Streak:  int(r.Streak),
				Badges:  badgeMap[uid],
			})
		}
	default:
		return fmt.Errorf("unknown period: %s", period)
	}

	data, err := json.Marshal(items)
	if err != nil {
		return err
	}

	return s.db.InsertLeaderboardSnapshot(ctx, database.InsertLeaderboardSnapshotParams{
		Period:  period,
		Entries: data,
	})
}

func (s *LeaderboardService) buildBadgeMap(ctx context.Context, userIDs []pgtype.UUID) map[string][]string {
	result := make(map[string][]string)
	if len(userIDs) == 0 {
		return result
	}

	badges, err := s.db.GetBadgesByUserIDs(ctx, userIDs)
	if err != nil {
		s.logger.Error("failed to fetch badges for leaderboard", "error", err)
		return result
	}

	for _, b := range badges {
		uid := uuid.UUID(b.UserID.Bytes).String()
		result[uid] = append(result[uid], b.BadgeLabel)
	}
	return result
}

func (s *LeaderboardService) GetLeaderboard(ctx context.Context, period string, currentUserID uuid.UUID) ([]LeaderboardItem, error) {
	if period != "daily" && period != "weekly" && period != "all-time" {
		return nil, &appError{Code: "INVALID_PERIOD", Message: "Period must be daily, weekly, or all-time", Status: 400}
	}

	snapshot, err := s.db.GetLatestLeaderboardSnapshot(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}

	var items []LeaderboardItem
	if err := json.Unmarshal(snapshot.Entries, &items); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	// Look up current user's email
	pgUID := pgtype.UUID{Bytes: currentUserID, Valid: true}
	employee, err := s.db.GetEmployeeByID(ctx, pgUID)
	if err == nil {
		for i := range items {
			if items[i].Email == employee.Email {
				items[i].IsCurrentUser = true
			}
		}
	}

	return items, nil
}

package fignode

import (
	"context"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

type badgeDef struct {
	Key       string
	Label     string
	Predicate func(profile database.FignodeEmployeeProfile) bool
}

var badgeDefs = []badgeDef{
	{"STREAK_7", "7 Day Streak", func(p database.FignodeEmployeeProfile) bool { return p.Streak >= 7 }},
	{"STREAK_30", "30 Day Streak", func(p database.FignodeEmployeeProfile) bool { return p.Streak >= 30 }},
	{"STREAK_100", "100 Streak", func(p database.FignodeEmployeeProfile) bool { return p.Streak >= 100 }},
	{"CLEARED_100", "100 Cleared", func(p database.FignodeEmployeeProfile) bool { return p.TotalCleared >= 100 }},
	{"CLEARED_1000", "1K Cleared", func(p database.FignodeEmployeeProfile) bool { return p.TotalCleared >= 1000 }},
}

func CheckAndAwardBadges(ctx context.Context, db *database.Queries, userID pgtype.UUID, profile database.FignodeEmployeeProfile) error {
	for _, bd := range badgeDefs {
		if bd.Predicate(profile) {
			if err := db.UpsertBadge(ctx, database.UpsertBadgeParams{
				UserID:     userID,
				BadgeKey:   bd.Key,
				BadgeLabel: bd.Label,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

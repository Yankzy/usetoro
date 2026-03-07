package fignode

import (
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
)

func TestBadgePredicates(t *testing.T) {
	tests := []struct {
		name       string
		profile    database.FignodeEmployeeProfile
		expectKeys []string
	}{
		{
			name: "no badges",
			profile: database.FignodeEmployeeProfile{
				Streak:       5,
				TotalCleared: 50,
			},
			expectKeys: []string{},
		},
		{
			name: "7 day streak only",
			profile: database.FignodeEmployeeProfile{
				Streak:       7,
				TotalCleared: 50,
			},
			expectKeys: []string{"STREAK_7"},
		},
		{
			name: "100 cleared only",
			profile: database.FignodeEmployeeProfile{
				Streak:       0,
				TotalCleared: 100,
			},
			expectKeys: []string{"CLEARED_100"},
		},
		{
			name: "mega employee",
			profile: database.FignodeEmployeeProfile{
				Streak:       105,
				TotalCleared: 1050,
			},
			expectKeys: []string{"STREAK_7", "STREAK_30", "STREAK_100", "CLEARED_100", "CLEARED_1000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var earned []string
			for _, def := range badgeDefs {
				if def.Predicate(tt.profile) {
					earned = append(earned, def.Key)
				}
			}

			if len(earned) != len(tt.expectKeys) {
				t.Errorf("expected %d badges, got %d", len(tt.expectKeys), len(earned))
			}

			// check all expected were earned
			earnedMap := make(map[string]bool)
			for _, e := range earned {
				earnedMap[e] = true
			}
			for _, exp := range tt.expectKeys {
				if !earnedMap[exp] {
					t.Errorf("expected to earn badge %s but didn't", exp)
				}
			}
		})
	}
}

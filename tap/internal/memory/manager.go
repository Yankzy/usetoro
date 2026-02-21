package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/tap/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Manager struct {
	DB *pgxpool.Pool
	Q  *database.Queries
}

func NewManager(db *pgxpool.Pool) *Manager {
	return &Manager{
		DB: db,
		Q:  database.New(db),
	}
}

// Learn saves a human correction as a permanent rule
func (m *Manager) Learn(ctx context.Context, realmID, trigger, instruction string) error {
	// The new schema relies purely on RealmID (QBO Company ID)
	return m.Q.CreateMemoryRule(ctx, database.CreateMemoryRuleParams{
		RealmID:     realmID,
		EntityValue: trigger,
		Instruction: instruction,
	})
}

// Recall fetches relevant rules to inject into the Context Window
func (m *Manager) Recall(ctx context.Context, realmID string, input string) (string, error) {
	// 1. Simple Keyword Extraction (Naive implementation)
	// In production, you might run a lightweight tokenizer or regex here.
	// For now, we assume the input might CONTAIN the vendor name.

	// We fetch ALL rules for this realm and filter in Go (assuming < 1000 rules per realm)
	rows, err := m.Q.GetMemoryRules(ctx, realmID)
	if err != nil {
		return "", err
	}
	// defer rows.Close() -- sqlc handles closing/scanning in GetMemoryRules usually returns slice

	var rulesBuilder strings.Builder
	foundRules := false

	for _, rule := range rows {
		trigger := rule.EntityValue
		instruction := rule.Instruction

		// Does the input text contain this vendor/keyword?
		if strings.Contains(strings.ToLower(input), strings.ToLower(trigger)) {
			if !foundRules {
				rulesBuilder.WriteString("\n🧠 RECALLED MEMORY (USER RULES):\n")
				foundRules = true
			}
			rulesBuilder.WriteString(fmt.Sprintf("- When you see '%s', you MUST %s\n", trigger, instruction))
		}
	}

	return rulesBuilder.String(), nil
}

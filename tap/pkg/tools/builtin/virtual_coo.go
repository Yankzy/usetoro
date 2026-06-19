package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/vcoo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type UpdateVirtualCOORestrictionTool struct {
	Queries *database.Queries
	Logger  *slog.Logger
}

func (t *UpdateVirtualCOORestrictionTool) Name() string {
	return "UpdateVirtualCOORestriction"
}

func (t *UpdateVirtualCOORestrictionTool) Description() string {
	return "Updates the Virtual COO restriction protocol, weekly velocity index, and updates brain/founder_state.md accordingly."
}

func (t *UpdateVirtualCOORestrictionTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id": {"type": "string", "description": "The UUID of the founder user to update."},
			"velocity_delta": {"type": "number", "description": "The computed weekly velocity delta (e.g. -0.12 or 0.05)."},
			"apply_multiplier": {"type": "boolean", "description": "True to trigger active System State Restriction protocol (1.5x multiplier and freeze refactoring), false to return to normal targets."}
		},
		"required": ["user_id", "velocity_delta", "apply_multiplier"]
	}`)
}

func (t *UpdateVirtualCOORestrictionTool) Call(ctx context.Context, input map[string]any) (string, error) {
	if t.Queries == nil {
		return "", fmt.Errorf("database queries not initialized in environment")
	}

	userIDStr, _ := input["user_id"].(string)
	velocityDelta, _ := input["velocity_delta"].(float64)
	applyMultiplier, _ := input["apply_multiplier"].(bool)

	parsedUUID, err := uuid.Parse(userIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid user_id UUID: %w", err)
	}

	pgUID := pgtype.UUID{Bytes: parsedUUID, Valid: true}
	userData, err := t.Queries.GetUserVCOOData(ctx, pgUID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch user VCOO data: %w", err)
	}

	// 1. Parse/load current VCOO state
	var vState tools.VCOOStateStruct
	if len(userData.VcooState) > 0 && string(userData.VcooState) != "{}" {
		_ = json.Unmarshal(userData.VcooState, &vState)
	}

	// Update state parameters
	vState.VelocityDelta = velocityDelta
	vState.RestrictionActive = applyMultiplier
	if applyMultiplier {
		vState.Multiplier = 1.5
	} else {
		vState.Multiplier = 1.0
	}
	vState.LastVelocityUpdate = time.Now().Format("2006-01-02")

	// 2. Perform markdown transition if not restricted
	tenantID := uuidToString(userData.EntityID)
	filePath := filepath.Join("docs", "knowledge", tenantID, "playbooks", "founder_state.md")
	if _, err := os.Stat(filePath); err == nil {
		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", filePath, err)
		}

		state, err := vcoo.ParseFounderState(string(contentBytes))
		if err != nil {
			return "", fmt.Errorf("failed to parse %s: %w", filePath, err)
		}

		if !applyMultiplier {
			// Unlock the next BACKLOG task
			unlocked := false
			for i, tk := range state.Tasks {
				if tk.Status == "BACKLOG" {
					state.Tasks[i].Status = "ACTIVE"
					unlocked = true
					t.Logger.Info("VCOO: unlocked backlog task", "task_id", tk.ID)
					break
				}
			}

			if unlocked {
				newContent := vcoo.FormatFounderState(state)
				// Ensure directory exists
				if err := os.MkdirAll(filepath.Dir(filePath), 0755); err == nil {
					_ = os.WriteFile(filePath, []byte(newContent), 0644)
				}
			}
		}
	} else {
		t.Logger.Warn("VCOO: state file not found, skipping markdown update", "path", filePath)
	}

	// 3. Write back to DB
	newStateBytes, _ := json.Marshal(vState)
	err = t.Queries.UpdateUserVCOOData(ctx, database.UpdateUserVCOODataParams{
		ID:                 userData.ID,
		VcooActiveBlockers: userData.VcooActiveBlockers,
		VcooHistory:        userData.VcooHistory,
		VcooState:          newStateBytes,
	})
	if err != nil {
		return "", fmt.Errorf("failed to update user VCOO DB state: %w", err)
	}

	statusStr := "NORMAL"
	if applyMultiplier {
		statusStr = "RESTRICTION ACTIVE (1.5x Multiplier)"
	}

	return fmt.Sprintf("Successfully updated VCOO state. Status: %s, Velocity Delta: %.2f", statusStr, velocityDelta), nil
}

func init() {
	Register("UpdateVirtualCOORestriction", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &UpdateVirtualCOORestrictionTool{
			Queries: env.Queries,
			Logger:  logger,
		}
	})
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

type CalculateVelocityTool struct {
	Logger *slog.Logger
}

func (t *CalculateVelocityTool) Name() string {
	return "CalculateVelocity"
}

func (t *CalculateVelocityTool) Description() string {
	return "Computes the velocity index delta based on actual logged activities, target benchmarks, and a specified scaling time horizon."
}

func (t *CalculateVelocityTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"targets": {
				"type": "object",
				"description": "Key-value map of baseline weekly target benchmarks (e.g. {\"OUTBOUND_CALLS\": 250}).",
				"additionalProperties": {"type": "number"}
			},
			"actuals": {
				"type": "object",
				"description": "Key-value map of aggregated actual metrics logged during the horizon (e.g. {\"outbound_voip_dials\": 40}).",
				"additionalProperties": {"type": "integer"}
			},
			"horizon": {
				"type": "string",
				"description": "The time window for scaling targets: \"hour\", \"daily\", \"weekly\", \"quarterly\", \"yearly\".",
				"enum": ["hour", "daily", "weekly", "quarterly", "yearly"]
			}
		},
		"required": ["targets", "actuals", "horizon"]
	}`)
}

func (t *CalculateVelocityTool) Call(ctx context.Context, input map[string]any) (string, error) {
	targetsRaw, ok := input["targets"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing or invalid 'targets' map")
	}
	actualsRaw, ok := input["actuals"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing or invalid 'actuals' map")
	}
	horizon, _ := input["horizon"].(string)
	if horizon == "" {
		horizon = "weekly"
	}

	targets := make(map[string]float64)
	for k, v := range targetsRaw {
		if val, ok := v.(float64); ok {
			targets[k] = val
		}
	}

	actuals := make(map[string]int)
	for k, v := range actualsRaw {
		if val, ok := v.(float64); ok {
			actuals[k] = int(val)
		} else if val, ok := v.(int); ok {
			actuals[k] = val
		}
	}

	delta := tools.CalculateVelocityDelta(actuals, targets, horizon)
	return fmt.Sprintf("Calculated Velocity Delta: %.4f", delta), nil
}

func init() {
	Register("CalculateVelocity", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &CalculateVelocityTool{
			Logger: logger,
		}
	})
}

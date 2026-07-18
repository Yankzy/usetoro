package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
)

// LLMTurnTelemetry mirrors the struct in tap/pkg/agent/runtime.go
type LLMTurnTelemetry struct {
	Context struct {
		ConversationID string `json:"conversation_id"`
		DAGID          string `json:"dag_id"`
		NodeID         string `json:"node_id"`
		TenantID       string `json:"tenant_id"`
		AgentID        string `json:"agent_id"`
		CampaignID     string `json:"campaign_id"`
		ProspectID     string `json:"prospect_id"`
		StepNumber     int    `json:"step_number"`
	} `json:"context"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
}

type LLMTelemetryWorker struct {
	db     *database.Queries
	logger *slog.Logger
}

func NewLLMTelemetryWorker(db *database.Queries, logger *slog.Logger) *LLMTelemetryWorker {
	return &LLMTelemetryWorker{
		db:     db,
		logger: logger.With("worker", "llm_telemetry"),
	}
}

func (w *LLMTelemetryWorker) Init(ctx context.Context) error {
	w.logger.Info("LLM Telemetry worker initialized")
	return nil
}

func (w *LLMTelemetryWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "llm.telemetry.turn_completed",
			Group:   "llm_telemetry_worker",
			Options: []nats.SubOpt{nats.ManualAck()},
		},
	}
}

func parseUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	if s != "" {
		_ = u.Scan(s)
	}
	return u
}

func (w *LLMTelemetryWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var payload LLMTurnTelemetry
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("Failed to unmarshal llm telemetry", "error", err, "data", string(msg.Data))
		return err
	}

	// 1. Look up pricing
	pricing, err := w.db.GetLLMPricingModel(ctx, payload.Model)
	var cost pgtype.Numeric

	if err != nil {
		w.logger.Warn("Failed to find pricing for model, setting cost to null", "model", payload.Model, "error", err)
		cost = pgtype.Numeric{Int: nil, Valid: false}
	} else {
		var inputCostPer1M, outputCostPer1M float64
		if pricing.InputCostPer1m.Valid {
			if fval, err := pricing.InputCostPer1m.Float64Value(); err == nil {
				inputCostPer1M = fval.Float64
			}
		}
		if pricing.OutputCostPer1m.Valid {
			if fval, err := pricing.OutputCostPer1m.Float64Value(); err == nil {
				outputCostPer1M = fval.Float64
			}
		}
		
		totalCost := (float64(payload.InputTokens)*inputCostPer1M + float64(payload.OutputTokens)*outputCostPer1M) / 1000000.0
		err = cost.Scan(totalCost)
		if err != nil {
			w.logger.Error("Failed to convert calculated cost to pgtype.Numeric", "error", err)
		}
	}

	var metadata []byte
	metadata = msg.Data // Storing the raw payload as metadata for now since we don't have other metadata fields

	// 2. Insert into llm_turn_metrics
	_, err = w.db.InsertLLMTurnMetric(ctx, database.InsertLLMTurnMetricParams{
		TenantID:        parseUUID(payload.Context.TenantID),
		ConversationID:  parseUUID(payload.Context.ConversationID),
		DagID:           pgtype.Text{String: payload.Context.DAGID, Valid: payload.Context.DAGID != ""},
		NodeID:          pgtype.Text{String: payload.Context.NodeID, Valid: payload.Context.NodeID != ""},
		AgentID:         pgtype.Text{String: payload.Context.AgentID, Valid: payload.Context.AgentID != ""},
		Model:           payload.Model,
		Provider:        pgtype.Text{String: payload.Provider, Valid: payload.Provider != ""},
		InputTokens:     int32(payload.InputTokens),
		OutputTokens:    int32(payload.OutputTokens),
		TotalTokens:     int32(payload.TotalTokens),
		CostUsd:         cost,
		CampaignID:      parseUUID(payload.Context.CampaignID),
		ProspectID:      parseUUID(payload.Context.ProspectID),
		StepNumber:      pgtype.Int4{Int32: int32(payload.Context.StepNumber), Valid: payload.Context.StepNumber > 0},
		Metadata:        metadata,
	})

	if err != nil {
		w.logger.Error("Failed to insert LLM turn metric", "error", err)
		return err
	}

	w.logger.Debug("Recorded LLM turn metric", "model", payload.Model, "input", payload.InputTokens, "output", payload.OutputTokens)
	return nil
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewLLMTelemetryWorker(deps.Store.Queries, deps.Logger), nil
	})
}

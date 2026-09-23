package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
)

// telemetryMockDBTX mocks the DBTX interface to return pricing and track inserts.
type telemetryMockDBTX struct {
	lastInsertedParams *database.InsertLLMTurnMetricParams
	pricingModel       database.ToroCoreLlmPricingModel
	pricingErr         error
}

func (m *telemetryMockDBTX) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), errors.New("not implemented")
}

func (m *telemetryMockDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (m *telemetryMockDBTX) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if strings.Contains(sql, "FROM toro_core.llm_pricing_models") {
		return &telemetryPricingRow{model: m.pricingModel, err: m.pricingErr}
	}

	if strings.Contains(sql, "INSERT INTO toro_core.llm_turn_metrics") {
		p := database.InsertLLMTurnMetricParams{
			TenantID:       args[0].(pgtype.UUID),
			ConversationID: args[1].(pgtype.UUID),
			DagID:          args[2].(pgtype.Text),
			NodeID:         args[3].(pgtype.Text),
			AgentID:        args[4].(pgtype.Text),
			Model:          args[5].(string),
			Provider:       args[6].(pgtype.Text),
			InputTokens:    args[7].(int32),
			OutputTokens:   args[8].(int32),
			TotalTokens:    args[9].(int32),
			CostUsd:        args[10].(pgtype.Numeric),
			CampaignID:     args[15].(pgtype.UUID),
			ProspectID:     args[16].(pgtype.UUID),
			StepNumber:     args[17].(pgtype.Int4),
			Metadata:       args[18].([]byte),
		}
		m.lastInsertedParams = &p
		return &telemetryInsertRow{}
	}

	return &telemetryMockRow{}
}

type telemetryMockRow struct{}

func (r *telemetryMockRow) Scan(_ ...interface{}) error {
	return errors.New("mock: not found")
}

type telemetryPricingRow struct {
	model database.ToroCoreLlmPricingModel
	err   error
}

func (r *telemetryPricingRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*pgtype.UUID) = r.model.ID
	*dest[1].(*string) = r.model.Model
	*dest[2].(*string) = r.model.Provider
	*dest[3].(*pgtype.Numeric) = r.model.InputCostPer1m
	*dest[4].(*pgtype.Numeric) = r.model.OutputCostPer1m
	*dest[5].(*pgtype.Numeric) = r.model.CacheCostPer1m
	*dest[6].(*pgtype.Timestamptz) = r.model.CreatedAt
	*dest[7].(*pgtype.Timestamptz) = r.model.UpdatedAt
	return nil
}

type telemetryInsertRow struct{}

func (r *telemetryInsertRow) Scan(dest ...interface{}) error {
	// Just return success
	return nil
}

func TestLLMTelemetryWorker_Subscriptions(t *testing.T) {
	w := NewLLMTelemetryWorker(nil, slog.Default())
	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Subject != "llm.telemetry.turn_completed" {
		t.Errorf("expected subject 'llm.telemetry.turn_completed', got '%s'", subs[0].Subject)
	}
	if subs[0].Group != "llm_telemetry_worker" {
		t.Errorf("expected group 'llm_telemetry_worker', got '%s'", subs[0].Group)
	}
}

func TestLLMTelemetryWorker_Handle_HappyPath(t *testing.T) {
	// Prepare input payload matching LLMTurnTelemetry
	payload := map[string]interface{}{
		"context": map[string]interface{}{
			"conversation_id": "a5fc98a5-8959-4503-b772-ef6f6a7fb346",
			"tenant_id":       "c0e45f46-269b-498a-93bb-54b544997da1",
			"dag_id":          "test-dag",
			"node_id":         "test-node",
			"agent_id":        "test-agent",
			"step_number":     3,
		},
		"model":         "gpt-5.4-mini",
		"provider":      "openai",
		"input_tokens":  1000,
		"output_tokens": 500,
		"total_tokens":  1500,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	msg := &nats.Msg{
		Subject: "llm.telemetry.turn_completed",
		Data:    payloadBytes,
	}

	// Prepare mock database with pricing
	var inputCost, outputCost pgtype.Numeric
	_ = inputCost.Scan("2.500000")   // $2.50 per 1M tokens
	_ = outputCost.Scan("10.000000") // $10.00 per 1M tokens

	dbtx := &telemetryMockDBTX{
		pricingModel: database.ToroCoreLlmPricingModel{
			Model:           "gpt-5.4-mini",
			Provider:        "openai",
			InputCostPer1m:  inputCost,
			OutputCostPer1m: outputCost,
		},
	}
	db := database.New(dbtx)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker := NewLLMTelemetryWorker(db, logger)

	err = worker.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected handle to succeed, got error: %v", err)
	}

	// Verify that InsertLLMTurnMetric was called with correctly calculated cost
	if dbtx.lastInsertedParams == nil {
		t.Fatal("expected InsertLLMTurnMetric to be called")
	}

	params := dbtx.lastInsertedParams
	if params.Model != "gpt-5.4-mini" {
		t.Errorf("expected model 'gpt-5.4-mini', got %q", params.Model)
	}
	if params.InputTokens != 1000 {
		t.Errorf("expected input tokens 1000, got %d", params.InputTokens)
	}
	if params.OutputTokens != 500 {
		t.Errorf("expected output tokens 500, got %d", params.OutputTokens)
	}
	if params.TotalTokens != 1500 {
		t.Errorf("expected total tokens 1500, got %d", params.TotalTokens)
	}

	fval, err := params.CostUsd.Float64Value()
	if err != nil {
		t.Fatalf("failed to get float64 value: %v", err)
	}
	if !fval.Valid {
		t.Fatal("expected cost value to be valid")
	}
	if fval.Float64 != 0.0075 {
		t.Errorf("expected calculated cost to be 0.0075, got %f", fval.Float64)
	}
}

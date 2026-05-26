package v2_agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
)

func makeTestTxnForGuard(desc, amount, cashDir, macroClass, accountType, accountName, reasoning string) database.FignodeStagingTransaction {
	return database.FignodeStagingTransaction{
		RawDescription:       pgtype.Text{String: desc, Valid: true},
		RawAmount:            amount,
		CashDirection:        pgtype.Text{String: cashDir, Valid: true},
		MacroClass:           pgtype.Text{String: macroClass, Valid: true},
		AccountType:          pgtype.Text{String: accountType, Valid: true},
		PredictedAccountName: pgtype.Text{String: accountName, Valid: true},
		AiReasoning:          pgtype.Text{String: reasoning, Valid: true},
	}
}

func TestEvaluateTransferRisk_Allow(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			result, ok := output.(*TransferGuardResult)
			if !ok {
				return errors.New("unexpected output type")
			}
			result.Action = ActionAllow
			result.Reasoning = "Internal transfer between two accounts at the same bank. No mirror statement needed."
			result.ClientPrompt = ""
			return nil
		},
	}

	txn := makeTestTxnForGuard(
		"Transfer to savings",
		"-1000.00",
		"OUTFLOW",
		"ASSET",
		"Bank",
		"Savings Account",
		"Internal transfer between checking and savings.",
	)

	stratResult := &StratificationResult{
		AccountingIntent: IntentPotentialTransfer,
		Reasoning:        "Same-institution transfer detected.",
		Confidence:       0.90,
	}

	result, err := EvaluateTransferRisk(context.Background(), llm, txn, "Checking Account", stratResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionAllow {
		t.Errorf("expected ALLOW, got %s", result.Action)
	}
}

func TestEvaluateTransferRisk_RequireDocument(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			result, ok := output.(*TransferGuardResult)
			if !ok {
				return errors.New("unexpected output type")
			}
			result.Action = ActionRequireExternalDocument
			result.Reasoning = "Credit card payment to external institution. Mirror statement required to avoid duplicate."
			result.ClientPrompt = "Please upload your Chase credit card statement for the period covering this payment to verify the liability balance."
			return nil
		},
	}

	txn := makeTestTxnForGuard(
		"Chase card payment",
		"-2000.00",
		"OUTFLOW",
		"LIABILITY",
		"Credit Cards",
		"Chase Credit Card",
		"Monthly credit card payment.",
	)

	stratResult := &StratificationResult{
		AccountingIntent: IntentPotentialTransfer,
		Reasoning:        "Credit card payment from checking account.",
		Confidence:       0.93,
	}

	result, err := EvaluateTransferRisk(context.Background(), llm, txn, "Checking Account", stratResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionRequireExternalDocument {
		t.Errorf("expected REQUIRE_EXTERNAL_DOCUMENT, got %s", result.Action)
	}
	if result.ClientPrompt == "" {
		t.Error("expected non-empty client_prompt for held transfer")
	}
}

func TestEvaluateTransferRisk_LLMError(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			return errors.New("llm timeout")
		},
	}

	txn := makeTestTxnForGuard("Test", "-10.00", "OUTFLOW", "LIABILITY", "Credit Cards", "Test", "")
	stratResult := &StratificationResult{
		AccountingIntent: IntentPotentialTransfer,
		Confidence:       0.5,
	}

	_, err := EvaluateTransferRisk(context.Background(), llm, txn, "Checking", stratResult)
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
}

func TestEvaluateTransferRisk_UnknownAction(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			raw := `{"action": "UNKNOWN", "client_prompt": "", "reasoning": "test"}`
			return json.Unmarshal([]byte(raw), output)
		},
	}

	txn := makeTestTxnForGuard("Test", "-10.00", "OUTFLOW", "LIABILITY", "Credit Cards", "Test", "")
	stratResult := &StratificationResult{
		AccountingIntent: IntentPotentialTransfer,
		Confidence:       0.5,
	}

	_, err := EvaluateTransferRisk(context.Background(), llm, txn, "Checking", stratResult)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

package v2_agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
)

type mockLLMClient struct {
	generateJSONFn func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error
}

func (m *mockLLMClient) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
	if m.generateJSONFn != nil {
		return m.generateJSONFn(ctx, systemPrompt, userPrompt, output)
	}
	return nil
}

func makeTestTxn(desc, amount, cashDir, macroClass, accountType, accountName, reasoning string) database.FignodeStagingTransaction {
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

func TestClassifyIntent_StandardPL(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			result, ok := output.(*StratificationResult)
			if !ok {
				return errors.New("unexpected output type")
			}
			result.AccountingIntent = IntentStandardPL
			result.Reasoning = "Target is an expense account, this is normal operating activity."
			result.Confidence = 0.95
			return nil
		},
	}

	txn := makeTestTxn(
		"Office supplies purchase",
		"-150.00",
		"OUTFLOW",
		"EXPENSE",
		"Expense",
		"Office Supplies",
		"Typical office expense transaction.",
	)

	result, err := ClassifyIntent(context.Background(), llm, txn, "Bank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccountingIntent != IntentStandardPL {
		t.Errorf("expected STANDARD_PL, got %s", result.AccountingIntent)
	}
	if result.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", result.Confidence)
	}
}

func TestClassifyIntent_BalanceSheetShift(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			result, ok := output.(*StratificationResult)
			if !ok {
				return errors.New("unexpected output type")
			}
			result.AccountingIntent = IntentBalanceSheetShift
			result.Reasoning = "This is a loan principal payment, shifting between asset and liability accounts."
			result.Confidence = 0.92
			return nil
		},
	}

	txn := makeTestTxn(
		"Monthly equipment loan payment",
		"-2500.00",
		"OUTFLOW",
		"LIABILITY",
		"Long Term Liability",
		"Equipment Loan",
		"Loan payment with principal and interest components.",
	)

	result, err := ClassifyIntent(context.Background(), llm, txn, "Bank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccountingIntent != IntentBalanceSheetShift {
		t.Errorf("expected BALANCE_SHEET_SHIFT, got %s", result.AccountingIntent)
	}
}

func TestClassifyIntent_PotentialTransfer(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			result, ok := output.(*StratificationResult)
			if !ok {
				return errors.New("unexpected output type")
			}
			result.AccountingIntent = IntentPotentialTransfer
			result.Reasoning = "Credit card payment from checking account — mirror statement needed."
			result.Confidence = 0.88
			return nil
		},
	}

	txn := makeTestTxn(
		"Amex payment",
		"-500.00",
		"OUTFLOW",
		"LIABILITY",
		"Credit Cards",
		"Amex",
		"Payment to American Express credit card.",
	)

	result, err := ClassifyIntent(context.Background(), llm, txn, "Bank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccountingIntent != IntentPotentialTransfer {
		t.Errorf("expected POTENTIAL_TRANSFER, got %s", result.AccountingIntent)
	}
}

func TestClassifyIntent_LLMError(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			return errors.New("llm service unavailable")
		},
	}

	txn := makeTestTxn("Test", "-10.00", "OUTFLOW", "EXPENSE", "Expense", "Test Account", "")
	_, err := ClassifyIntent(context.Background(), llm, txn, "Bank")
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
}

func TestClassifyIntent_UnknownIntent(t *testing.T) {
	llm := &mockLLMClient{
		generateJSONFn: func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
			// Write a valid JSON response but with an unknown intent
			raw := `{"accounting_intent": "UNKNOWN_TYPE", "reasoning": "test", "confidence": 0.5}`
			return json.Unmarshal([]byte(raw), output)
		},
	}

	txn := makeTestTxn("Test", "-10.00", "OUTFLOW", "EXPENSE", "Expense", "Test Account", "")
	_, err := ClassifyIntent(context.Background(), llm, txn, "Bank")
	if err == nil {
		t.Fatal("expected error for unknown intent")
	}
}

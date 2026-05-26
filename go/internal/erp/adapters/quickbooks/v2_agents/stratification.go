package v2_agents

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
)

// AccountingIntent classifies the structural nature of a transaction.
type AccountingIntent string

const (
	IntentStandardPL       AccountingIntent = "STANDARD_PL"
	IntentBalanceSheetShift AccountingIntent = "BALANCE_SHEET_SHIFT"
	IntentPotentialTransfer AccountingIntent = "POTENTIAL_TRANSFER"
)

// StratificationResult is the structured JSON output from the stratification LLM call.
type StratificationResult struct {
	AccountingIntent AccountingIntent `json:"accounting_intent"`
	Reasoning        string           `json:"reasoning"`
	Confidence       float64          `json:"confidence"`
}

const stratificationSystemPrompt = `You are an expert accounting classifier. Your task is to determine the accounting intent of a financial transaction based on its structural properties — the accounts involved, the amounts, and the macro-level classification — NOT by simple text matching on the description.

Classify each transaction into exactly one of these categories:

1. STANDARD_PL: The transaction affects a Profit & Loss account (Income, Expense, COGS, Other Income, Other Expense). This is normal operating activity.
2. BALANCE_SHEET_SHIFT: The transaction moves money between balance sheet accounts (Asset, Liability, Equity) without affecting P&L. Examples: loan payments, asset purchases, owner draws, paying off a credit card from a bank account — these are internal shifts, not operating expenses.
3. POTENTIAL_TRANSFER: The transaction appears to move money between the company's own accounts at different institutions, or represents a credit card payment where the mirror statement may not be visible. This requires further review to avoid double-counting.

Key rules:
- If the target account is a Credit Card, Loan, or Equity account AND the source is a Bank account, this is a BALANCE_SHEET_SHIFT or POTENTIAL_TRANSFER — never STANDARD_PL.
- If the target account is Expense, COGS, Income, or Other Income, this is STANDARD_PL.
- If both source and target are Bank/Cash accounts at different institutions, this is POTENTIAL_TRANSFER.
- A credit card payment from a checking account is a POTENTIAL_TRANSFER (risk of missing the mirror credit card statement).
- Owner draws to Equity are BALANCE_SHEET_SHIFT.
- Loan principal payments are BALANCE_SHEET_SHIFT.
- Asset purchases (Fixed Assets, Leasehold Improvements) are BALANCE_SHEET_SHIFT, not STANDARD_PL expenses.

You MUST respond with valid JSON only, no other text.`

const stratificationUserPromptTmpl = `Classify the accounting intent for this transaction:

Description: %s
Amount: %s
Cash Direction: %s
Macro Class (predicted): %s
Account Type (predicted): %s
Target Account: %s
Source Account Type: %s
AI Reasoning (from prior stages): %s

Return your classification as JSON with these fields:
- accounting_intent: one of "STANDARD_PL", "BALANCE_SHEET_SHIFT", "POTENTIAL_TRANSFER"
- reasoning: brief explanation of why this classification was chosen
- confidence: a number between 0.0 and 1.0`

// ClassifyIntent runs the stratification LLM call against a single staging transaction.
func ClassifyIntent(
	ctx context.Context,
	llm LLMClient,
	txn database.FignodeStagingTransaction,
	sourceAccountType string,
) (*StratificationResult, error) {
	userPrompt := fmt.Sprintf(
		stratificationUserPromptTmpl,
		txn.RawDescription.String,
		txn.RawAmount,
		txn.CashDirection.String,
		txn.MacroClass.String,
		txn.AccountType.String,
		txn.PredictedAccountName.String,
		sourceAccountType,
		txn.AiReasoning.String,
	)

	var result StratificationResult
	if err := llm.GenerateJSON(ctx, stratificationSystemPrompt, userPrompt, &result); err != nil {
		return nil, fmt.Errorf("stratification llm call: %w", err)
	}

	if result.AccountingIntent != IntentStandardPL &&
		result.AccountingIntent != IntentBalanceSheetShift &&
		result.AccountingIntent != IntentPotentialTransfer {
		return nil, fmt.Errorf("stratification returned unknown intent: %s", result.AccountingIntent)
	}

	return &result, nil
}

package v2_agents

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
)

// TransferGuardAction is the decision from the transfer guard evaluation.
type TransferGuardAction string

const (
	ActionAllow                   TransferGuardAction = "ALLOW"
	ActionRequireExternalDocument TransferGuardAction = "REQUIRE_EXTERNAL_DOCUMENT"
)

// TransferGuardResult is the structured JSON output from the transfer guard LLM call.
type TransferGuardResult struct {
	Action       TransferGuardAction `json:"action"`
	ClientPrompt string              `json:"client_prompt"`
	Reasoning    string              `json:"reasoning"`
}

const transferGuardSystemPrompt = `You are an expert accounting auditor specializing in inter-account transfers and credit card reconciliations. Your task is to evaluate whether a potential transfer transaction can be safely booked, or whether it requires external documentation to avoid duplication.

Background: When a business transfers money between its own accounts (e.g., paying a credit card from a checking account), the transaction appears on BOTH statements — as an outflow on the checking account and as an inflow/payment on the credit card statement. If we only see one side, booking it could create a duplicate entry.

Evaluate each transaction and decide:

1. ALLOW: The transfer is safe to book. This applies when:
   - The transfer is between two accounts at the SAME institution (internal transfer, not a credit card payment).
   - The transfer is an owner draw or equity contribution (these don't generate mirror statements).
   - The target is a loan account where the payment is clearly a principal reduction.

2. REQUIRE_EXTERNAL_DOCUMENT: The transfer requires the mirror statement before booking. This applies when:
   - The transaction is a credit card payment and we don't have the credit card statement linked.
   - The transaction is a transfer to an external institution where the receiving account isn't tracked.
   - The nature of the transfer is ambiguous and could represent either a payment or a purchase.

When REQUIRE_EXTERNAL_DOCUMENT is returned, you MUST provide a client_prompt — a clear, professional message (1-2 sentences) explaining to the business owner what documentation is needed and why. This message will be shown directly to the client.

You MUST respond with valid JSON only, no other text.`

const transferGuardUserPromptTmpl = `Evaluate this potential transfer for documentation requirements:

Description: %s
Amount: %s
Cash Direction: %s
Macro Class: %s
Account Type: %s
Source Account: %s
Target Account: %s
AI Reasoning: %s
Stratification Result: %s (confidence: %.2f)

Return your evaluation as JSON with these fields:
- action: "ALLOW" or "REQUIRE_EXTERNAL_DOCUMENT"
- client_prompt: if REQUIRE_EXTERNAL_DOCUMENT, a 1-2 sentence message for the client explaining what's needed
- reasoning: your audit rationale`

// EvaluateTransferRisk runs the transfer guard LLM call against a transaction
// that was classified as POTENTIAL_TRANSFER by the stratification agent.
func EvaluateTransferRisk(
	ctx context.Context,
	llm LLMClient,
	txn database.FignodeStagingTransaction,
	sourceAccountName string,
	stratResult *StratificationResult,
) (*TransferGuardResult, error) {
	userPrompt := fmt.Sprintf(
		transferGuardUserPromptTmpl,
		txn.RawDescription.String,
		txn.RawAmount,
		txn.CashDirection.String,
		txn.MacroClass.String,
		txn.AccountType.String,
		sourceAccountName,
		txn.PredictedAccountName.String,
		txn.AiReasoning.String,
		stratResult.AccountingIntent,
		stratResult.Confidence,
	)

	var result TransferGuardResult
	if err := llm.GenerateJSON(ctx, transferGuardSystemPrompt, userPrompt, &result); err != nil {
		return nil, fmt.Errorf("transfer guard llm call: %w", err)
	}

	if result.Action != ActionAllow && result.Action != ActionRequireExternalDocument {
		return nil, fmt.Errorf("transfer guard returned unknown action: %s", result.Action)
	}

	return &result, nil
}

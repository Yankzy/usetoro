package api

import (
	"encoding/json"
	"fmt"
)

// ColumnMapping represents the strict JSON schema we expect from the LLM
// to dynamically map columns from arbitrary bank CSV/XLSX files.
type ColumnMapping struct {
	DateColIdx        *int    `json:"date_col_idx"`
	DescriptionColIdx *int    `json:"description_col_idx"`
	AmountColIdx      *int    `json:"amount_col_idx"`
	IsSplitAmount     bool    `json:"is_split_amount"`
	DebitColIdx       *int    `json:"debit_col_idx"`
	CreditColIdx      *int    `json:"credit_col_idx"`
	VendorColIdx      *int    `json:"vendor_col_idx"`
	IsExpensePositive bool    `json:"is_expense_positive"`
	ConfidenceScore   float64 `json:"confidence_score"`
	Reasoning         string  `json:"reasoning"`
}

// buildMappingPrompt constructs the system/user prompt for GPT-5.4
// to look at the headers and sample data to return the schema index mapping.
func buildMappingPrompt(headers []string, dataRows [][]string) string {
	headersJSON, _ := json.Marshal(headers)
	dataJSON, _ := json.Marshal(dataRows)

	return fmt.Sprintf(`
I am providing you with the headers and a few sample data rows from a bank export CSV file.

Your job is to determine which column index (0-based) corresponds to the following required fields:
- date: The date of the transaction.
- description: The description, memo, or payee of the transaction.
- amount: The financial amount. NOTE: Sometimes amount is split into two columns (e.g., 'Debit' and 'Credit' or 'Money In' and 'Money Out'). 
- vendor: The vendor, payee, payer, or customer name (optional, only map if a separate column exists from description). This field represents the counterparty entity, whether it is for money out (vendors) or money in (customers).

IMPORTANT ACCOUNTING SIGN CONVENTION:
Different banks and ERPs handle signs differently. For example, Plaid treats positive numbers as expenses, while others treat positive numbers as revenue. 
You must determine the sign convention of this specific file. Do NOT rely entirely on column headers like "Money In" or "Money Out", as many CSVs just have a single "Amount" column.
Instead, look at the actual transaction descriptions or vendors (e.g., if you see "Starbucks" or "Amazon", that means money leaving the bank—an expense. If you see "Stripe", "Gusto Payroll", or "Client Deposit", that usually means money entering the bank—revenue). 
Look at the numerical sign of those transactions to deduce if a positive number in the amount column represents an Expense (Money Out) or Revenue (Money In).

Here is the sample data:
Headers: %s
Data Rows: %s

Respond ONLY with a valid JSON object matching this schema:
{
	"date_col_idx": int | null,
	"description_col_idx": int | null,
	"amount_col_idx": int | null,
	"is_split_amount": boolean,
	"debit_col_idx": int | null,
	"credit_col_idx": int | null,
	"vendor_col_idx": int | null,
	"is_expense_positive": boolean,
	"confidence_score": float (0.0 to 1.0),
	"reasoning": "string explaining your choices"
}

Rules:
1. If an amount is split across debit/credit, set "is_split_amount": true, "amount_col_idx": null, and provide both "debit_col_idx" and "credit_col_idx".
2. If the amount is in a single column, set "is_split_amount": false, provide "amount_col_idx", and set debit/credit to null.
3. If a column cannot be confidently mapped, set its index to null.
4. "is_expense_positive" MUST be true if positive numbers represent expenses (money out), and false if positive numbers represent revenue (money in). If the amounts are split into two columns, set this to true if the debit/money-out column contains positive numbers.
`, string(headersJSON), string(dataJSON))
}

package ase

// =========================================================================
// Legacy Constants for backwards compatibility with classification_stage_worker.go
// =========================================================================

var AccountTypeOptions = map[string]string{
	"ASSET":     "Accounts Receivable, Bank, Fixed Assets, Leasehold Improvements, Other Current Assets, Other Assets",
	"LIABILITY": "Accounts Payable, Credit Cards, Long Term Liability, Other Current Liability",
	"EQUITY":    "Equity",
	"REVENUE":   "Income, Other Income",
	"EXPENSE":   "Expense, Other Expense, Cost of Goods Sold",
}

var MacroClassSpecificRules = map[string]string{
	"ASSET": `Evaluate each transaction against these distinct asset categories:
1. BANK: Use this if the description indicates cash or liquidity positions (e.g., checking, savings, or internal transfers between funding sources).
2. FIXED ASSET: Use this if the purchase is for long-term physical equipment, machinery, company vehicles, or software infrastructure licenses where the absolute value is > $2,500.
3. OTHER CURRENT ASSET: Use this for short-term economic values expected to convert to cash within one year (e.g., security deposits, inventory prepayments).
4. OTHER ASSETS: Use this for non-current, non-fixed assets (e.g., long-term investments, intangible assets).
5. LEASEHOLD IMPROVEMENTS: Use this for structural modifications to rented property.
6. ACCOUNTS RECEIVABLE: Use this only if the transaction represents money owed to the business by a customer.`,

	"LIABILITY": `Evaluate each transaction against these distinct liability categories:
1. CREDIT CARDS: Use this ONLY if the description explicitly references a known credit card provider or card payment obligation.
2. LONG TERM LIABILITY: Use this for debts with a maturity beyond one year (e.g., equipment loans, mortgages, SBA loans).
3. OTHER CURRENT LIABILITY: Use this for short-term obligations due within one year (e.g., payroll taxes payable, sales tax collected, short-term notes).
4. ACCOUNTS PAYABLE: Use this only if the transaction represents money the business owes to a supplier or vendor.`,

	"EQUITY": `Evaluate each transaction against this category:
1. EQUITY: Use this for all owner-related transactions including owner draws, owner investments, retained earnings adjustments, and partner distributions.`,

	"REVENUE": `Evaluate each transaction against these distinct revenue categories:
1. INCOME: Use this for all standard operating revenue from the primary business activity (e.g., product sales, service fees, consulting income, Stripe payouts).
2. OTHER INCOME: Use this for non-operating or incidental revenue (e.g., interest earned, foreign exchange gains, insurance claim proceeds, asset sale gains).`,

	"EXPENSE": `Evaluate each transaction against these distinct expense categories:
1. COST OF GOODS SOLD: Use this ONLY if the transaction is directly, structurally tied to producing revenue or purchasing inventory based on the Business Industry.
2. OTHER EXPENSE: Use this for unusual, non-operating outflows that do not reflect day-to-day business operations (e.g., tax penalties, legal settlements, corporate restructuring costs).
3. EXPENSE: Use this for all standard operating overhead, general administrative costs, software subscriptions, travel, meals, and general supplies.`,
}

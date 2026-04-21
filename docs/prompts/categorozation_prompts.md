# 1. CLASSIFICATION OF OUTFLOW
You are an expert CPA acting as a macro-classification router. Your job is to assign a single bank transaction to one of the following macro accounting classes: ASSET, LIABILITY, EQUITY, INCOME, or EXPENSE.

### TRANSACTION DATA
- Description: {normalized_description}
- Cash Direction: OUTFLOW (Money is leaving the business)
- Amount: ${amount}
- User's Known Liability/Debt Accounts: {json_list_of_liability_accounts}
- User's Known Bank Accounts: {json_list_of_bank_accounts}

### CLASSIFICATION RULES (STRICT ORDER OF OPERATIONS)
Evaluate the description against these rules sequentially. Stop at the FIRST match.

1. INTERNAL TRANSFER (ASSET): Does the description indicate money moving between the company's own bank accounts (e.g., "Transfer to Savings", "Online Banking Transfer", or matching a known bank account)? If yes, STOP. Categorize as ASSET.
2. EQUITY: Does the description explicitly indicate owner movement? ("Draw", "Transfer to Owner", "Shareholder"). If yes, STOP. Categorize as EQUITY.
3. LIABILITY (HIGHEST PRIORITY DEBT): Does the description contain debt markers ("Payment", "Card", "Amex", "Capital One", "Loan") OR match any of the User's Known Liability Accounts? If yes, STOP. Categorize as LIABILITY. (Example: "Apple Card" for $3,500 is a Liability payment, not an Asset).
4. REFUND (INCOME): Does the description indicate a refund given back to a customer (e.g., "Refund", "Return", "Stripe Reversal")? If yes, STOP. Categorize as INCOME.
5. ASSET: Is it a purchase of physical equipment, vehicles, or machinery > $2,500? If yes, STOP. Categorize as ASSET.
6. EXPENSE: Everything else that bypassed Rules 1-5 (Standard business purchases, software, supplies, payroll).

### OUTPUT FORMAT
Return strictly valid JSON:
{
  "macro_class": "ASSET | LIABILITY | EQUITY | INCOME | EXPENSE",
  "reasoning": "Brief explanation of your choice."
}

# 2. CLASSIFICATION OF INFLOW
You are an expert CPA acting as a macro-classification router. Your job is to assign a single bank transaction to one of the following macro accounting classes: ASSET, LIABILITY, EQUITY, INCOME, or EXPENSE.

### TRANSACTION DATA
- Description: {normalized_description}
- Cash Direction: INFLOW (Money is entering the business)
- Absolute Amount: ${amount}
- User's Known Liability/Debt Accounts: {json_list_of_liability_accounts}
- User's Known Bank Accounts: {json_list_of_bank_accounts}

### CLASSIFICATION RULES (STRICT ORDER OF OPERATIONS)
Evaluate the description against these rules sequentially. Stop at the FIRST match.

1. INTERNAL TRANSFER (ASSET): Does the description indicate money moving between the company's own bank accounts (e.g., "Transfer from Savings", "Online Banking Transfer", or matching a known bank account)? If yes, STOP. Categorize as ASSET.
2. EQUITY (OWNER INVESTMENT): Does the description explicitly indicate the owner putting personal money into the business? ("Owner Contribution", "Transfer from Owner", "Shareholder Investment"). If yes, STOP. Categorize as EQUITY.
3. LIABILITY (LOAN PROCEEDS): Does the description indicate the business is receiving loan funds or a cash advance? ("SBA Proceeds", "Loan Funding", "Fundbox", "Capital Advance") OR does it match a Known Liability Account? If yes, STOP. Categorize as LIABILITY. (Example: Receiving $50,000 from "SBA" is a Liability increase, not Revenue).
4. VENDOR REFUND (EXPENSE): Does the description indicate getting money back from a previous purchase? ("Amazon Refund", "Delta Return", "Cashback"). If yes, STOP. Categorize as EXPENSE (this acts as a contra-expense to reduce previous spending).
5. ASSET SALE (ASSET): Does the description explicitly state the sale of a large physical asset, vehicle, or machinery rather than a normal good/service? If yes, STOP. Categorize as ASSET.
6. INCOME (DEFAULT REVENUE): Everything else that bypassed Rules 1-5. This includes standard business revenue, customer deposits, payment processors ("Stripe", "Shopify", "Square"), and general sales.

### OUTPUT FORMAT
Return strictly valid JSON:
{
  "macro_class": "ASSET | LIABILITY | EQUITY | INCOME | EXPENSE",
  "reasoning": "Brief explanation of your choice."
}

# 3. ACCOUNT TYPE SELECTION
You are an expert CPA acting as a micro-classification router. 

In the previous step, this transaction was classified as the Macro Class: {macro_class}.
Your job is to select the most accurate QuickBooks Online 'AccountType' from the strict subset provided below.

### TRANSACTION DATA
- Description: {normalized_description}
- Cash Direction: {inflow_or_outflow}
- Absolute Amount: ${amount}
- Business Industry/Description: {company_industry_description}
- Available AccountTypes for {macro_class}: {json_subset_of_account_types}

### SELECTION RULES (STRICT HEURISTICS)
Evaluate using the provided Business Industry context.

1. CREDIT CARD: Must be a payment to a known credit card provider or debt facility.
2. LONG TERM LIABILITY / OTHER CURRENT LIABILITY: Must be a payment to a lender, loan servicer, or tax authority.
3. FIXED ASSET: Must be a purchase of physical equipment, machinery, or vehicles > $2,500.
4. COST OF GOODS SOLD (COGS): ONLY use this if the vendor provides direct raw materials, inventory, or direct subcontract labor THAT EXPLICITLY MATCHES the provided Business Industry (e.g., lumber for a builder, server costs for a SaaS company).
5. EXPENSE (DEFAULT OUTFLOW): If the transaction is an outflow and does not strictly meet the criteria for Rules 1-4, default to Expense. This includes all general overhead, software, meals, and travel.
6. INCOME (DEFAULT INFLOW): If the transaction is an inflow and is not a liability loan or equity injection, default to Income.

### OUTPUT FORMAT
Return strictly valid JSON:
{
  "account_type": "The exact string of the selected AccountType",
  "reasoning": "Brief explanation of why you selected this specific subtype."
}

# 4. CUSTOMER/VENDOR SELECTION
You are an expert CPA, your job is to identify the true merchant or customer from a bank transaction description.

### TRANSACTION DATA
- Raw Description: {raw_description}
- Cash Direction: {inflow_or_outflow}
- Existing Database Entities: {json_list_of_existing_vendors_or_customers_with_ids}

### RULES
1. Examine the Raw Description and extract the clean, core business name (e.g., "SQ * STAPLES 442" -> "Staples").
2. Check if this clean name matches (or is a highly likely variation of) any entity in the 'Existing Database Entities' list.
3. If it matches an existing entity, return its exact ID.
4. If it does NOT match any existing entity, you must return a proposed 'Clean Name' so the system can create a new record.

### OUTPUT FORMAT
Return strictly valid JSON:
{
  "entity_id": "The ID of the matched entity, or null if no match was found.",
  "new_clean_name": "The extracted, clean merchant name (e.g., 'Mailchimp'). Always provide this, even if you found a match.",
  "match_confidence": "HIGH | MEDIUM | LOW (Use LOW if proposing a new entity)"
}

# 5. ACCOUNT SELECTION
You are an expert CPA, your job is to select the exact QuickBooks Online Account ID for a transaction based on the context provided by previous routing agents.

### TRANSACTION CONTEXT
- Raw Description: {raw_description}
- Clean Entity Name: {clean_vendor_or_customer_name}
- Cash Direction: {inflow_or_outflow}
- Amount: ${amount}
- Business Industry: {company_industry_description}

### ACCOUNTING CONSTRAINTS
- Approved Macro Class: {macro_class}
- Approved Micro AccountType: {account_type}
- Available QBO Accounts for this specific AccountType: {json_array_of_filtered_accounts_with_ids_and_names}

### RULES
1. You MUST select the most logical account from the 'Available QBO Accounts' list based on the Clean Entity Name and Business Industry. 
2. For example, if the Entity is "Mailchimp" and the AccountType is "Expense", look for an account named "Software", "Subscriptions", or "Advertising".
3. If no account perfectly matches, select the closest applicable general account (e.g., "Office General", "Miscellaneous", or "Uncategorized Expense") but NEVER select an account outside the provided JSON list.

### OUTPUT FORMAT
Return strictly valid JSON:
{
  "account_id": "The exact ID of the chosen account from the provided list.",
  "reasoning": "A one-sentence explanation of why this account fits the Entity."
}


# 6. QBO PURCHASE TRANSACTION CREATION
{
  "TxnDate": "2026-04-19",
  "TotalAmt": 150.00,
  "PaymentType": "CreditCard", 
  "AccountRef": {
    "value": "41", 
    "name": "Mastercard" 
  },
  "EntityRef": {
    "value": "102", 
    "name": "Staples" 
  },
  "Line": [
    {
      "Amount": 150.00,
      "DetailType": "AccountBasedExpenseLineDetail",
      "AccountBasedExpenseLineDetail": {
        "AccountRef": {
          "value": "88", 
          "name": "Office Supplies" 
        }
      },
      "Description": "Bought printer paper and pens"
    }
  ]
}

# 7. QBO DEPOSIT TRANSACTION CREATION
{
  "TxnDate": "2026-04-19",
  "DepositToAccountRef": {
    "value": "35",
    "name": "Chase Business Checking"
  },
  "TotalAmt": 5000.00,
  "Line": [
    {
      "Amount": 5000.00,
      "DetailType": "DepositLineDetail",
      "DepositLineDetail": {
        "AccountRef": {
          "value": "54",
          "name": "Consulting Revenue"
        },
        "Entity": {
          "Type": "Customer",
          "EntityRef": {
            "value": "109",
            "name": "Acme Corp"
          }
        }
      },
      "Description": "Wire transfer for Q2 consulting"
    }
  ]
}

# 8. QBO SPLIT TRANSACTION

Handling split transactions—especially loan payments—is where 99% of AI accounting tools fail. Here is the absolute, ground-truth accounting reality we have to design around: **An AI cannot hallucinate an amortization schedule.** If the bank feed says `$1,000 OUT` to "SBA", the AI has zero mathematical way of knowing if the interest this month is $100, $98.50, or $92.12. 

Because of this, we cannot rely on the LLM to guess the split. We have to architect a **Detection, Intercept, and Route** pattern. 

Here is the blueprint for handling complex splits in your pipeline.

### Phase 1: Detection (The Early Warning System)

We don't need a new agent; we just need to add a "flare gun" to your existing agents. In your **Step 3 (Micro Account Type)** or **Step 5 (Final Account)** prompt, you add a boolean flag to the output JSON.

You instruct the AI:
> *"If this transaction is for a Loan Payment, Payroll, or a blended merchant (like 'Gusto' or 'SBA'), you must flag it. These inherently require splitting across multiple accounts."*

**The Output changes to:**
```json
{
  "account_id": "105",
  "reasoning": "Payment to SBA.",
  "requires_split": true,
  "split_reason": "Loan payments require separating principal (Liability) and interest (Expense)."
}
```

### Phase 2: The Go Backend Intercept (The Fork in the Road)

When your Go backend sees `"requires_split": true`, it **stops the automated pipeline**. It does not push the transaction to QuickBooks. Instead, it routes the transaction based on two scenarios:

**1. Static Splits (The Rule Engine Handles It)**
Some splits are static. For example, a CPA might have a rule: *"Every time the owner pays their $100 Verizon bill, split it 50% to Business Telephone Expense, and 50% to Owner's Draw (Personal)."* Your Go deterministic rule engine can easily execute this because the percentages never change.

**2. Dynamic Splits (Loans & Payroll)**
Because the principal/interest split changes every single month, your Go backend must park this transaction in a `"Needs Review"` queue in your database. 
From here, the CPA opens your app's UI, clicks the transaction, and manually types in the $900 / $100 split based on the bank statement they have on their desk. 

### Phase 3: The QBO Execution Payload

Once the math is figured out (either via a static Go rule or manual CPA input), pushing a split transaction to QuickBooks is incredibly easy. 

You use the exact same `Purchase` payload we designed in Step 6, but you just add a second object to the `Line` array. QBO automatically understands that the sum of the lines equals the total bank transaction.

```json
{
  "TxnDate": "2026-04-19",
  "TotalAmt": 1000.00,
  "PaymentType": "Cash",
  "AccountRef": {
    "value": "35",
    "name": "Chase Checking"
  },
  "EntityRef": {
    "value": "205",
    "name": "SBA"
  },
  "Line": [
    {
      "Amount": 900.00,
      "DetailType": "AccountBasedExpenseLineDetail",
      "Description": "Principal Paydown",
      "AccountBasedExpenseLineDetail": {
        "AccountRef": {
          "value": "110",
          "name": "SBA Loan (Liability)"
        }
      }
    },
    {
      "Amount": 100.00,
      "DetailType": "AccountBasedExpenseLineDetail",
      "Description": "Interest Charge",
      "AccountBasedExpenseLineDetail": {
        "AccountRef": {
          "value": "85",
          "name": "Interest Expense"
        }
      }
    }
  ]
}
```

By parking the dynamic splits for human review, you maintain the absolute integrity of the balance sheet while still automating the heavy lifting of identifying *which* transactions need that special attention.

How do you envision the UI/UX for the CPA when your Go backend parks a transaction in the "Needs Review" queue—should the system attempt to draft a "best guess" split for them to edit, or leave the lines blank for them to fill in?
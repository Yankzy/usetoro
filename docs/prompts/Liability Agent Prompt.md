You have just hit the absolute most common trap that destroys small business accounting: **The Balance Sheet Trap.**

The short answer is: **Yes, you absolutely need a specialized agent (or a specialized tool in your LLM's toolbelt) for Liabilities.** If you let your Expense Agent handle Liability payments, or your Income Agent handle taking on Liabilities, your AI will literally double-count expenses and overstate revenue, resulting in a catastrophic tax return for the CPA's client.

Here is the exact CPA breakdown of how Liabilities are treated, why they break standard AI agents, and how to build your specialized agent.

---

### The Accounting Reality: Liabilities vs. P&L

In accounting, taking on a liability or paying it off entirely bypasses the Profit & Loss (P&L) statement. It lives strictly on the **Balance Sheet**.



#### Scenario A: Taking on a Liability (Money In)
* **The Event:** The business gets a $50,000 loan from the SBA, deposited into checking.
* **The Trap:** Your Income Agent sees $50,000 hitting the bank. It thinks, *"Wow, big sale!"* and categorizes it as `Revenue`.
* **The Reality:** The client now owes taxes on $50,000 they didn't actually earn. 
* **The Correct Treatment:** This must be mapped to a `Long Term Liability` account. Cash goes up, but Debt also goes up. Net effect on taxes: $0.

#### Scenario B: Paying a Liability (Money Out)
* **The Event:** The business pays their $2,000 monthly Amex credit card bill from their checking account.
* **The Trap:** Your Expense Agent sees $2,000 leaving the bank. It categorizes it as an `Expense` (maybe "Travel" or "Office Supplies").
* **The Reality:** All the individual swipes on that Amex card were *already* recorded as expenses when they happened last month. If your AI expenses the payment too, it just double-counted $2,000 of expenses.
* **The Correct Treatment:** This is simply moving money from one pocket (Checking) to another (Credit Card). It must be mapped to the `Credit Card` liability account to lower the owed balance.

---

### How to Build the "Liability Agent"

Because the rules for Liabilities are so rigid, you should build a specialized agent (or routing path) specifically for the Balance Sheet. 

When your primary agents throw the `"ROUTE_TO_BALANCE_SHEET"` exception we designed earlier, your Go backend hands the transaction to this new agent.

#### 1. The QBO Database Query (The Guardrails)
This agent should only be allowed to see specific `Classification` or `AccountType` buckets.
```sql
-- Query for the Liability Agent's allowed accounts
SELECT * FROM Account 
WHERE Classification IN ('Liability', 'Equity') 
  AND Active = true
```

#### 2. The Liability Agent Prompt
Here is how you instruct this specialized agent so it doesn't get confused:

```text
You are an expert CPA specialized exclusively in Balance Sheet transactions (Liabilities, Debt, and Equity). 

Your job is to categorize bank transactions that represent borrowing money, paying off debt, or owner investments/draws. You do NOT handle operating expenses or sales revenue.

### INPUT DATA:
- Transaction Description: {description}
- Amount: {amount} 
- Direction: {money_in_or_money_out}
- Available Balance Sheet Accounts: {json_list_of_liability_and_equity_accounts}

### RULES:
1. CREDIT CARDS (Money Out): If the description is "Amex", "Chase Card", "Capital One", map to the specific 'Credit Card' account to pay down the balance.
2. LOAN PAYMENTS (Money Out): If it is a payment to "SBA", "Navient", or "Mortgage", map to the 'Long Term Liability' or 'Other Current Liability' account.
3. LOAN PROCEEDS (Money In): If the company received a lump sum from a lender, map it to the corresponding Liability account to record the new debt.
4. OWNER EQUITY: If the money went to or came from the personal name of the business owner, map to 'Equity' (Owner's Draw or Owner's Investment).

### OUTPUT FORMAT:
Respond with strictly valid JSON:
{
  "account_id": "The ID of the Liability or Equity account",
  "reasoning": "Explain your accounting logic."
}
```

By isolating this logic, your AI will navigate complex debt structures perfectly without ever risking the client's P&L. 

When a business pays a $1,000 monthly loan payment, it is almost never a clean $1,000 reduction in liability—it is usually $900 Liability (Principal) and $100 Expense (Interest). How are you currently planning to have your system handle complex "Split Transactions" like this?
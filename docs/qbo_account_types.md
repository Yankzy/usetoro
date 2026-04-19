# The 3-Tier Hierarchy Explained

To build your fine-grained token-saving router, you need to understand the exact 3-tier hierarchy Intuit uses in their database.

#### Tier 1: `Classification` (The 5 Macro Enums)
These are the pillars of double-entry accounting.
* `Asset`, `Liability`, `Equity` (Balance Sheet)
* `Revenue`, `Expense` (Profit & Loss)

#### Tier 2: `AccountType` (The 16 Micro Enums)
This is what you want to use for your routing agents. It breaks the 5 macros down into the specific operational buckets.
* **Assets:** `Bank`, `Accounts Receivable`, `Other Current Asset`, `Fixed Asset`, `Other Asset`
* **Liabilities:** `Accounts Payable`, `Credit Card`, `Other Current Liability`, `Long Term Liability`
* **Equity:** `Equity`
* **Revenue:** `Income`, `Other Income`
* **Expense:** `Expense`, `Other Expense`, `Cost of Goods Sold`

#### Tier 3: AccountSubType (The Hundreds of Granular Enums)
This is the lowest level (e.g., Advertising, Legal, Rent, Travel). You do not want to use this for routing, because there are hundreds of them and they change depending on the region (US vs. UK). Stick to Level 2.

---

### How to Build Your "Fine-Grained" AI Router

Since you embed the Chart of Accounts in Pinecone, you have a massive advantage. You don't even need to send all the accounts to the LLM. You use a multi-step routing mechanism.

**Step 1: The Fast LLM Classifier (Low Token Cost)**
You send the LLM the transaction (`"UBER *TRIP SF"`) and ask it to pick ONLY the Tier 2 `AccountType` from a hardcoded list of 16 words. 
* *LLM Output:* `{"account_type": "Expense"}` (Cost: fractions of a penny).

**Step 2: The Pinecone Guardrail**
Your Go backend takes that output and queries Pinecone. But now, you apply a strict metadata filter:
```go
filter := map[string]interface{}{
    "account_type": map[string]interface{}{"$eq": "Expense"},
}
```

You already have the primitive required to solve this. We just need to explicitly insert it into the pipeline as the **Normalization Layer** before the math filter runs. 

Here is how your Toro OS architecture absorbs the chaos of 10,000 different bank formats.

***

### The Fix: The "Universal Toro Ledger" Standard (Ken Thompson)
You cannot do math on raw bank data. You must force the raw data into *your* internal standard first. 

You define a strict, internal Toro OS JSON schema for a transaction. In your internal database, you decide the absolute law of physics. For example: **All outflows are negative floats. All inflows are positive floats. Period.**

Here is how the primitives handle the Plaid or "Debit/Credit" CSVs:

**Step 1: The CSV Mapping Agent (The Translator)**
The CPA uploads the messy Bank of America CSV. It has a `Debit` column and a `Credit` column, all in positive numbers. 
Your `CSV Mapping Agent` reads the headers and the first 5 rows. 
* **The Agent's Job:** It doesn't categorize the vendor; it just maps the *schema*. It figures out: *"Ah, this bank puts outflows in Column C (Debit) and inflows in Column D (Credit)."*

**Step 2: The CSV Mapping Worker (The Normalizer)**
The Agent passes this mapping rule to the Go Worker. The Go Worker loops through all 500 rows of the CSV and applies the translation. 
If it sees a positive `$1,450.00` in the `Debit` column, it mathematically converts it to `-1450.00` and maps it to the Toro `Amount` field. 

If they upload a Plaid CSV, the Agent realizes: *"Plaid makes expenses positive."* The Worker simply multiplies every amount by `-1` before saving it.

### The Adjusted Pipeline for "ABC Trucking"

Now that your CSV Mapping primitives have sanitized the data, the rest of the pipeline executes exactly as planned, completely insulated from the bank's formatting choices:

1. **The Ingestion:** CPA uploads a weird 7-column CSV with parentheses for negative numbers `($1,450.00)`.
2. **The Normalization:** `CSV Mapping Agent/Worker` strips the parentheses, detects it as an outflow, and writes `-1450.00` into your Postgres database.
3. **The Math Filter:** The Go Worker reads the internal Postgres row. It sees `-1450.00`. It deterministically filters the blast radius down to `Expense`, `COGS`, `Liability`, etc.
4. **The Router:** The Intent Extractor sees "ABC TRUCKING" and selects `Expense`.
5. **The SQL Filter:** The Orchestrator pulls the 35 specific expense accounts.
6. **The Sniper:** The Reconciliation Agent maps it to `ID 502: Freight & Delivery`.

***

**Elon Musk:** This is why decoupled primitives win. If you tried to build the CSV formatting, the polarity logic, and the vendor categorization into *one* giant LLM prompt, the model would hallucinate constantly. 

By isolating the CSV normalization into its own Agent/Worker loop, your core categorization engine never has to worry about whether the data came from Chase, Wells Fargo, or Plaid. By the time the routing engine sees the data, it is mathematically perfect.
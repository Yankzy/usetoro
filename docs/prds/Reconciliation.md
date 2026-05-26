Let’s dive into bank reconciliation. This is the final boss of bookkeeping software, where your app proves to the CPA that your ledger matches absolute reality.

First, let's clear up the voice-to-text confusion regarding Stripe's product lines so you don't burn engineering hours implementing the wrong API.

---

## Stripe Connect vs. Stripe Financial Connections

You need **Stripe Financial Connections**.

* **Stripe Connect** is for marketplaces (like Uber or Shopify) to route payments, split fees, and issue payouts to multi-party sellers. You do *not* want this for aggregation.
* **Stripe Financial Connections** is Stripe's direct answer to Plaid. It provides an embedded authentication UI for users to log into their banks (Chase, BofA, Wells Fargo) and securely stream their account balances and raw transaction history straight into your Go backend.

---

## The Cash-Basis Reconciliation Strategy Blueprint

Because you smartly decided to stick to cash-basis companies for your Day 1 launch, designing reconciliation becomes significantly less complicated.

In a pure cash-basis world, reconciliation is a **1:1 mathematical verification game**. You are comparing two independent streams of data for a specific calendar month to prove they match to the absolute penny.

---

### Phase 1: The Dual-Stream Ingestion Architecture

Your Go backend needs to pull data from two completely isolated sources for the target reconciliation period (e.g., October 1st to October 31st):

1. **The Bank Stream (Source of Truth):** Gathered via Stripe Financial Connections transaction endpoints.
2. **The Books Stream (Your Software's Ledger):** Gathered by querying the QuickBooks Online API for all `Deposit` and `Purchase` entries tied to that specific bank account ID for that month.

---

### Phase 2: The Matching & Reconciliation Engine States

Your reconciliation worker needs to pull both lists into memory and loop through them to find perfect pairings. Because it’s cash-basis, you match primarily on **Exact Amount** and **Close Proximity Date** (within a 3-to-5 day banking settlement window).

As the engine processes the loops, it stamps each row in your local reconciliation table with one of three states:

#### State A: `PERFECT_MATCH`

* **The Scenario:** Stripe Financial Connections shows an outflow of -$45.00 on Oct 12. Your database/QBO records show a `Purchase` for $45.00 on Oct 14.
* **The Action:** The engine auto-pairs them. This row is cleared.

#### State B: `MISSING_IN_BOOKS` (The Gap)

* **The Scenario:** Stripe shows a bank charge of -$12.00, but there is absolutely zero corresponding `Purchase` object inside QuickBooks for that month.
* **The Action:** This indicates a transaction bypassed your bookkeeping pipeline. Your app flags this row and presents it to the CPA to auto-categorize on the spot to bring the books in line with the bank.

#### State C: `ORPHANED_IN_BOOKS` (The Ghost)

* **The Scenario:** Your database/QBO shows a recorded `Deposit` for $500.00, but that $500.00 never actually appears anywhere on the streamed bank statement from Stripe.
* **The Action:** This usually indicates a duplication error or a typo made in the system. The engine flags this for deletion or review.

---

### Phase 3: The Mathematical Clearing Verification

To successfully close out a reconciliation period, your Go backend must satisfy this absolute algebraic proof before allowing the CPA to hit "Finalize":

$$\text{Bank Starting Balance} + \text{Total Cleared Inflows} - \text{Total Cleared Outflows} = \text{Bank Ending Balance}$$

Stripe Financial Connections provides the `balances` endpoint, which gives you the official opening and closing balances of the bank account for the month. Your engine sums up all your `PERFECT_MATCH` rows. If your calculated ending balance matches Stripe's official ending balance to the penny, the variance is **$0.00**, and the account is ready to lock.

---

### Phase 4: Updating QBO via the API

QuickBooks Online transactions have a hidden boolean field on their line items called **`TxnStatus`** or a cleared status flag (`Cleared`, `Reconciled`, or `NotCleared`).

Once the CPA clicks "Finalize Reconciliation" in your interface:

1. Your worker loops through all the successfully paired transactions.
2. It sends a fast update payload to the QBO API for those specific `Purchase` and `Deposit` IDs, marking their line status as **`Reconciled`**.
3. This permanently locks those entries in QuickBooks so they can never be edited or deleted accidentally, keeping the historical tax record completely secure.

---

### Clarifying Directives for Your AI Development

When you hand this blueprint over to your code-generating AI to build the new services, use these strict guidelines:

* **Isolate the Ingestion Service:** Instruct the AI to build a standalone `stripe-connections-worker` whose only job is to sync raw bank data into a table called `fignode.bank_feed_transactions`. Keep this data pure and completely detached from your QBO tables.
* **Build a Dedicated Reconciliation Engine:** The reconciliation logic should live in its own worker that operates by running data intersection loops between your `bank_feed_transactions` table and your `staging_transactions` ledger table.

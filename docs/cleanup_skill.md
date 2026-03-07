Let's break down what a "QBO Cleanup" actually is in the real world. When a CPA inherits a mess from a client who tried to do their own bookkeeping, they are usually dealing with five specific nightmares:

1. **The "Uncategorized" Dumpster Fire:** Hundreds of transactions sitting in "Ask My Accountant" or "Uncategorized Expense."
2. **Vendor Bloat:** "Home Depot," "The Home Depot," and "Home Depot #4421" all existing as separate vendors.
3. **Chart of Accounts (CoA) Duplication:** "Meals & Entertainment" vs. "Client Dinners."
4. **Zombie AR/AP:** Unpaid invoices or bills from three years ago that the client already settled in cash but never matched in the system.
5. **Reconciliation Discrepancies:** The QBO balance says $40k, the actual Chase bank feed says $12k.

If we build a `/cleanup` skill, it cannot be a black box that just blindly rewrites the database. That will terrify the CPA. It needs to be an orchestrated, multi-step agentic workflow that leans heavily into the Swiping UX we already built.

What if the `/cleanup` skill operates in three distinct phases?

### Phase 1: The Diagnostic Sweep (The Estimate)

The CPA opens the chat and types: `/cleanup scan`
The Agent uses our `/query` primitive to ingest the entire QBO ledger, looking for those 5 nightmares.
Because we are operating in a **Token Economy**, the Agent replies with a diagnostic report and a quote:

> *"I found 412 uncategorized transactions, 14 duplicate vendors, and $12,400 in stale Accounts Receivable from 2023. Fixing this will require approx 2.5M tokens. Estimated cost: $12.50. Shall I draft the cleanup?"*

This is brilliant for monetization. The CPA is about to charge the client $3,000 for this cleanup. Clicking "Yes" for $12.50 of token compute is a complete no-brainer.

### Phase 2: The Agentic Draft (Orchestrating Primitives)

The Agent goes to work. But it doesn't commit the changes—it *drafts* them using our other skills.

* It uses the `/query` skill to see how *other* businesses on the Toro network categorize "Stripe Fees" to fix the uncategorized list.
* It uses fuzzy string matching to group the duplicate Home Depot vendors.
* It flags the Zombie AR to be written off to "Bad Debt."

### Phase 3: The "Tinder for Cleanup" (Human in the Loop)

Once the Agent finishes the draft, it doesn't just present a massive, boring spreadsheet. It pushes the drafted fixes to the Fignode mobile app as a deck of cards.
The CPA is sitting on the couch, drinking a beer, and their phone says: *Cleanup Ready for Review.*

* **Card 1:** *Merge Vendors: 'Home Depot' + 'The Home Depot' -> Swipe Right to approve.*
* **Card 2:** *Reclassify 40 transactions from 'Uncategorized' to 'Software Subscriptions' -> Swipe Right.*
* **Card 3:** *Write off Invoice #1042 (2023) to Bad Debt -> Swipe Left to reject.*

From an AI perspective, `/cleanup` is essentially a **Macro-Skill**. It's a parent agent that automatically orchestrates the `/query`, `/mutate`, and `/ripple` atomic primitives we already defined.
# Technical PRD: Fignode Employee Classification Backend

## 1. Product Overview
The backend system serves an internal application for an accounting firm. Employees of the firm will use the application to review and categorize bookkeeping transactions for various clients. The system ingests transactions from clients (e.g., via QuickBooks Online integrated previously), presents them to employees in a streamlined queue, and records their classifications.

## 2. Shift from "Crowdsourced Player" to "Firm Employee"
The previous schema was designed for a public crowdsourced gamification platform where anonymous "players" ranked up and earned payouts. The new direction requires trimming public-scale dispute/consensus models and monetization (bounties, Stripe payouts) in favor of employee performance tracking and workflow efficiency.

### What to Trim (Remove)
*   **Monetization & Bounties:** Remove `pending_balance`, `available_balance`, `bounty`, `bounty_earned`, `stripe_account_id`. Employees are paid via payroll, not per transaction.
*   **Withdrawals:** Drop the `fignode.withdrawals` table completely.
*   **Ghost Banning:** Remove `is_ghost_banned` and `strikes`. Employees won't be silently ghost-banned; instead, their error rates will be tracked for manager review.
*   **Heavy Consensus & Honey Pots:** Disable or simplify the "3-vote consensus" (`consensus_required`). An accounting firm typically relies on single-employee classification or a Maker-Checker process (Junior -> Senior). "Honey pots" (fake transactions to test trust) may be removed or reserved strictly for trainee onboarding.

### What to Keep / Modify
*   **Employee Profiles:** Rename `player_profiles` to `employee_profiles`.
*   **Client Association:** Ensure `fignode.transactions` cleanly links to a Client record (currently using `owner_user_id` which references the QBO connected client).
*   **Performance Metrics:** Keep `streak`, `total_cleared`, `today_cleared`, and Leaderboards. These now serve as transparent employee productivity metrics (KPIs) to keep work engaging.
*   **Trust Score -> Accuracy Score:** Keep `trust_score` but re-contextualize it as an internal `ai_accuracy_score` based on AI suggestion.

## 3. Schema Updates (SQL modifications)

### 3.1 `fignode.employee_profiles` (replaces `player_profiles`)
*   `user_id UUID PRIMARY KEY` (References `toro_core.users`)
*   `email TEXT UNIQUE`
*   `first_name TEXT`
*   `last_name TEXT`
*   `is_manager BOOLEAN`
*   `ai_accuracy_score NUMERIC` (replaces `trust_score`)
*   `total_cleared INT`, `today_cleared INT`, `streak INT` (KPIs)
*   **Dropped:** `stripe_account_id`, `strikes`, `is_ghost_banned`, `pending_balance`, `available_balance`.

### 3.2 `fignode.transactions`
*   `status` enum: `open`, `in_review`, `cleared`, `exported_to_qbo`. (Replaces `consensus_status`).
*   **Dropped:** `bounty`, `multiplier`, `is_honey_pot` (unless kept for training).
*   Maintain `owner_user_id` as the Client reference.

### 3.3 `fignode.classifications`
*   Records who categorized what.
*   **Added:** `approved_by UUID` (for Senior review workflow if needed).
*   **Dropped:** `is_honey_pot`, `bounty_earned`, `consensus_progress`, `consensus_required`.

### 3.4 Unchanged / Minor Tweaks
*   `fignode.categories`, `fignode.skips`, `fignode.batches`, `fignode.leaderboard_snapshots`, `fignode.badges` can remain for internal use and gamification.

## 4. Required Go Backend APIs

### 4.1 Auth & User Management
*   `POST /api/auth/login` (Employee login)
*   `GET /api/employees/me` (Fetch employee profile & KPI stats (Daily/Weekly cleared metrics, streaks))

### 4.2 Transaction Queue & Workflow
*   `GET /api/transactions/batch` (Fetch a batch of open transactions for the employee to categorize, filtered by their assigned clients or a global queue).
*   `POST /api/transactions/{id}/classify` (Submit a category for a transaction).
*   `POST /api/transactions/{id}/skip` (Skip transaction if unable to categorize, maybe requiring a reason).

### 4.3 Client Management & QBO Integration
*   *(Existing)* OAuth flow to connect Client QBO accounts.
*   Background workers to sync incoming transactions from QBO/Plaid and insert them into `fignode.transactions`.
*   Background workers to sync `cleared` transactions back to QBO with the selected category.

### 4.4 Dashboard & Performance (Gamification)
*   `GET /api/leaderboard` (Internal leaderboard ranking employees by `total_cleared`).

## 5. Implementation Plan
1.  **Refactor SQL Schema:** Write a migration (Goose) to rename the profile table, drop financial/payout columns, and trim table structures as proposed.
2.  **Update SQLC Queries:** Re-write the `sqlc` query definitions to match the new schema, removing references to consensus voting counts and bounties.
3.  **Implement Go Services:** Rebuild the backend handlers and services (e.g., `TransactionService`, `EmployeeService`) in Go to expose the endpoints.

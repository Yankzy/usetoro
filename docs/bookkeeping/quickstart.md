# Quickstart Guide

This guide details the exact steps to spin up required services, seed the production-like synthetic company in PostgreSQL, execute the end-to-end bookkeeping session, and query the resulting state through the natural-language Operator REPL.

---

## Prerequisites

- **macOS** or **Linux** with `zsh` or `bash`
- **Python 3.12** with repository virtual environment at `.venv/`
- **Go 1.22+**
- **Docker** & **Docker Compose** (for PostgreSQL, Redis, and NATS services)
- **OpenAI API Key** (optional for deterministic REPL evaluation; required for conversational Operator queries)

---

## 1. Environment & Service Setup

### Step 1.1: Activate Virtual Environment
Ensure you are using the repository's dedicated virtual environment:

```bash
source .venv/bin/activate
```

Verify Python version and dependencies:
```bash
python --version  # Should be Python 3.12.x
python -c "import django, ortools, nats; print('Dependencies ready')"
```

### Step 1.2: Start Infrastructure Services
Start PostgreSQL, Redis, and NATS via Docker Compose:

```bash
docker compose -f container/docker-compose.yml up -d torodb redis nats-1
```

Verify services are running and healthy:
```bash
docker compose -f container/docker-compose.yml ps
```

### Step 1.3: Configure Environment Variables
Export the required environment variables in your active shell:

```bash
export DJANGO_SETTINGS_MODULE="config.settings"
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/torodb"
export NATS_URL="nats://localhost:4222"

# Operator LLM Configuration (required for interactive natural language queries)
export OPENAI_API_KEY="your-openai-api-key"
export BOOKKEEPING_OPERATOR_MODEL="gpt-5.6-luna"
```

### Step 1.4: Run Database Migrations
Apply all Django and bookkeeping migrations:

```bash
python manage.py migrate
```

---

## 2. Start the Go ASE Bank Categorizer Worker

The bookkeeping runtime communicates with the Go Autonomous Semantic Engine (ASE) over NATS to evaluate residual bank movements.

In a separate terminal, launch the Go ASE worker:

```bash
# Build and run the Go parity/worker binary
go run ./go/cmd/parity-worker
```

Alternatively, to run the worker package tests and verify NATS contracts:
```bash
go test -v ./go/internal/workers/ -run TestBookkeepingAseBankCategorizer
```

---

## 3. Seed the Synthetic Production Company

Toro maintains an authoritative synthetic company, **Toro Synthetic Trading SARL** (`toro-synthetic-bookkeeping`), that mirrors live production datasets across all 7 lifecycle cases.

Run the seed command with `--reset` to establish a clean, known initial baseline:

```bash
python manage.py seed_production_like_bookkeeping_company --reset
```

### What This Command Seeds:
- **Canonical Owner**: `yankz@fignode.com` (verified across both `auth_user` and `toro_core.users`).
- **Entity**: `toro-synthetic-bookkeeping` with Moroccan PCGE Chart of Accounts.
- **Bank Account**: Attijariwafa Bank operating account linked to cash GL account `5141`.
- **7 Transactional Datasets**:
  1. **Case 1**: Approved customer invoice (10,000.00 MAD) + matching bank deposit (Stage 1 target).
  2. **Case 2**: Approved vendor bill (4,500.00 MAD) + matching bank withdrawal (Stage 1 target).
  3. **Case 3**: Pre-posted cash GL deposit (2,500.00 MAD) + matching bank deposit (Stage 2 target).
  4. **Case 4**: Bank outflow for DGI TVA (3,400.00 MAD) $\to$ residual classification target (Account `4456`).
  5. **Case 5**: Bank outflow for bank fees (150.00 MAD) $\to$ residual classification target (Account `6147`).
  6. **Case 6**: Bank inflow for scrap sales (1,200.00 MAD) $\to$ residual classification target (Account `7127`).
  7. **Case 7**: Ambiguous bank transfer (7,850.00 MAD) $\to$ residual HOLD target (`HOLD_AMBIGUOUS`).

---

## 4. Execute the Production Bookkeeping Session (E2E)

Run the end-to-end production runner against the seeded company:

```bash
python manage.py run_synthetic_bookkeeping_e2e
```

### Expected Output Summary:
```text
=== Production Bookkeeping Lifecycle E2E Runner ===
Resolved entity 'Toro Synthetic Trading SARL' (d0e1a1a0-7080-4500-a000-000070705001) owned by yankz@fignode.com
Preflight: Found 7 staged transactions in real database.

Executing BookkeepingSession via production application service...
Session completed in 1.42s
Result: SUCCESS

Lifecycle Stages:
- Routing: APPLIED
- Stage 1 (Payment Applications): 2 executed (Invoice 10,000 MAD, Bill 4,500 MAD)
- Stage 2 (Reconciliations): 3 active reconciliations
- Residual Categorization: 4 evaluated (3 CLASSIFIED, 1 HOLD)
- Residual Postings: 3 posted (je-101, je-102, je-103)
- Direct Reconciliations: 3 created
- Total Active Reconciliations: 6
- Unresolved Bank Items: 1 (active HOLD: 7,850.00 MAD)
- Final Persistence Revision: P8
```

### Idempotency Check:
Run the command a second time:
```bash
python manage.py run_synthetic_bookkeeping_e2e
```
The session will complete immediately with **0 new payment applications**, **0 new postings**, and the persistence revision will remain unchanged at **P8 $\to$ P8**.

---

## 5. Launch the Accountant REPL

Launch the interactive developer and accountant REPL directly against the live PostgreSQL state:

```bash
./.venv/bin/python ledger/bookkeeping_state_eval/lab.py \
  --production-state \
  --company-id toro-synthetic-bookkeeping
```

### Useful REPL Commands:
```text
(lab) > state
Company: Toro Synthetic Trading SARL (toro-synthetic-bookkeeping)
Session: lab-production-inspection-1
Local revision: S0
Persistence revision: P8

Bank items: 7
Book items: 6

Active routes: 1
Legacy book classifications: 0
Residual bank classifications: 4
Residual bank postings: 3
Active residual HOLDs: 1
Active reconciliations: 6
Executed payment applications: 2

Unresolved bank items: 1
Unresolved book items: 0

(lab) > bank
staged:144668c8-772d-4717-8357-f5a2e179b992
  7,850.00 MAD
  "VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS"
  remaining: 7,850.00 MAD
...

(lab) > residuals
Residual Decisions (4):
  * [HOLD] Item staged:... (7,850.00 MAD): VIREMENT DIVERS REF 999888777 SANS OBJET
    Account: None | Conf: N/A | Status: NO POSTING (HOLD)
    Hold Reason: HOLD_AMBIGUOUS
  * [CLASSIFIED] Item staged:... (150.00 MAD): COMMISSION BANCAIRE ET FRAIS TENUE DE CO
    Account: 6147 | Conf: 0.99 | Status: POSTED (je-101)
  * [CLASSIFIED] Item staged:... (1,200.00 MAD): VENTE DE PRODUITS ACCESSOIRES MATERIEL R
    Account: 7127 | Conf: 0.99 | Status: POSTED (je-102)
  * [CLASSIFIED] Item staged:... (3,400.00 MAD): TELEPAIEMENT DIRECTION GENERALE DES IMPO
    Account: 4456 | Conf: 0.99 | Status: POSTED (je-103)
```

---

## 6. Ask Natural-Language Questions

Type questions directly into the prompt. The Bookkeeping Operator will inspect the hydrated state using typed read-only tools and respond truthfully:

```text
(lab) > classification issues?

Toro Operator is thinking...

Toro:
There is 1 classification issue currently on hold:
- Bank item: staged:144668c8-772d-4717-8357-f5a2e179b992
- Amount: 7,850.00 MAD
- Description: "VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS"
- Status: HOLD
- Reason: HOLD_AMBIGUOUS
- Rationale: Description is ambiguous or missing source context.
- Posting: No accounting posting exists.

Three other residual bank items were classified and posted:
- 150.00 MAD -> account 6147 (Bank fees)
- 1,200.00 MAD -> account 7127 (Accessory sales)
- 3,400.00 MAD -> account 4456 (DGI VAT)
```

```text
(lab) > why is it unresolved?

Toro:
The 7,850.00 MAD bank movement remains unresolved because it was placed on HOLD due to ambiguous description context (HOLD_AMBIGUOUS). No candidate book item hypotheses or open obligations exist to match it, and supporting evidence has not yet been provided.
```

To exit the REPL, type:
```text
(lab) > quit
```

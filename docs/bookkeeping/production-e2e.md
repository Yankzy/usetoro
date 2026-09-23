# Production E2E Verification Runbook

This runbook documents the exact operational workflow to seed, execute, verify, and query the production-like synthetic company in PostgreSQL.

---

## 1. Prerequisites & Topology

The synthetic production benchmark verifies the entire bookkeeping runtime on real PostgreSQL tables without relying on in-memory mocks:

- **Target Tenant**: `toro-synthetic-bookkeeping` ("Toro Synthetic Trading SARL")
- **Owner Account**: `yankz@fignode.com`
  - Must exist in `auth_user` (Django auth).
  - Must exist in `toro_core.users` (Go/SQL tenant registry).
- **Chart of Accounts**: Moroccan PCGE (Plan Comptable Général des Entreprises)
- **Operating Currency**: `MAD`
- **Bank Account**: Attijariwafa Bank Principal (GL Account `5141`)

---

## 2. Step-by-Step Runbook

### Step 2.1: Start Services & Go ASE Worker

Ensure PostgreSQL, Redis, and NATS are active:
```bash
docker compose -f container/docker-compose.yml up -d torodb redis nats-1
```

In a dedicated terminal, launch the Go ASE worker:
```bash
go run ./go/cmd/parity-worker
```

### Step 2.2: Seed the Dedicated Synthetic Company
Run the seeding command with `--reset` to clear any prior transactional data and establish a clean baseline:

```bash
python manage.py seed_production_like_bookkeeping_company --reset
```

#### Verification:
The command outputs:
```text
=== Synthetic Bookkeeping Bootstrap for user: yankz@fignode.com ===
Resolved existing canonical user: yankz@fignode.com (auth_user_id=1, toro_user_id=..., org_entity_id=...)
Resetting existing transactional data for dedicated synthetic entity 'toro-synthetic-bookkeeping'...
  [Case 1] Seeded approved Invoice INV-SYNTH-2026-001 (10000.00 MAD)
  [Case 2] Seeded approved Bill BILL-SYNTH-2026-001 (4500.00 MAD)
  [Case 3] Seeded posted cash GL Journal Entry (2500.00 MAD)
Synthetic company successfully prepared with 7 staged bank transactions covering all 7 lifecycle cases.
```

---

### Step 2.3: Execute the Full E2E Bookkeeping Session
Run the production E2E runner:

```bash
python manage.py run_synthetic_bookkeeping_e2e
```

#### Verification of Terminal Execution Report:
```text
=== Production Bookkeeping Lifecycle E2E Runner ===
Resolved entity 'Toro Synthetic Trading SARL' (d0e1a1a0-7080-4500-a000-000070705001) owned by yankz@fignode.com
Preflight: Found 7 staged transactions in real database.
Session ID: synth-session-1726938000

Executing BookkeepingSession via production application service...

======================================================================
                  BOOKKEEPING SESSION EXECUTION REPORT                
======================================================================
Overall Status: SUCCESS
Starting Revision: P1  -->  Final Revision: P8
Total Duration: 1.38s

LIFECYCLE STAGES:
  * Routing:                APPLIED
  * Legacy Book DAG:        SKIPPED (0 eligible items)
  * Stage 1 (Payment Apps): APPLIED (2 executed payments, 2 rehydrations)
  * Stage 2 (Reconciliation): APPLIED (1 batch, 1 active reconciliation)
  * Residual Categorization: APPLIED (4 decisions: 3 CLASSIFIED, 1 HOLD)
  * Residual Posting Loop:  APPLIED (3 postings executed, 3 rehydrations)
  * Final Direct Reconciliations: 3 created

ECONOMIC CLOSURE SUMMARY:
  * Total Bank Items:        7
  * Reconciled Bank Items:   6 (Exact value-conserved match)
  * Unresolved Bank Items:   1 (7,850.00 MAD on durable HOLD)
  * Unresolved Book Items:   0
  * Active Reconciliations:  6
======================================================================
```

---

### Step 2.4: Verify Idempotency (Second Run)
Execute the E2E command a second time without modifying the database:

```bash
python manage.py run_synthetic_bookkeeping_e2e
```

#### Expected Idempotent Behavior:
- **Starting Revision**: `P8`
- **Final Revision**: `P8`
- **Payment Applications**: `0 executed`
- **Residual Postings**: `0 executed` (NOOP replayed)
- **Unresolved Bank Items**: Still exactly `1` (`7,850.00 MAD` on `HOLD`)
- **Status**: `SUCCESS`

---

## 3. The 7 Production Lifecycle Scenarios

| Case | ID | Staged Transaction Narrative | Amount | Handled By | Outcome |
|---|---|---|---|---|---|
| **Case 1** | `FIT-SYNTH-CASE1-INVOICE` | `"VIREMENT CLIENT ATLAS SARL REF INV-SYNTH-2026-001"` | +10,000.00 MAD | Stage 1 Payment Application | Settle `INV-SYNTH-2026-001`, post cash JE, direct link. |
| **Case 2** | `FIT-SYNTH-CASE2-BILL` | `"VIREMENT FOURNISSEUR EQUIPEMENT SARL BILL-SYNTH-2026-001"` | -4,500.00 MAD | Stage 1 Payment Application | Settle `BILL-SYNTH-2026-001`, post cash JE, direct link. |
| **Case 3** | `FIT-SYNTH-CASE3-CASHGL` | `"VERSEMENT ESPECES AGENCE BANCAIRE REF DEP-4412"` | +2,500.00 MAD | Stage 2 CP-SAT Reconciliation | Match existing cash GL JE leg on account `5141`. |
| **Case 4** | `FIT-SYNTH-CASE4-DGIVAT` | `"TELEPAIEMENT DIRECTION GENERALE DES IMPOTS TVA DGI REF 202604"` | -3,400.00 MAD | Residual Categorization & Posting | Go ASE classifies to `4456`. Post JE: Dr `4456` / Cr `5141`. |
| **Case 5** | `FIT-SYNTH-CASE5-BANKFEE` | `"COMMISSION BANCAIRE ET FRAIS TENUE DE COMPTE ATT-0426"` | -150.00 MAD | Residual Categorization & Posting | Go ASE classifies to `6147`. Post JE: Dr `6147` / Cr `5141`. |
| **Case 6** | `FIT-SYNTH-CASE6-MISCINC` | `"VENTE DE PRODUITS ACCESSOIRES MATERIEL REBUT"` | +1,200.00 MAD | Residual Categorization & Posting | Go ASE classifies to `7127`. Post JE: Dr `5141` / Cr `7127`. |
| **Case 7** | `FIT-SYNTH-CASE7-HOLD` | `"VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS"` | +7,850.00 MAD | Residual Categorization | Go ASE returns `HOLD` (`HOLD_AMBIGUOUS`). Surfaces in REPL. |

---

## 4. Launching the Production State REPL

Following the E2E session, verify and explore the state interactively:

```bash
./.venv/bin/python ledger/bookkeeping_state_eval/lab.py \
  --production-state \
  --company-id toro-synthetic-bookkeeping
```

### Verification Checklist in REPL:
1. Run `state`:
   - Verify `Active residual HOLDs: 1`
   - Verify `Residual bank postings: 3`
   - Verify `Residual bank classifications: 4`
   - Verify `Active reconciliations: 6`
   - Verify `Unresolved bank items: 1`
2. Run `bank`:
   - Verify all amounts render in human-readable currency with two decimals (e.g. `7,850.00 MAD`, not `78,500,000`).
3. Ask: `classification issues?`
   - Operator reports 1 issue on HOLD for the 7,850.00 MAD ambiguous transfer.
4. Ask: `why is it unresolved?`
   - Operator explains the `HOLD_AMBIGUOUS` reason and missing context without inventing facts.

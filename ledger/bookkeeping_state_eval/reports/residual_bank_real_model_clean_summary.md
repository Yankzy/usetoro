# Real-Model Residual-Bank Categorization Evaluation Summary

- **Execution Mode**: `REAL_MODEL_DOMAIN_TOOLS`
- **Model / Provider**: `gpt-5.4-mini`
- **Candidate Source**: `DJANGO_LEDGER_DEFAULT_COA`
- **PostgreSQL Database Target**: `127.0.0.1:5435/toro`
- **Eval Entity Slug / UUID**: `eval-residual-bank` (`79519ce6-d528-4312-83a5-5f35ade32ad5`)
- **Authoritative CoA Account Count**: **29** accounts (including `1481` and `1111`)
- **Wire Schema**: `bookkeeping.ase.bank_categorize.v1`
- **Execution DAG**: `bookkeeping_bank_categorization_v1`
- **Generated At**: 2026-09-18T01:07:24.821922+00:00

## 1. Corpus Demographics & Evaluation Boundary

- **Corpus Size**: **15** residual bank items
- **Exact-Code Denominator**: **9** items
- **HOLD Denominator**: **6** items
- **Macro Family Denominator**: **9** items

## 2. Headline Accuracy & Performance Metrics

| Metric | Hits / Total | Rate |
| :--- | :--- | :--- |
| **Raw CoA Presence** (Authoritative DB CoA) | 9/9 | **100.0%** |
| **Candidate Recall** (Admissible Resolver Candidates) | 8/9 | **88.9%** |
| **Conditional Exact Accuracy** (When Candidate Available) | 8/8 | **100.0%** |
| **Overall Exact-Code Accuracy** | 8/9 | **88.9%** |
| **Macro-Family Accuracy** | 9/9 | **100.0%** |
| **Terminal Accuracy (CLASSIFIED vs HOLD)** | 14/15 | **93.3%** |

## 3. HOLD & Safety Boundary Metrics

- Expected HOLDS: **6**
- True Positives (Correctly Held): **6**
- False Positives (Incorrectly Held): **1**
- False Negatives (Should Hold, but Classified): **0**
- **HOLD Precision**: **85.7%**
- **HOLD Recall**: **100.0%**

## 5. Performance Split by Category

| Category | Items | Exact Code Acc | Terminal Acc |
| :--- | :--- | :--- | :--- |
| `HOLD_SAFETY` | 6 | N/A (All HOLD) | 6/6 (100.0%) |
| `INFLOW` | 4 | 3/4 (75.0%) | 3/4 (75.0%) |
| `OUTFLOW` | 5 | 5/5 (100.0%) | 5/5 (100.0%) |

## 6. Forensic Diagnostic Table for Exact-Code Items

| Item ID | Dir | Expected | Macro Exp | Raw CoA? | Macro Pred | Conf | Macro OK? | Candidate OK? | Selected | Conf | Terminal | Primary Failure Layer |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `res-out-fee-01` | OUTFLOW | `6147` | EXPENSE | YES | EXPENSE | 0.99 | YES | YES | `6147` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-supplies-02` | OUTFLOW | `6125` | EXPENSE | YES | EXPENSE | 0.99 | YES | YES | `6125` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-telecom-03` | OUTFLOW | `6134` | EXPENSE | YES | EXPENSE | 0.99 | YES | YES | `6134` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-tax-04` | OUTFLOW | `4456` | LIABILITY | YES | LIABILITY | 0.99 | YES | YES | `4456` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-asset-05` | OUTFLOW | `2355` | ASSET | YES | ASSET | 0.99 | YES | YES | `2355` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-in-merch-01` | INFLOW | `7111` | REVENUE | YES | REVENUE | 0.99 | YES | YES | `7111` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-in-service-02` | INFLOW | `7124` | REVENUE | YES | REVENUE | 0.96 | YES | NO | `None` | N/A | `HOLD` | `confidence_hold` |
| `res-in-loan-03` | INFLOW | `1481` | LIABILITY | YES | LIABILITY | 0.99 | YES | YES | `1481` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-in-capital-04` | INFLOW | `1111` | EQUITY | YES | EQUITY | 0.99 | YES | YES | `1111` | 1.00 | `CLASSIFIED` | None (Success) |

## 7. Failure Decomposition by Causal Layer

| Causal Layer | Count |
| :--- | :--- |
| `raw_coa_missing` | 0 |
| `macro_routing_miss` | 0 |
| `candidate_filtering_miss` | 0 |
| `model_account_selection_miss` | 0 |
| `confidence_hold` | 1 |
| `terminal_or_contract_issue` | 0 |

### Detailed Observed Failures:

- Case `res-in-service-02` (INFLOW):
  - **Primary Layer**: `confidence_hold`
  - **Expected**: `7124`
  - **Observed**: Terminal=`HOLD`, Code=`None`, HoldReason=`top candidate 'REVENUE' confidence (0.96) below 0.98 guardrail during routing at bank_macro_classifier_inflow. AI Reasoning: The description 'HONORAIRES PRESTATION SERVICE FORMATION PRO' indicates service fees / training services rendered, which fall under operating revenue in Moroccan PCGE (712x). No evidence suggests an internal transfer or liability/equity inflow.`
  - **Forensic Diagnosis**: Macro 'REVENUE' correctly recognized, but router confidence (0.96) below 0.98 halted execution before resolver.
  - **Rationale**: The description 'HONORAIRES PRESTATION SERVICE FORMATION PRO' indicates service fees / training services rendered, which fall under operating revenue in Moroccan PCGE (712x). No evidence suggests an internal transfer or liability/equity inflow.

## 8. Safety & Invariant Audit Confirmation

- **Zero Synthetic Catalog Fallback**: Verified 100% of candidate accounts retrieved via authoritative SQL `AuthoritativeDjangoCoaQuery`.
- **Candidate Source**: Confirmed `DJANGO_LEDGER_DEFAULT_COA` telemetry tag across all responses.
- **Unified PostgreSQL Persistence**: Verified Django fixture setup and Go ASE worker query the identical PostgreSQL database instance.
- **Pre-Flight Parity Asserted**: Confirmed 100% code parity between Django ORM and Go raw SQL prior to model execution.
- **Safety Hold Enforcement**: Confirmed internal transfer descriptions (`VIREMENT INTERNE`, `VIR COMPTE A COMPTE`, `TRANSIT 5115`) fail closed to semantic HOLD.
- **Global Threshold Integrity**: Preserved 0.98 global ASE threshold without relaxation.

RESIDUAL_BANK_REAL_MODEL_EVAL_COMPLETE

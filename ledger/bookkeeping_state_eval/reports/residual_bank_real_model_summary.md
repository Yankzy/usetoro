# Real-Model Residual-Bank Categorization Evaluation Summary

- **Execution Mode**: `REAL_MODEL_DOMAIN_TOOLS`
- **Model / Provider**: `gpt-5.4-mini`
- **Candidate Source**: `DJANGO_LEDGER_DEFAULT_COA`
- **Wire Schema**: `bookkeeping.ase.bank_categorize.v1`
- **Execution DAG**: `bookkeeping_bank_categorization_v1`
- **Generated At**: 2026-09-17T00:38:47.480164+00:00

## 1. Corpus Demographics & Evaluation Boundary

- **Corpus Size**: **15** residual bank items
- **Exact-Code Denominator**: **9** items
- **HOLD Denominator**: **6** items
- **Macro Family Denominator**: **9** items

## 2. Headline Accuracy & Performance Metrics

| Metric | Hits / Total | Rate |
| :--- | :--- | :--- |
| **Raw CoA Presence** (Authoritative DB CoA) | 7/9 | **77.8%** |
| **Candidate Recall** (Admissible Resolver Candidates) | 5/9 | **55.6%** |
| **Conditional Exact Accuracy** (When Candidate Available) | 5/5 | **100.0%** |
| **Overall Exact-Code Accuracy** | 5/9 | **55.6%** |
| **Macro-Family Accuracy** | 8/9 | **88.9%** |
| **Terminal Accuracy (CLASSIFIED vs HOLD)** | 11/15 | **73.3%** |

## 3. HOLD & Safety Boundary Metrics

- Expected HOLDS: **6**
- True Positives (Correctly Held): **6**
- False Positives (Incorrectly Held): **4**
- False Negatives (Should Hold, but Classified): **0**
- **HOLD Precision**: **60.0%**
- **HOLD Recall**: **100.0%**

## 4. Replicate Stability Across Repeated Runs

- Replicate Count: **3**
- Overall Mean Stability Rate: **97.8%**
- Perfectly Stable Items: **14/15**

## 5. Performance Split by Category

| Category | Items | Exact Code Acc | Terminal Acc |
| :--- | :--- | :--- | :--- |
| `HOLD_SAFETY` | 6 | N/A (All HOLD) | 6/6 (100.0%) |
| `INFLOW` | 4 | 2/4 (50.0%) | 2/4 (50.0%) |
| `OUTFLOW` | 5 | 3/5 (60.0%) | 3/5 (60.0%) |

## 6. Forensic Diagnostic Table for Exact-Code Items

| Item ID | Dir | Expected | Macro Exp | Raw CoA? | Macro Pred | Conf | Macro OK? | Candidate OK? | Selected | Conf | Terminal | Primary Failure Layer |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `res-out-fee-01` | OUTFLOW | `6147` | EXPENSE | YES | EXPENSE | 0.95 | YES | NO | `None` | N/A | `HOLD` | `confidence_hold` |
| `res-out-supplies-02` | OUTFLOW | `6125` | EXPENSE | YES | EXPENSE | 0.99 | YES | YES | `6125` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-telecom-03` | OUTFLOW | `6134` | EXPENSE | YES | EXPENSE | 0.99 | YES | YES | `6134` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-out-tax-04` | OUTFLOW | `4455` | LIABILITY | YES | EXPENSE | 0.99 | NO | NO | `None` | N/A | `HOLD` | `macro_routing_miss` |
| `res-out-asset-05` | OUTFLOW | `2355` | ASSET | YES | ASSET | 0.99 | YES | YES | `2355` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-in-merch-01` | INFLOW | `7111` | REVENUE | YES | REVENUE | 0.99 | YES | YES | `7111` | 0.98 | `CLASSIFIED` | None (Success) |
| `res-in-service-02` | INFLOW | `7124` | REVENUE | YES | REVENUE | 0.99 | YES | YES | `7124` | 0.99 | `CLASSIFIED` | None (Success) |
| `res-in-loan-03` | INFLOW | `1481` | LIABILITY | NO | LIABILITY | 0.99 | YES | NO | `5141` | 0.95 | `HOLD` | `raw_coa_missing` |
| `res-in-capital-04` | INFLOW | `1111` | EQUITY | NO | EQUITY | 0.99 | YES | NO | `None` | N/A | `HOLD` | `raw_coa_missing` |

## 7. Failure Decomposition by Causal Layer

| Causal Layer | Count |
| :--- | :--- |
| `raw_coa_missing` | 2 |
| `macro_routing_miss` | 1 |
| `candidate_filtering_miss` | 0 |
| `model_account_selection_miss` | 0 |
| `confidence_hold` | 1 |
| `terminal_or_contract_issue` | 0 |

### Detailed Observed Failures:

- Case `res-out-fee-01` (OUTFLOW):
  - **Primary Layer**: `confidence_hold`
  - **Expected**: `6147`
  - **Observed**: Terminal=`HOLD`, Code=`None`, HoldReason=`top candidate 'EXPENSE' confidence (0.95) below 0.98 guardrail during routing at bank_macro_classifier_outflow. AI Reasoning: The description 'FRAIS TENUE DE COMPTE T2 2026' indicates bank account maintenance fees, which fall under operating expenses according to Moroccan accounting standards.`
  - **Forensic Diagnosis**: Macro 'EXPENSE' correctly recognized, but router confidence (0.95) below 0.98 halted execution before resolver.
  - **Rationale**: The description 'FRAIS TENUE DE COMPTE T2 2026' indicates bank account maintenance fees, which fall under operating expenses according to Moroccan accounting standards.

- Case `res-out-tax-04` (OUTFLOW):
  - **Primary Layer**: `macro_routing_miss`
  - **Expected**: `4455`
  - **Observed**: Terminal=`HOLD`, Code=`None`, HoldReason=`HOLD_INSUFFICIENT_EVIDENCE`
  - **Forensic Diagnosis**: Macro router selected 'EXPENSE' instead of expected 'LIABILITY', causing target accounts to be excluded.
  - **Rationale**: Model output invalid for account resolution: no account code found in model output: {
  "account_code": null,
  "confidence": 0.95,
  "reasoning": "The transaction 'TELEPAIEMENT DGI TVA TRIMESTRIELLE' refers to a tax payment, specifically the quarterly Value Added Tax (TVA) to the General Directorate of Taxes (DGI). This type of transaction does not match any of the provided expense or cost of goods sold account codes in the candidate list, as it involves tax obligations rather than operational expenses or purchases. According to Moroccan PCGE standards, a specific account code for tax payments would typically be used, but since it is not available in the pre-approved list, no suitable code can be assigned confidently."
}

- Case `res-in-loan-03` (INFLOW):
  - **Primary Layer**: `raw_coa_missing`
  - **Expected**: `1481`
  - **Observed**: Terminal=`HOLD`, Code=`None`, HoldReason=`top candidate '5141' confidence (0.95) below 0.98 guardrail during routing at account_resolver. AI Reasoning: The transaction involves an inflow of funds related to a loan (bank credit release), which typically passes through a bank account. However, the transaction's macro class 'LIABILITY' suggests recording it as a loan, but without an appropriate candidate account code given, it relates most closely to the 'Banques' account for inflow processing. Clarity on the appropriate liability classification code is missing from the candidate list.`
  - **Forensic Diagnosis**: Expected code '1481' absent from authoritative company CoA in database.
  - **Rationale**: The transaction involves an inflow of funds related to a loan (bank credit release), which typically passes through a bank account. However, the transaction's macro class 'LIABILITY' suggests recording it as a loan, but without an appropriate candidate account code given, it relates most closely to the 'Banques' account for inflow processing. Clarity on the appropriate liability classification code is missing from the candidate list.

- Case `res-in-capital-04` (INFLOW):
  - **Primary Layer**: `raw_coa_missing`
  - **Expected**: `1111`
  - **Observed**: Terminal=`HOLD`, Code=`None`, HoldReason=`HOLD_INSUFFICIENT_EVIDENCE`
  - **Forensic Diagnosis**: Expected code '1111' absent from authoritative company CoA in database.
  - **Rationale**: Model output invalid for account resolution: no account code found in model output: {
  "account_code": null,
  "confidence": 0.00,
  "reasoning": "The transaction pertains to an equity contribution, which involves capital accounts. The provided list of candidate accounts does not include equity-related accounts, thus no suitable account code can be selected."
}

## 8. Safety & Invariant Audit Confirmation

- **Zero Synthetic Catalog Fallback**: Verified 100% of candidate accounts retrieved via authoritative SQL `AuthoritativeDjangoCoaQuery`.
- **Candidate Source**: Confirmed `DJANGO_LEDGER_DEFAULT_COA` telemetry tag across all responses.
- **No Offline Heuristic Leakage**: Verified that real-model execution invoked model runtime without falling back to test heuristics.
- **Safety Hold Enforcement**: Confirmed internal transfer descriptions (`VIREMENT INTERNE`, `VIR COMPTE A COMPTE`, `TRANSIT 5115`) fail closed to semantic HOLD.
- **Global Threshold Integrity**: Preserved 0.98 global ASE threshold without relaxation.

RESIDUAL_BANK_REAL_MODEL_EVAL_COMPLETE

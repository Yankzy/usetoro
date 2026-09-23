# Simulator-vs-Go ASE Book-Categorization Parity Evaluation Summary

**Execution Mode**: `DETERMINISTIC_EXTERNAL_MODEL_STUB` (Deterministic Go ASE DAG vs Simulator)
**Generated At**: 2026-09-14T15:38:02.576521+00:00

## 1. Corpus Composition
- Total Evaluation Items: **61**
- Labeled with Independent Truth: **21**
- Unlabeled Items: **40**
- Operational Provider Failures: **0**

## 2. Accuracy & Agreement Metrics

| Dimension | Simulator | Go ASE | Cross-Provider Agreement |
| :--- | :--- | :--- | :--- |
| **Exact Accuracy vs Truth** | 18/21 (85.7%) | 1/21 (4.8%) | - |
| **Terminal Accuracy** | 18/21 (85.7%) | 21/21 (100.0%) | 47/61 (77.0%) |
| **Code Agreement (Both Classified)** | - | - | 2/46 (4.3%) |

## 3. Disagreement Taxonomy Breakdown

| Disagreement Class | Count |
| :--- | :--- |
| `ACCOUNT_CODE_MISMATCH` | 25 |
| `BOTH_WRONG` | 3 |
| `GO_WRONG_SIMULATOR_RIGHT` | 18 |
| `MATCH` | 3 |
| `SIMULATOR_WRONG_GO_RIGHT` | 1 |
| `TERMINAL_TYPE_MISMATCH` | 11 |

## 4. High-Risk Disagreements

Observed **20** high-risk disagreement(s):

### Item: `book-1` (Case: `cat_scenario_h_supplier_payment_multiple_bills`)
- **Disagreement Class**: `GO_WRONG_SIMULATOR_RIGHT`
- **Independent Expected Truth**: `4411`
- **Simulator**: `CLASSIFIED` (4411)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule supplier_payment_node on book-1"

### Item: `book-2` (Case: `cat_scenario_h_supplier_payment_multiple_bills`)
- **Disagreement Class**: `GO_WRONG_SIMULATOR_RIGHT`
- **Independent Expected Truth**: `4411`
- **Simulator**: `CLASSIFIED` (4411)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule supplier_payment_node on book-2"

### Item: `book-eur-1` (Case: `chal_challenge_03_account_currency_minefield`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-eur-1"

### Item: `book-mad-decoy` (Case: `chal_challenge_03_account_currency_minefield`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-mad-decoy"

### Item: `book-mad-sec` (Case: `chal_challenge_03_account_currency_minefield`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (4411)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule supplier_payment_node on book-mad-sec"

### Item: `book-ambiguous` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `TERMINAL_TYPE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-ambiguous"

### Item: `book-aws` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (6181)
- **Go ASE**: `CLASSIFIED` (6134)
- **Rationale**: Go: "Categorized to 6134 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule it_software_node on book-aws"

### Item: `book-client-bulk` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-client-bulk"

### Item: `book-dup-a` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-dup-a"

### Item: `book-dup-b` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-dup-b"

### Item: `book-eur-fee` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `TERMINAL_TYPE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6147)
- **Rationale**: Go: "Categorized to 6147 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-eur-fee"

### Item: `book-mad-decoy` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `TERMINAL_TYPE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6147)
- **Rationale**: Go: "Categorized to 6147 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-mad-decoy"

### Item: `book-payroll` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (6171)
- **Go ASE**: `CLASSIFIED` (4432)
- **Rationale**: Go: "Categorized to 4432 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule payroll_node on book-payroll"

### Item: `book-preexist` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-preexist"

### Item: `book-supplier` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (4411)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule supplier_payment_node on book-supplier"

### Item: `book-unmatched` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule customer_receipt_node on book-unmatched"

### Item: `book-tax-01` (Case: `gap_tax_sensitive_tva`)
- **Disagreement Class**: `GO_WRONG_SIMULATOR_RIGHT`
- **Independent Expected Truth**: `4455`
- **Simulator**: `CLASSIFIED` (4455)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "Matched simulated ASE rule tax_node on book-tax-01"

### Item: `book-asset-01` (Case: `gap_asset_capitalization_vs_expense`)
- **Disagreement Class**: `BOTH_WRONG`
- **Independent Expected Truth**: `2355`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-asset-01"

### Item: `book-rev-01` (Case: `gap_sales_revenue_inflow`)
- **Disagreement Class**: `SIMULATOR_WRONG_GO_RIGHT`
- **Independent Expected Truth**: `7111`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "Categorized to 7111 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-rev-01"

### Item: `book-xfer-01` (Case: `gap_bank_control_transfer`)
- **Disagreement Class**: `BOTH_WRONG`
- **Independent Expected Truth**: `5115`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6111)
- **Rationale**: Go: "Categorized to 6111 by pcge_account_resolver agent" | Sim: "No deterministic semantic rule matched for book-xfer-01"

## 5. Rollout Gate Recommendation

- **Current Boundary State**: Sealed & Verified (`PRODUCTION_BOOK_CATEGORIZATION_BOUNDARY_FROZEN`).
- **Recommendation**: Maintain Go ASE in shadow mode. Disagreements on non-PCGE accounts (e.g. Asset vs Expense, Payroll liabilities) should be aligned in DAG domain taxonomy before live traffic rollout.

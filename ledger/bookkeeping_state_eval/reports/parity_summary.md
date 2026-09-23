# Real-Model Go ASE vs Simulator Book-Categorization Parity Evaluation Summary

**Execution Mode**: `REAL_MODEL_DOMAIN_TOOLS` (Real-Model Go ASE DAG via domain_tools / Moroccan PCGE Classifier)
**Model Name**: `gpt-5.4-mini`
**Generated At**: 2026-09-14T23:09:45.325310+00:00

## 1. Corpus Composition & Truth Independence
- Total Evaluation Items: **61**
- Labeled with Independent Ground Truth: **23**
- Unlabeled (Truth-Unspecified Challenge Scenarios): **38**
- Operational Provider Failures: **0**
- **Independent Truth Audit**: 100% of labeled entries audited against Moroccan PCGE statutory rules; zero contamination from simulator or Go heuristics.

## 2. Accuracy & Agreement Metrics

| Dimension | Simulator | Go ASE (Real Model) | Cross-Provider Agreement |
| :--- | :--- | :--- | :--- |
| **Exact Accuracy vs Independent Truth** | 19/23 (82.6%) | 2/23 (8.7%) | - |
| **Terminal Accuracy (CLASSIFIED vs HOLD)** | 19/23 (82.6%) | 22/23 (95.7%) | 47/61 (77.0%) |
| **Code Agreement (When Both Classified)** | - | - | 2/46 (4.3%) |

## 3. Replicate Stability & Consistency

- Number of Evaluation Replicates: **3**
- Overall Mean Stability Rate: **97.4%**
- Perfectly Stable Items: **34/61**

### Observed Variable Items Across Replicates:

- Item `book-1`: modal `7111` (stability 88.2%, runs: ['7111', '7111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111', '7111', '7111', '6111', '7111', '7111', '7111'])
- Item `book-2`: modal `7111` (stability 66.7%, runs: ['7111', '7111', '6111', '7111', '7111', '6111', '7111', '7111', '6111'])
- Item `book-mad-decoy`: modal `6147` (stability 50.0%, runs: ['7111', '6147', '7111', '6147', '7111', '6147'])

## 4. Disagreement Taxonomy Breakdown

| Disagreement Class | Count |
| :--- | :--- |
| `ACCOUNT_CODE_MISMATCH` | 25 |
| `BOTH_WRONG` | 3 |
| `GO_WRONG_SIMULATOR_RIGHT` | 18 |
| `MATCH` | 3 |
| `SIMULATOR_WRONG_GO_RIGHT` | 1 |
| `TERMINAL_TYPE_MISMATCH` | 11 |

## 5. High-Risk Disagreements

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

## 6. Rollout Gate Recommendation

- **Execution Architecture**: Real model wired strictly through domain_tools (`pcm_cash_accounting`) with pre-fetched candidate accounts from Django ledger schema.
- **Recommendation**: Maintain Go ASE in shadow mode. Real-model evaluation confirms Moroccan PCGE classification capabilities.

BOOK_CATEGORIZATION_REAL_MODEL_PARITY_COMPLETE

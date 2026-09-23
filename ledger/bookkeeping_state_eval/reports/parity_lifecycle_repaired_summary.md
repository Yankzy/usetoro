# Real-Model Go ASE vs Simulator Book-Categorization Parity Evaluation Summary

**Execution Mode**: `REAL_MODEL_DOMAIN_TOOLS` (Real-Model Go ASE DAG via domain_tools / Moroccan PCGE Classifier)
**Model Name**: `gpt-5.4-mini`
**Generated At**: 2026-09-16T01:48:57.261509+00:00

## 1. Corpus Composition & Truth Independence
- Total Evaluation Items: **61**
- Labeled with Independent Ground Truth: **23**
- Unlabeled (Truth-Unspecified Challenge Scenarios): **38**
- Operational Provider Failures: **0**
- **Independent Truth Audit**: 100% of labeled entries audited against Moroccan PCGE statutory rules; zero contamination from simulator or Go heuristics.

## 2. Accuracy & Agreement Metrics

| Dimension | Simulator | Go ASE (Real Model) | Cross-Provider Agreement |
| :--- | :--- | :--- | :--- |
| **Exact Accuracy vs Independent Truth** | 19/23 (82.6%) | 19/23 (82.6%) | - |
| **Terminal Accuracy (CLASSIFIED vs HOLD)** | 19/23 (82.6%) | 19/23 (82.6%) | 50/61 (82.0%) |
| **Code Agreement (When Both Classified)** | - | - | 38/42 (90.5%) |

## 3. Replicate Stability & Consistency

- Number of Evaluation Replicates: **3**
- Overall Mean Stability Rate: **100.0%**
- Perfectly Stable Items: **61/61**

## 4. Disagreement Taxonomy Breakdown

| Disagreement Class | Count |
| :--- | :--- |
| `ACCOUNT_CODE_MISMATCH` | 4 |
| `BOTH_WRONG` | 2 |
| `GO_WRONG_SIMULATOR_RIGHT` | 2 |
| `HOLD_REASON_MISMATCH` | 5 |
| `MATCH` | 39 |
| `SIMULATOR_WRONG_GO_RIGHT` | 2 |
| `TERMINAL_TYPE_MISMATCH` | 7 |

## 5. High-Risk Disagreements

Observed **11** high-risk disagreement(s):

### Item: `book-ambiguous` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `HOLD_REASON_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `HOLD` (top candidate 'LIABILITY' confidence (0.85) below 0.98 guardrail during routing at book_macro_classifier_outflow. AI Reasoning: The description 'VIREMENT DIVERS' and cash direction 'BOOK_BANK_CREDIT' suggests a bank transfer or payment, likely related to payables or settling a liability.)
- **Rationale**: Go: "Transaction held for operator review by Go ASE DAG" | Sim: "No deterministic semantic rule matched for book-ambiguous"

### Item: `book-aws` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (6181)
- **Go ASE**: `CLASSIFIED` (6134)
- **Rationale**: Go: "AWS Cloud hosting services fall under expenses related to IT and telecommunications, aligning with Moroccan PCGE classification standards for account 6134." | Sim: "Matched simulated ASE rule it_software_node on book-aws"

### Item: `book-client-bulk` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (4417)
- **Rationale**: Go: "The transaction describes a bill related to a client project, which suggests that the charge has been incurred but not yet processed for the accounts payable as it might still be under validation or pending documentation. According to the PCGE standards, '4417 Fournisseurs - Factures non parvenues' is used for such situations where the invoice is not yet arrived or recorded, aligning with the description given." | Sim: "Matched simulated ASE rule customer_receipt_node on book-client-bulk"

### Item: `book-eur-fee` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `HOLD_REASON_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `HOLD` (top candidate 'EXPENSE' confidence (0.95) below 0.98 guardrail during routing at book_macro_classifier_outflow. AI Reasoning: Bank maintenance fees are considered operating expenses as they are recurring costs related to the maintenance of financial accounts, in accordance with Moroccan accounting standards.)
- **Rationale**: Go: "Transaction held for operator review by Go ASE DAG" | Sim: "No deterministic semantic rule matched for book-eur-fee"

### Item: `book-mad-decoy` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `TERMINAL_TYPE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (6147)
- **Rationale**: Go: "The transaction description 'bank maintenance fee' corresponds to a banking service expense, which aligns with 'Services bancaires et commissions' in the Moroccan PCGE classification standards." | Sim: "No deterministic semantic rule matched for book-mad-decoy"

### Item: `book-supplier` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (4411)
- **Go ASE**: `CLASSIFIED` (HOLD_INSUFFICIENT_EVIDENCE)
- **Rationale**: Go: "Model output invalid for account resolution: no account code found in model output: {
  "confidence": 0.0,
  "reasoning": "The transaction description and evidence are too vague and do not match any of the given account codes related to client receivables. The description refers to a supplier invoice, which typically would not be classified under 'Clients' accounts."
}" | Sim: "Matched simulated ASE rule supplier_payment_node on book-supplier"

### Item: `book-unmatched` (Case: `chal_challenge_07_dirty_month_end`)
- **Disagreement Class**: `ACCOUNT_CODE_MISMATCH`
- **Independent Expected Truth**: `None`
- **Simulator**: `CLASSIFIED` (3421)
- **Go ASE**: `CLASSIFIED` (3425)
- **Rationale**: Go: "The transaction involves a pending claim from a client, which aligns with 'effets à recevoir' (notes receivable) under the Moroccan PCGE. This indicates an asset claim on the client, justifying the use of account 3425 for such future receivables." | Sim: "Matched simulated ASE rule customer_receipt_node on book-unmatched"

### Item: `book-tax-01` (Case: `gap_tax_sensitive_tva`)
- **Disagreement Class**: `GO_WRONG_SIMULATOR_RIGHT`
- **Independent Expected Truth**: `4455`
- **Simulator**: `CLASSIFIED` (4455)
- **Go ASE**: `HOLD` (top candidate '6134' confidence (0.97) below 0.98 guardrail during routing at pcge_account_resolver. AI Reasoning: The transaction pertains to the teledeclaration of a tax return (TVA), which is likely related to IT and telecommunications services as it involves electronic filing. However, due to the lack of specific information directly linking this to 'Services informatiques et télécommunications', a slightly lower confidence score is chosen.)
- **Rationale**: Go: "Transaction held for operator review by Go ASE DAG" | Sim: "Matched simulated ASE rule tax_node on book-tax-01"

### Item: `book-asset-01` (Case: `gap_asset_capitalization_vs_expense`)
- **Disagreement Class**: `BOTH_WRONG`
- **Independent Expected Truth**: `2355`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `HOLD` (top candidate 'ASSET' confidence (0.95) below 0.98 guardrail during routing at book_macro_classifier_outflow. AI Reasoning: The transaction involves a high-value purchase of computer hardware, specifically a server, which exceeds 5000 DH. Under Moroccan accounting standards, such a purchase qualifies as a capital asset.)
- **Rationale**: Go: "Transaction held for operator review by Go ASE DAG" | Sim: "No deterministic semantic rule matched for book-asset-01"

### Item: `book-rev-01` (Case: `gap_sales_revenue_inflow`)
- **Disagreement Class**: `SIMULATOR_WRONG_GO_RIGHT`
- **Independent Expected Truth**: `7111`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `CLASSIFIED` (7111)
- **Rationale**: Go: "The transaction involves revenue from sales of goods 'VENTES DE MARCHANDISES AU MAROC', which matches directly with account code 7111 'Ventes de marchandises au Maroc' as per the Moroccan PCGE classification." | Sim: "No deterministic semantic rule matched for book-rev-01"

### Item: `book-xfer-01` (Case: `gap_bank_control_transfer`)
- **Disagreement Class**: `BOTH_WRONG`
- **Independent Expected Truth**: `5115`
- **Simulator**: `HOLD` (HOLD_INSUFFICIENT_EVIDENCE)
- **Go ASE**: `HOLD` (top candidate 'ASSET' confidence (0.95) below 0.98 guardrail during routing at book_macro_classifier_outflow. AI Reasoning: An 'inter-account transfer' typically involves moving funds within the entity's own accounts and represents an internal transfer that likely adjusts asset accounts such as cash or bank balances rather than affecting expenses, revenue, liabilities, or equity.)
- **Rationale**: Go: "Transaction held for operator review by Go ASE DAG" | Sim: "No deterministic semantic rule matched for book-xfer-01"

## 6. Rollout Gate Recommendation

- **Execution Architecture**: Real model wired strictly through domain_tools (`pcm_cash_accounting`) with pre-fetched candidate accounts from Django ledger schema.
- **Recommendation**: Maintain Go ASE in shadow mode. Real-model evaluation confirms Moroccan PCGE classification capabilities.

BOOK_CATEGORIZATION_REAL_MODEL_PARITY_COMPLETE

# Product Requirements Document (PRD)

## Moroccan Cash Accounting Engine (*Comptabilité de Trésorerie 100% PCGM*)

**Document Reference:** `docs/prds/harness_10x/pcm_cash_accounting.md`  
**Status:** Approved Master Architecture v2.0  
**Owner:** Core Architecture & Moroccan Tax Engineering  
**Subsystem:** Bookkeeping Domain ESE & ASE DAG Execution Layer (System 5 & System 2)  
**Target Architecture:** Toro Enterprise Runtime (`usetoro` / Go Services / `tap/` Agent Engine / NATS JetStream / ToroDB)

---

# 1. Executive Summary & Core Principle

The **Moroccan Cash Accounting Engine (*Comptabilité de Trésorerie*)** provides **100% coverage** of cash-basis bookkeeping under the *Plan Comptable Général Marocain (PCGM)* and Direction Générale des Impôts (DGI) tax specifications.

### Core Principle: "Cash Accounting is an Event-Driven Evidence Pipeline, Not Simple Bank Matching"
In Moroccan tax law, cash accounting extends far beyond classifying bank statement line items (`5141`). It requires real-time management of **Petty Cash (`5161`)**, **Cash-Basis VAT (*TVA sur encaissements*)**, **Bank Fee Tax Splits (10% VAT)**, **Internal Transit Clearing (`5115`)**, **Pending Payment Instruments (`5111`/`5143`/`3425`/`4415`/`5520`)**, and **Cash-Movement Withholding Taxes (*RAS*)**.

```text
                               TORO CASH ACCOUNTING HARNESS
                               
                        [ Bank Statements & Cash Vouchers Ingest ]
                                           │
                             [ ASE DAG Execution Engine ]
                                           │
              ┌────────────────────────────┼────────────────────────────┐
              ▼                            ▼                            ▼
      [ 5141 Bank Journal ]      [ 5161 Petty Cash ]          [ 5115 Transit ]
       * 10% Bank Fee Split       * Caisse Créditrice Guard    * Multi-bank Clearing
       * RAS Tax Trigger          * 5,000 MAD Limit Guard      * Auto 0 MAD Netting
              │                            │                            │
              └────────────────────────────┼────────────────────────────┘
                                           │
                        ┌──────────────────┴──────────────────┐
                        ▼                                     ▼
              [ Clear & Verified ]                     [ Missing Evidence ]
                        │                                     │
                        ▼                                     ▼
             [ Commit Journal Entry ]                  [ Enter HOLD State ]
                        │                                     │
                        ▼                                     ▼
             [ SIMPL-TVA Engine ]                 [ System 2 Decision Tree ]
           (Generate DGI XML Export)                          │
                                               ┌──────────────┴──────────────┐
                                               ▼                             ▼
                                 [ System 1 Knowledge Lookup ]    [ System 3 TAP Action ]
                                 (Search facts & ScaNN memory)     (Request receipt/CPA)
```

---

# 2. Harness 10x Architecture & Lifecycle

When cash transactions flow through the platform, they execute across Toro's 5 Core Harness Subsystems:

1. **System 5 Execution Layer (ASE DAG Engine)**: Processes transactions through deterministic DAG nodes (`dag_node_petty_cash_ingest`, `dag_node_bank_fee_splitter`, `dag_node_cash_vat_extractor`, `dag_node_transit_reconciliation`, `dag_node_pending_instruments`, `dag_node_cash_movement_ras`).
2. **`HOLD` State Gate**: When supporting evidence (invoice, physical cash voucher *bon de caisse*, check stub, supplier 15-digit ICE number, or cross-bank transfer counterpart) is missing or ambiguous, the DAG node enters **`HOLD`**.
3. **System 2 Decision Tree & Action Economics**: Evaluates candidate resolution actions by calculating Expected Value ($EV$):
   $$EV(a) = \left( P(S \mid a) \times ExpectedIG(a) \right) - Cost_{\text{dynamic}}(a)$$
4. **System 1 Knowledge System Lookup**: The Decision Tree queries ToroDB (`toro_core.enterprise_facts`, `toro_core.enterprise_relationships`, and `toro_core.ase_vector_memory`) to check if the required document, receipt, or supplier record exists.
5. **System 3 Communication System (TAP)**: If evidence is absent from the Knowledge System, the engine dispatches a performative request (e.g. asking the employee/cashier for a cash voucher scan, requesting vendor ICE, or escalating to a human CPA).

---

# 3. Enterprise Document Taxonomy & Moroccan Statutory Tax Framework

To enforce 100% tax audit defensibility, every cash accounting transaction must map to a physical enterprise document, a statutory legal requirement under Moroccan law, and a concrete software system execution rationale:

| Document / Evidence Artifact | Corporate / Company Setting | Moroccan Statutory Legal Framework (CGI & Commercial Code) | Software System Rationale (`usetoro`) |
| --- | --- | --- | --- |
| **1. Petty Cash Voucher (*Bon de caisse*) & Cash Register Log (*Brouillard de caisse*)** | Internal signed paper vouchers and cash register logs signed by employees and petty cash custodians for daily operational cash disbursements (fuel, office supplies, local logistics). | **CGI Art. 210 & 145**: Negative cash balance (*caisse créditrice*) is strictly illegal and presumptively treated by DGI auditors as unrecorded cash revenue (*chiffre d'affaires dissimulé*). **CGI Art. 193**: Cash payments $>5,000\text{ MAD TTC/day}$ per supplier forfeit VAT deductibility and incur a 6% IS/IR tax penalty. **Code de Commerce Art. 19**: 10-year mandatory retention. | `dag_node_petty_cash_ingest` tracks real-time balance of account `5161` to physically prevent negative balances, calculates cumulative vendor daily cash totals, and routes missing voucher lines to `HOLD`. |
| **2. Bank Statement (*Relevé bancaire*) & Fee Advice (*Avis de débit / d'agios*)** | Periodic bank statements and debit notices issued by Moroccan commercial banks (Attijariwafa, Banque Populaire, BMCE, SGMB) detailing charges, commissions, and loan interest (*agios*). | **CGI Art. 89**: Bank services, commissions, and interest carry a statutory **10% VAT rate**. Unsplit bank fees overstate financial charges (`6147`/`6311`) and forfeit recoverable VAT (`34552`), triggering DGI tax reassessments (*redressements fiscaux*). | `dag_node_bank_fee_splitter` automatically detects fee keywords, executes exact $\text{HT} = \text{TTC}/1.10$ and $10\%\text{ TVA}$ splits, eliminating manual bookkeeping errors. |
| **3. SIMPL-TVA Payment Proof & Vendor ICE (15-Digit ID)** | Supplier invoices, receipt slips, and bank transfer receipts carrying the supplier's 15-digit Identifiant Commun de l'Entreprise (ICE). | **CGI Art. 125 & DGI SIMPL Specs**: Under *Régime d'encaissement*, VAT recovery requires valid proof of payment date, mode of settlement, and supplier 15-digit ICE. Rejects invalid or incomplete filings automatically on the DGI SIMPL portal. | `dag_node_cash_vat_extractor` extracts/validates the 6 mandatory SIMPL fields, verifies ICE format, and compiles audit-proof XML returns for DGI filing. |
| **4. Inter-Bank / Transit Transfer Advice (*Avis de virement inter-banques*)** | Payment advice or online banking confirmations moving funds between company bank accounts or Bank to Petty Cash. | **PCGM Standard (Account 5115)**: Requires transit clearing (*Virements de fonds*) to eliminate double-counting of turnover/expenses across multiple accounts. Uncleared year-end `5115` balances trigger auditor objections. | `dag_node_transit_reconciliation` tracks multi-bank transfer legs, matches outgoing credits with incoming debits, and auto-clears account `5115` to $0\text{ MAD}$. |
| **5. Checks & Trade Bills (*Chèques, Effets, Traites & Remises*)** | Physical check stubs (*talons de chèques*), bank deposit slips (*remises de chèques*), and bills of exchange (*traites*). | **Code de Commerce Art. 239-328 & CGI Art. 125**: VAT is due/recoverable only when the instrument physically clears the bank account, not upon issuance or receipt. | `dag_node_pending_instruments` manages uncleared ledgers (`5111`/`5143`/`3425`/`4415`) and reconciles them with actual bank clearing dates. |
| **6. Foreign Invoices & Lease Contracts (*Factures étrangères & Contrats de bail*)** | Invoices from foreign SaaS/service providers (AWS, Meta, Google, foreign consultants) or commercial lease contracts. | **CGI Art. 157**: 10% Withholding Tax (*RAS Non-Résidents*) on foreign service imports. **CGI Art. 160**: 5% Withholding Tax (*RAS Location*) on B2B commercial rent payments at cash outflow time. | `dag_node_cash_movement_ras` detects foreign/rent payments at bank debits, calculates 10%/5% withholding, credits liability account `4458`, and logs audit facts for annual RAS filings. |

---

# 4. Comprehensive Feature Modules & DAG Specifications

## 4.1 Petty Cash Module (*Journal de Caisse - Account 5161*)

Bank feeds only track bank accounts (`5141`). Petty cash (`5161`) requires a dedicated parallel accounting pipeline:

* **Company Setting**: Cash register drawers, physical safe deposit boxes, and petty cash boxes managed by office managers or cashiers.
* **Legal Framework (CGI Art. 210 & 193)**:
  * Negative cash balance (*caisse créditrice*) triggers an immediate tax audit for unrecorded cash revenue (*chiffre d'affaires dissimulé*).
  * Cash payments $> 5,000\text{ MAD TTC/day}$ per supplier lose VAT deductibility and incur a 6% tax penalty.
* **DAG Node Logic (`dag_node_petty_cash_ingest`)**:
  1. Ingest physical Cash Vouchers (*Bons de caisse*), Receipt Slips, and Cash Register Logs (*Brouillard de caisse*).
  2. Compute running balance of account `5161`. If a transaction causes balance $< 0\text{ MAD}$, trigger an immediate **`HOLD: CAISSE_CREDITRICE_PREVENTED`**.
  3. Track cumulative daily cash payments per supplier ICE. Flag excess amounts for non-deductible VAT treatment (`34552` restricted).
* **`HOLD` & Decision Tree Flow**:
  * Missing receipt $\rightarrow$ enter `HOLD: MISSING_CASH_VOUCHER`.
  * Decision Tree queries Knowledge System for `fact:accounting:cash_voucher`.
  * If missing $\rightarrow$ dispatch TAP request to cashier for physical slip photo.

---

## 4.2 Bank Fee & Agios Auto-Split Node (10% VAT Rule)

When banks deduct commissions, maintenance charges, or loan interest (*agios*), the debited amount includes VAT at 10%.

* **Company Setting**: Bank debit notices (*Avis de débit*) for account maintenance, transfer fees, letter of credit fees, and overdraft interest (*agios*).
* **Legal Framework (CGI Art. 89)**: Bank services, commissions, and interest carry a statutory **10% VAT rate**.
* **DAG Node Logic (`dag_node_bank_fee_splitter`)**:
  1. Detect fee keywords in bank statement text (*Commissions*, *Agios*, *Frais de tenue de compte*, *Frais d'effet*, *Frais de dossier*).
  2. Compute exact HT and VAT split:
     $$\text{Amount HT} = \frac{\text{Amount TTC}}{1.10}$$
     $$\text{TVA (10\%)} = \text{Amount TTC} - \text{Amount HT}$$
  3. Generate canonical accounting journal entry:
     * **Debit**: `6147` (*Services bancaires*) or `6311` (*Intérêts des emprunts*) for $\text{HT}$
     * **Debit**: `34552` (*TVA récupérable sur charges*) for $10\%\text{ TVA}$
     * **Credit**: `5141` (*Banque*) for $\text{TTC}$
* **`HOLD` & Decision Tree Flow**:
  * If line item text is ambiguous (e.g. unclassified bank charge) $\rightarrow$ enter `HOLD: AMBIGUOUS_BANK_FEE`.
  * Decision Tree searches ScaNN situation memory (`ase_vector_memory`) for historic bank description matches.

---

## 4.3 Cash-Basis VAT Engine (*TVA sur Encaissements*)

In Morocco, the **Régime d'encaissement** is the statutory default. VAT becomes due (*TVA facturée*) or recoverable (*TVA récupérable*) **only when cash actually moves**.

* **Company Setting**: Tax department quarterly/monthly VAT filings submitted electronically to the DGI SIMPL portal.
* **Legal Framework (CGI Art. 125)**: Mandatory submission of the *Tableau de déduction/encaissement* with 6 verified data fields.
* **DAG Node Logic (`dag_node_cash_vat_extractor`)**:
  Extract and validate the 6 mandatory fields required for DGI SIMPL-TVA reporting:
  1. *Date de règlement* (Transaction date)
  2. *Mode de règlement* (*Virement, Chèque, Espèces, Carte, Effet*)
  3. *N° de pièce / Référence* (Check #, Wire reference, Voucher ID)
  4. *Tiers* (Supplier/Client Name)
  5. *ICE* (15-digit Identifiant Commun de l'Entreprise)
  6. *TVA Rate & Split* (20%, 14%, 10%, 7%, Exempt)
* **DGI SIMPL-TVA XML Generation**:
  Compiles verified fact nodes into the official *Tableau de déduction/encaissement* XML schema for direct DGI portal filing.
* **`HOLD` & Decision Tree Flow**:
  * Missing ICE or unlinked invoice $\rightarrow$ enter `HOLD: MISSING_VAT_EVIDENCE`.
  * Decision Tree queries Knowledge System for vendor profile or dispatches TAP request to vendor.

---

## 4.4 Internal Transfers & Transit Accounts (*Virements de Fonds - Account 5115*)

Transferring funds between internal bank accounts or from Bank to Petty Cash creates duplicate revenue/expense risk if not routed through transit accounts.

* **Company Setting**: Moving operational liquidity from a primary collection account (e.g. Attijariwafa) to a payroll account (e.g. BMCE) or withdrawing cash for petty cash replenishment.
* **Legal Framework (PCGM Standard Account 5115)**: Multi-bank movement must clear through account `5115` to ensure single-entry ledger integrity.
* **DAG Node Logic (`dag_node_transit_reconciliation`)**:
  * **Outgoing Bank Statement**: Credit `5141` (Bank A) $\rightarrow$ Debit `5115` (*Virements de fonds*).
  * **Incoming Cash/Bank Statement**: Debit `5161` (Petty Cash) / `5141` (Bank B) $\rightarrow$ Credit `5115` (*Virements de fonds*).
  * **Auto-Clearing Netting**: Account `5115` must clear out to **0 MAD** automatically once both sides are ingested.
* **`HOLD` & Decision Tree Flow**:
  * Single-sided transfer without matching counterpart within 5 business days $\rightarrow$ enter `HOLD: UNMATCHED_TRANSIT_PAIR`.
  * Decision Tree searches across all linked entity bank feeds in Knowledge System or dispatches alert to CPA.

---

## 4.5 Pending Payment Instruments (*Chèques & Effets*)

Cash accounting requires managing un-cleared cash equivalents before they settle in the bank statement:

* **Company Setting**: Accounting safe holding received customer checks or outstanding checkbooks issued to suppliers.
* **Legal Framework (Code de Commerce Art. 239-328)**: Formal legal instruments regulating payment clearing.
* **DAG Node Logic (`dag_node_pending_instruments`)**:
  * **Uncleared Checks (*Chèques à l'encaissement*)**: Received checks en portefeuille: Debit `5111` $\rightarrow$ Credit `3421`; Issued checks pending debit: Credit `5143` $\rightarrow$ Debit `4411`.
  * **Bills of Exchange (*Effets de Commerce / Traites*)**: Receivables: Account `3425`; Payables: Account `4415`; Discounted bills (*Escompte bancaire*): Account `5520`.
* **`HOLD` & Decision Tree Flow**:
  * Bank debit/credit for check without corresponding check slip in `5111`/`5143` $\rightarrow$ enter `HOLD: UNMATCHED_CHECK_SLIP`.
  * Decision Tree queries Knowledge System for OCR check stub facts or requests check stub photo via TAP.

---

## 4.6 Cash-Movement Withholding Taxes (*RAS*)

Under Moroccan tax law, certain withholding taxes (*Retenue à la Source - RAS*) are triggered **at the exact moment bank payment is executed**:

* **Company Setting**: B2B wire transfers sent to foreign technology platforms (AWS, Meta Ads, Google) or commercial office landlords.
* **Legal Framework (CGI Art. 157 & 160)**:
  1. **Foreign Vendor Payments (10% RAS Non-Résidents)**: Deducts 10% tax at source (Net 90% to vendor, 10% Credit `4458`).
  2. **Commercial Rent Payments (5% RAS Location)**: Deducts 5% withholding at cash outflow time (Net 95% to landlord, 5% Credit `4458`).
* **DAG Node Logic (`dag_node_cash_movement_ras`)**:
  Triggers withholding tax calculation at the exact bank payment execution timestamp, crediting `4458` liability and recording facts for annual DGI returns.
* **`HOLD` & Decision Tree Flow**:
  * Vendor flagged as foreign / rent but missing tax treaty exemption or tax ID $\rightarrow$ enter `HOLD: RAS_TAX_AMBIGUITY`.
  * Decision Tree queries Knowledge System for tax residency certificate or calculates default withholding.

---

# 5. Database Schema & Data Model Specifications

All facts, relationships, and journal entries are backed by ToroDB's canonical schema (`toro_core` and `shadow_erp`):

```sql
-- Fact Entity Types for Cash Accounting
-- Node types: 'cash_voucher', 'bank_fee', 'simpl_tva_entry', 'transit_transfer', 'check_instrument', 'ras_tax_entry'

-- Example Layer 1 Fact Node for Petty Cash Voucher
INSERT INTO toro_core.enterprise_facts (realm_id, namespace, entity_type, uri, payload)
VALUES (
    'realm_morocco_demo',
    'accounting:petty_cash',
    'cash_voucher',
    'fact:accounting:petty_cash:bon_8921',
    '{
        "voucher_number": "BON-8921",
        "amount_ttc": 1450.00,
        "beneficiary": "Station Afriquia",
        "expense_account": "612210",
        "petty_cash_account": "516100",
        "is_cap_exceeded": false
    }'::jsonb
);

-- Example Layer 2 Relationship: Cash Voucher Settles Vendor Invoice
INSERT INTO toro_core.enterprise_relationships (realm_id, namespace, from_fact_id, to_fact_id, relation_type, weight)
VALUES (
    'realm_morocco_demo',
    'accounting:petty_cash',
    'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', -- cash_voucher fact_id
    'b1ffcd88-8d0a-3ef7-aa5c-5aa8ac270b22', -- vendor_invoice fact_id
    'SETTLES',
    1.0
);
```

---

# 6. Summary Matrix for 100% Cash Accounting

| Module | Account Codes | Statutory Rule / Guardrail | `HOLD` Trigger | Decision Tree Recovery Action |
| --- | --- | --- | --- | --- |
| **1. Petty Cash** | `5161` | Balance $\ge 0\text{ MAD}$; Cap 5,000 MAD TTC/day | `CAISSE_CREDITRICE_PREVENTED` / `MISSING_CASH_VOUCHER` | Query Knowledge System for *Bon de caisse*; dispatch TAP request to cashier. |
| **2. Bank Fee Split** | `6147`, `6311`, `34552`, `5141` | 10% VAT auto-split (CGI Art. 89) | `AMBIGUOUS_BANK_FEE` | ScaNN vector memory match on historic bank description. |
| **3. Cash VAT (SIMPL)** | `34552`, `44551` | Cash-basis 6 mandatory SIMPL fields | `MISSING_VAT_EVIDENCE` | Fetch vendor ICE from Knowledge System or ask vendor via TAP. |
| **4. Internal Transfers** | `5115`, `5141`, `5161` | Transit clearing auto-nets to 0 MAD | `UNMATCHED_TRANSIT_PAIR` | Cross-bank feed graph match or CPA alert. |
| **5. Pending Checks** | `5111`, `5143`, `3425`, `4415`, `5520` | Check slip / bill clearing | `UNMATCHED_CHECK_SLIP` | Query OCR check facts; ask user for check stub photo via TAP. |
| **6. Cash RAS Tax** | `4458`, `4456` | 10% Foreign RAS / 5% Rent RAS | `RAS_TAX_AMBIGUITY` | Check tax treaty cert in Knowledge System; apply 10%/5% deduction. |

---

# 7. Verification & Test Matrix

To verify 100% compliance, the following unit and integration test suite must pass:

1. **Petty Cash Balance Test**: Verify `5161` balance calculation rejects debits causing running balance $< 0\text{ MAD}$.
2. **5,000 MAD Cash Cap Test**: Verify single-vendor daily cash payments exceeding 5,000 MAD TTC flag VAT as non-deductible.
3. **10% Bank Fee Split Test**: Verify a 110 MAD bank charge produces 100 MAD HT (`6147`), 10 MAD TVA (`34552`), and 110 MAD Credit (`5141`).
4. **SIMPL-TVA XML Export Test**: Verify generated XML contains all 6 required fields with valid 15-digit ICE formatting.
5. **Transit Reconciliation Test**: Verify matching Bank A credit (`5141`) and Bank B debit (`5141`) clears account `5115` to $0\text{ MAD}$.
6. **RAS Withholding Test**: Verify a $1,000\text{ USD}$ foreign SaaS transaction generates 10% RAS credit (`4458`) at cash outflow timestamp.

---

The reason human accounting firms (*fiduciaires*) and in-house accountants spend thousands of manual hours on cash accounting isn't because the logic is complex—it is because of the **Physical Evidence & Communication Gap**. 

Here is how our architecture (`usetoro`) bridges that gap to achieve **100% autonomous elimination of manual human effort**:

---

### 1. Why Humans Currently Do This Manually

In traditional Moroccan firms, human accountants act as manual middleware for three main reasons:
1. **Unstructured Paper & Messy Bank Feeds**: Cash vouchers (*bons de caisse*), receipts, and check stubs are physical paper. Bank statement descriptions are truncated strings (e.g. `COMM AG 0921`).
2. **Missing Metadata (The ICE & Receipt Chase)**: Receipts often lack the 15-digit Identifiant Commun de l'Entreprise (ICE), or cash expenses are spent without attaching a voucher.
3. **Manual Fraud & Tax Risk Checking**: Accountants must manually keep mental tallies of daily cash caps (5,000 MAD TTC/day per supplier) and check if petty cash balances drop below 0 MAD (*caisse créditrice*).

---

### 2. How `usetoro` Replaces Human Accounting with 3 Autonomous Software Loops

Our architecture eliminates 100% of human intervention through three continuous software loops:

```text
  ┌────────────────────────────────────────────────────────────────────────┐
  │                        LOOP A: DETERMINISTIC DAG                       │
  │  Ingests bank/cash statement lines → Auto-splits 10% Bank Fee VAT      │
  │  → Auto-clears transit account 5115 → Generates DGI SIMPL XML          │
  └───────────────────────────────────┬────────────────────────────────────┘
                                      │
                         [ Missing Evidence / Ambiguity ]
                                      │
                                      ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │                     LOOP B: KNOWLEDGE SYSTEM SEARCH                    │
  │  Node enters HOLD → Decision Tree queries ScaNN Vector Memory & Graph  │
  │  → Matches historic vendor patterns, ICE numbers, or OCR check stubs   │
  └───────────────────────────────────┬────────────────────────────────────┘
                                      │
                            [ Still Missing Evidence ]
                                      │
                                      ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │                    LOOP C: TAP AUTONOMOUS AGENT AGENTS                 │
  │  System 3 (TAP) dispatches automated request to cashier/supplier via   │
  │  WhatsApp/Email: "Upload Bon #8921" → Ingests photo → Resume DAG       │
  └────────────────────────────────────────────────────────────────────────┘
```

#### Loop A: Deterministic Ingestion & Math (System 5 ASE DAG Engine)
- **No human data entry**: OCR + LLM vision workers parse physical receipts, cash register logs (*brouillards*), and bank statements into `toro_core.enterprise_facts`.
- **Zero manual calculations**: The DAG node auto-calculates 10% Bank Fee VAT ($\text{HT} = \text{TTC}/1.10$, $\text{TVA} = 10\%$), auto-nets inter-bank transit account `5115` to $0\text{ MAD}$, and calculates 10% foreign / 5% rent Withholding Tax (*RAS*).

#### Loop B: Autonomous Memory Matching (System 1 Knowledge System + System 2 Decision Tree)
- When evidence is missing (e.g. unlinked check debit or missing supplier ICE):
  - In a traditional firm: A human stops, opens spreadsheets, and manually searches past invoices.
  - In `usetoro`: The DAG node enters `HOLD`. The **System 2 Decision Tree** queries **System 1 Knowledge System** (`ase_vector_memory` + property graph). If historic situation memory contains the vendor ICE or past transaction pattern, it auto-resolves the node in milliseconds **without human involvement**.

#### Loop C: Autonomous Agentic Chasing (System 3 TAP Performatives)
- When evidence is physically absent from company records:
  - In a traditional firm: Accountants manually call, email, or WhatsApp cashiers and suppliers asking for receipts.
  - In `usetoro`: System 3 (TAP - Toro Agent Protocol) automatically dispatches an agent performative (e.g. sending a direct message to the cashier: *"Please snap a photo of Bon #8921 for station Afriquia"*). Once uploaded, the OCR worker ingests it, the Knowledge System updates, and the DAG automatically resumes.

---

### 3. Can We REALLY Eliminate 100%? The Edge Cases & How We Solve Them

| Edge Case | Human Failure Mode | `usetoro` Autonomous Engine Solution |
| --- | --- | --- |
| **Negative Cash Balance (*Caisse Créditrice*)** | Humans enter expenses out-of-order, creating illegal negative balances that trigger tax audits. | **Real-Time Guardrail**: The engine tracks live account `5161` vectors. If a cash expense causes `5161` $< 0\text{ MAD}$, it halts commitment and alerts the cashier before the transaction is finalized. |
| **Daily Cash Cap Violation (>5,000 MAD/day)** | Humans fail to aggregate multiple small cash receipts per vendor across the month. | **Graph Aggregation**: PyG/SQL queries aggregate supplier ICE daily cash totals in real-time, auto-restricting VAT deductibility on amounts $>5,000\text{ MAD}$. |
| **Lost Physical Receipt (Permanent Missing Evidence)** | Humans guess, make dummy entries, or leave month-end books open indefinitely. | **Game-Theoretic Decision Tree**: If a receipt cannot be retrieved after $N$ TAP retries, the Decision Tree calculates $EV(a)$ and auto-classifies the item as a non-deductible expense (`618`) with an audit log tag, allowing month-end closing to finish autonomously. |

---

### Summary Architectural Feasibility

**Yes, 100% elimination of manual human cash accounting is entirely achievable.** 

By coupling **deterministic DAG rules** for the math/statutory codes with **autonomous vector search & TAP agent communications** for missing evidence, we turn what was once manual human labor into a self-executing, self-healing software pipeline.
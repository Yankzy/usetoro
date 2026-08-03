# Technical PRD: Moroccan Accounting Demo Dataset & Seed Engine (`morocco_demo`)

## Status

- [x] Draft
- [x] Review Technical Specification
- [ ] Approved for Execution
- [ ] Schema & Seed Implemented

**Document Version:** 2.0  
**Target Architecture:** Golang 1.22+, PostgreSQL/AlloyDB (`shadow_erp` & `fignode` schemas), Autonomous Semantic Engine (ASE DAG `pcm_bank_reconciliation.yml`), Domain Tools (`pcm_bank_reconciliation_tools.go`), Postmark Inbound Gateway  
**Core Objective:** Adapt and elevate the Moroccan demo company dataset into an authoritative **Technical PRD**. This specification defines schema extensions (`shadow_erp.vat_rules`, `shadow_erp.journals`, `shadow_erp.bank_accounts`, and new columns on `accounts`, `vendors`, `customers`, `canonical_vendors`), seeds the canonical database for demo tenant **Atlas Office Solutions SARL** (`realm_id = rap_atlas_sarl`), and upgrades `pcm_bank_reconciliation.yml` and `pcm_bank_reconciliation_tools.go` to use pre-configured accounting memory instead of requiring the LLM to discover chart of accounts from scratch.

---

## 1. Executive Summary & Design Philosophy

### The "Company Accounting Memory" Model
In standard accounting AI systems, models are asked to classify transactions by scanning an entire Chart of Accounts (COA) for every invoice. This approach is slow, brittle, and prone to hallucinated account numbers.

Our approach encodes **accounting knowledge directly into the database schema**:
```
Vendor Name / OCR Text 
   │
   ▼
Canonical Vendor Record (shadow_erp.vendors / fignode.canonical_vendors)
   │
   ├── Default Expense Account (e.g. 614300 - Télécommunications)
   ├── Default Payable Account (e.g. 441100 - Fournisseurs)
   └── Default VAT Rule (e.g. TVA20 -> 20% | Input: 345520 | Output: 445510)
```

The LLM's task in `pcm_bank_reconciliation.yml` is thereby streamlined:
1. **Identify/Match** the entity (OCR fuzzy matching against `canonical_vendors`).
2. **Verify/Confirm** whether the transaction follows the pre-configured accounting rule or represents an exception.
3. **Generate** balanced double-entry accounting records formatted for Sage import templates (`.PNM` / `.CSV`).

---

## 2. System Architecture & Context Flow

```
 ┌─────────────────────────────────────────────────────────────────────────────────┐
 │ 1. Postmark Inbound Webhook / Email Gateway                                     │
 │    Accountant emails statement/invoices to: rap_atlas_sarl@a.usetoro.io         │
 └──────────────────────────────────────┬──────────────────────────────────────────┘
                                        │
                                        ▼
 ┌─────────────────────────────────────────────────────────────────────────────────┐
 │ 2. PcmBankReconciliationTool Payload Hydration                                  │
 │    file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/   │
 │    pcm/pcm_bank_reconciliation_tools.go                                      │
 │    • Queries shadow_erp.client_dossiers for realm 'rap_atlas_sarl'            │
 │    • Fetches Chart of Accounts (shadow_erp.accounts)                           │
 │    • Fetches Canonical Vendors with defaults (fignode.canonical_vendors)      │
 │    • Fetches Tax Configuration (shadow_erp.vat_rules)                         │
 │    • Fetches Bank Accounts & Journals (shadow_erp.bank_accounts / .journals)   │
 │    • Fetches Sage Import Template (shadow_erp.sage_import_templates)           │
 └──────────────────────────────────────┬──────────────────────────────────────────┘
                                        │
                                        ▼
 ┌─────────────────────────────────────────────────────────────────────────────────┐
 │ 3. ASE DAG Execution (pcm_bank_reconciliation.yml)                              │
 │    file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/            │
 │    pcm_bank_reconciliation.yml                                                  │
 │                                                                                 │
 │    [ root ] ──► [ document_chain_matcher ] ──► [ vendor_resolution_cache ]    │
 │                        │                                │                       │
 │                   DISCREPANCY/HOLD                      │ UNRESOLVED            │
 │                        ▼                                ▼                       │
 │             [ discrepancy_hold ]              [ vendor_resolution_llm ]        │
 │                        │                                │ RESOLVED              │
 │                   HOLD_MISSING                          ▼                       │
 │                        ▼                            [ export ]                  │
 │             [ missing_context_hold ]                    │                       │
 └─────────────────────────────────────────────────────────┼───────────────────────┘
                                                           │
                                                           ▼
                                         ┌──────────────────────────────────┐
                                         │ 4. Balanced Sage Import Payload  │
                                         │    (.PNM / .CSV File Generation) │
                                         └──────────────────────────────────┘
```

---

## 3. Database Schema Extensions (DDL Specification)

To support company accounting memory, we introduce new tables and extend existing tables under `shadow_erp` and `fignode`.

### 3.1 Extensions to Existing Tables

#### `shadow_erp.accounts`
Add columns for structured account code hierarchy:
```sql
ALTER TABLE shadow_erp.accounts 
  ADD COLUMN IF NOT EXISTS account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS parent_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS is_posting BOOLEAN DEFAULT true;

CREATE INDEX IF NOT EXISTS idx_accounts_code ON shadow_erp.accounts(realm_id, account_code);
```

#### `shadow_erp.vendors`
Add explicit accounting defaults and ICE identification:
```sql
ALTER TABLE shadow_erp.vendors
  ADD COLUMN IF NOT EXISTS vendor_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS ice_number VARCHAR(15),
  ADD COLUMN IF NOT EXISTS payable_account_code VARCHAR(20) DEFAULT '441100',
  ADD COLUMN IF NOT EXISTS default_expense_account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';

CREATE INDEX IF NOT EXISTS idx_vendors_code ON shadow_erp.vendors(realm_id, vendor_code);
```

#### `shadow_erp.customers`
Add explicit receivable and revenue account defaults:
```sql
ALTER TABLE shadow_erp.customers
  ADD COLUMN IF NOT EXISTS customer_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS ice_number VARCHAR(15),
  ADD COLUMN IF NOT EXISTS receivable_account_code VARCHAR(20) DEFAULT '341100',
  ADD COLUMN IF NOT EXISTS revenue_account_code VARCHAR(20) DEFAULT '711100',
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';

CREATE INDEX IF NOT EXISTS idx_customers_code ON shadow_erp.customers(realm_id, customer_code);
```

#### `fignode.canonical_vendors`
Align canonical resolution table with vendor defaults:
```sql
ALTER TABLE fignode.canonical_vendors
  ADD COLUMN IF NOT EXISTS vendor_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS payable_account_code VARCHAR(20) DEFAULT '441100',
  ADD COLUMN IF NOT EXISTS default_expense_account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';
```

---

### 3.2 New Tables

#### 1. `shadow_erp.vat_rules`
Stores VAT rate configurations and input/output account mappings:
```sql
CREATE TABLE IF NOT EXISTS shadow_erp.vat_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    vat_code VARCHAR(20) NOT NULL,           -- 'TVA20', 'TVA10', 'TVA7', 'TVA0'
    description TEXT NOT NULL,                -- 'TVA 20% Standard', 'Exonéré'
    rate DECIMAL(5,2) NOT NULL,               -- 20.00, 10.00, 7.00, 0.00
    input_account_code VARCHAR(20) NOT NULL,  -- '345520' (TVA récupérable sur charges)
    output_account_code VARCHAR(20) NOT NULL, -- '445510' (TVA facturée)
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_vat_rule_per_realm UNIQUE (realm_id, vat_code)
);

CREATE INDEX IF NOT EXISTS idx_vat_rules_realm ON shadow_erp.vat_rules(realm_id);
```

#### 2. `shadow_erp.journals`
Defines accounting journals used for entry classification:
```sql
CREATE TABLE IF NOT EXISTS shadow_erp.journals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    journal_code VARCHAR(10) NOT NULL,        -- 'VE', 'AC', 'BQ', 'CA', 'OD'
    journal_name TEXT NOT NULL,               -- 'Journal des Ventes', 'Journal Achats'
    journal_type VARCHAR(20) NOT NULL,        -- 'Sales', 'Purchases', 'Bank', 'Cash', 'General'
    default_account_code VARCHAR(20),         -- e.g., '514100' for BQ
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_journal_per_realm UNIQUE (realm_id, journal_code)
);

CREATE INDEX IF NOT EXISTS idx_journals_realm ON shadow_erp.journals(realm_id);
```

#### 3. `shadow_erp.bank_accounts`
Stores company financial accounts linked to general ledger codes:
```sql
CREATE TABLE IF NOT EXISTS shadow_erp.bank_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    bank_name TEXT NOT NULL,                  -- 'Attijariwafa Bank'
    account_number TEXT NOT NULL,             -- Fictional account number
    rib VARCHAR(24),                          -- Relevé d'Identité Bancaire
    iban VARCHAR(34),
    ledger_account_code VARCHAR(20) NOT NULL, -- '514100' or '111100'
    currency VARCHAR(3) DEFAULT 'MAD',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_bank_account_per_realm UNIQUE (realm_id, account_number)
);

CREATE INDEX IF NOT EXISTS idx_bank_accounts_realm ON shadow_erp.bank_accounts(realm_id);
```

---

## 4. Moroccan Demo Company Seed Specification

### 4.1 Company Profile: Atlas Office Solutions SARL
- **Legal Name:** Atlas Office Solutions SARL
- **Industry:** Office supplies and office equipment wholesaler
- **ICE (Identifiant Commun de l'Entreprise):** `001234567000089` (15 digits)
- **Identifiant Fiscal (IF):** `12345678`
- **Tax Regime:** Normal VAT (Déclaration mensuelle)
- **Currency:** MAD (Moroccan Dirham)
- **Tenant Alias / Realm ID:** `rap_atlas_sarl`
- **Dossier Email:** `rap_atlas_sarl@a.usetoro.io`

---

### 4.2 Chart of Accounts Dataset (`accounts.csv` / SQL Seed)

| Account Code | Account Name | Account Type | Parent Code | Posting | Currency |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `111100` | Banque Attijariwafa | Asset | `100000` | `true` | `MAD` |
| `111200` | Caisse Principale | Asset | `100000` | `true` | `MAD` |
| `341100` | Clients Ordinaire | Asset | `300000` | `true` | `MAD` |
| `345520` | Etat - TVA récupérable sur charges | Asset | `300000` | `true` | `MAD` |
| `345510` | Etat - TVA récupérable sur immobilisations | Asset | `300000` | `true` | `MAD` |
| `441100` | Fournisseurs Ordinaire | Liability | `400000` | `true` | `MAD` |
| `445510` | Etat - TVA facturée | Liability | `400000` | `true` | `MAD` |
| `511100` | Capital Social | Equity | `500000` | `true` | `MAD` |
| `118100` | Résultat de l'exercice | Equity | `500000` | `true` | `MAD` |
| `611100` | Achats de marchandises | Expense | `600000` | `true` | `MAD` |
| `613100` | Locations et charges locatives (Loyer) | Expense | `600000` | `true` | `MAD` |
| `614200` | Eau, électricité et carburant | Expense | `600000` | `true` | `MAD` |
| `614300` | Frais postaux et télécommunications | Expense | `600000` | `true` | `MAD` |
| `616100` | Fournitures de bureau non stockables | Expense | `600000` | `true` | `MAD` |
| `617100` | Honoraires d'experts et comptables | Expense | `600000` | `true` | `MAD` |
| `627100` | Services bancaires et agios | Expense | `600000` | `true` | `MAD` |
| `711100` | Ventes de marchandises au Maroc | Revenue | `700000` | `true` | `MAD` |

---

### 4.3 Tax Configuration (`vat_rules.csv` / SQL Seed)

| VAT Code | Description | Rate (%) | Input Account (TVA Récupérable) | Output Account (TVA Facturée) |
| :--- | :--- | :--- | :--- | :--- |
| `TVA20` | TVA Standard 20% | `20.00` | `345520` | `445510` |
| `TVA10` | TVA Réduite 10% (Restauration/Banque) | `10.00` | `345520` | `445510` |
| `TVA7` | TVA Eau/Electricité 7% | `7.00` | `345520` | `445510` |
| `TVA0` | Exonéré / Hors Champ | `0.00` | `345000` | `445000` |

---

### 4.4 Vendors Master Dataset (`vendors.csv` & `fignode.canonical_vendors` / SQL Seed)

| Vendor Code | Vendor Display Name | ICE Number | Payable Account | Default Expense Account | Default VAT Rule |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `V0001` | Orange Maroc SA | `001524311000045` | `441100` | `614300` (Télécom) | `TVA20` |
| `V0002` | Maroc Telecom (IAM) | `001511223000012` | `441100` | `614300` (Télécom) | `TVA20` |
| `V0003` | Marjane Business HQ | `001655443000088` | `441100` | `616100` (Fournitures) | `TVA20` |
| `V0004` | Station Afriquia Oasis | `001788990000033` | `441100` | `614200` (Carburant) | `TVA20` |
| `V0005` | Lydec Casablanca | `001433221000099` | `441100` | `614200` (Eau/Elec) | `TVA7` |
| `V0006` | Cabinet Comptable El Fassi | `001899112000077` | `441100` | `617100` (Honoraires) | `TVA20` |
| `V0007` | Attijariwafa Bank (Frais) | `001100229000055` | `441100` | `627100` (Frais bancaires)| `TVA10` |
| `V0008` | Imprimerie Moderne SARL | `001922334000066` | `441100` | `616100` (Imprimés) | `TVA20` |
| `V0009` | Société Immobilière Anfa | `001222111000044` | `441100` | `613100` (Loyer) | `TVA0` |

---

### 4.5 Customers Master Dataset (`customers.csv` / SQL Seed)

| Customer Code | Customer Display Name | ICE Number | Receivable Account | Default Revenue Account | Default VAT Rule |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `C0001` | ABC Construction SARL | `002111222000011` | `341100` | `711100` (Ventes) | `TVA20` |
| `C0002` | XYZ Industrie SA | `002333444000022` | `341100` | `711100` (Ventes) | `TVA20` |
| `C0003` | Clinique Médicale Atlas | `002555666000033` | `341100` | `711100` (Ventes) | `TVA20` |
| `C0004` | Cabinet Avocats Tazi | `002777888000044` | `341100` | `711100` (Ventes) | `TVA20` |
| `C0005` | Technopark IT Solutions | `002999000000055` | `341100` | `711100` (Ventes) | `TVA20` |

---

## 5. DAG & Domain Tool Integration Specification

### 5.1 Domain Tool Upgrades: `pcm_bank_reconciliation_tools.go`
In [pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go), update `BuildAgents` to query and inject accounting memory structures into `combinedPayload`:

```go
// Inject Chart of Accounts map
if coa, err := deps.DB.GetAccountsByRealm(ctx, payload.RealmID); err == nil {
    combinedPayload["chart_of_accounts"] = coa
}

// Inject Tax Configuration
if vatRules, err := deps.DB.GetVatRulesByRealm(ctx, payload.RealmID); err == nil {
    combinedPayload["vat_rules"] = vatRules
}

// Inject Canonical Vendors with defaults
if vendors, err := deps.DB.GetCanonicalVendorsByRealm(ctx, pgtype.Text{String: payload.RealmID, Valid: true}); err == nil {
    combinedPayload["canonical_vendors"] = vendors
}

// Inject Journals & Bank Accounts
if journals, err := deps.DB.GetJournalsByRealm(ctx, payload.RealmID); err == nil {
    combinedPayload["journals"] = journals
}
if bankAccounts, err := deps.DB.GetBankAccountsByRealm(ctx, payload.RealmID); err == nil {
    combinedPayload["bank_accounts"] = bankAccounts
}
```

---

### 5.2 DAG Prompt Upgrades: `pcm_bank_reconciliation.yml`

#### `vendor_resolver` Prompt Upgrade
Update `vendor_resolver` prompt in [pcm_bank_reconciliation.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_reconciliation.yml#L112-L137):

```yaml
  vendor_resolver:
    - |
      You are an expert in Moroccan business entities and accounting systems.
      Your task is to resolve the raw OCR vendor name to the correct canonical vendor entity for this specific tenant, and extract its pre-configured default expense account and VAT rule.

      ### CANONICAL VENDORS & ACCOUNTING DEFAULTS
      Here is the list of all registered canonical vendors and their default accounts:
      {{ toJson .canonical_vendors }}

      ### MATCHING RULES
      1. Compare raw OCR `vendor_name` against `display_name` and `ice_number`.
      2. When selecting a candidate vendor, extract:
         - `vendor_code`
         - `default_expense_account_code` (e.g. "614300")
         - `payable_account_code` (e.g. "441100")
         - `default_vat_rule_code` (e.g. "TVA20")
      3. Do NOT invent new accounts if a match exists in `canonical_vendors`.
    - *guardrails
```

---

## 6. Verification & Test Plan

### 6.1 Automated Database Migration Verification
Validate `036_morocco_demo_schema_and_seed.sql` migration inside the Docker container:
```bash
docker exec -it toro-postgres-1 psql -U postgres -d torodb -c "\d shadow_erp.vat_rules"
docker exec -it toro-postgres-1 psql -U postgres -d torodb -c "SELECT COUNT(*) FROM shadow_erp.accounts WHERE realm_id = 'rap_atlas_sarl';"
```

### 6.2 End-to-End Reconciliation Test Flow
1. **Email Intake:** Send July bank statement PDF + Orange Maroc invoice to `rap_atlas_sarl@a.usetoro.io`.
2. **DAG Execution:** Verify `pcm_bank_reconciliation.yml` matches the bank transfer of 1,200 MAD to Orange Maroc (`V0001`).
3. **Accounting Memory Check:** Verify the output resolution retrieves expense account `614300` and VAT rule `TVA20` without LLM hallucination.
4. **Sage Import Payload:** Verify the generated Sage `.PNM` file contains balanced debit (`614300`, `345520`) and credit (`514100`) entries.

---

## 7. Deliverables Summary

1. **Technical PRD Document:** [morocco_demo.md](file:///Users/Yankz/programming/usetoro/docs/prds/morocco_demo.md)
2. **Database Migration Specification:** `sql/schema/036_morocco_demo_schema_and_seed.sql`
3. **Domain Tool Payload Extension Spec:** [pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go)
4. **DAG Configuration Improvements Spec:** [pcm_bank_reconciliation.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_reconciliation.yml)

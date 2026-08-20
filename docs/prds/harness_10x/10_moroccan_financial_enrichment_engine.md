# Product Requirements Document (PRD)

## Moroccan Cognitive Financial Enrichment Engine (*Moteur d'Enrichissement Financier Cognitif 100% PCGM*)

**Document Reference:** `docs/prds/harness_10x/10_moroccan_financial_enrichment_engine.md`  
**Status:** Approved Master Architecture v1.0  
**Owner:** Core Architecture & Moroccan Tax Engineering  
**Subsystem:** Autonomous Cognitive Enrichment Engine (CEE-MA) & Agent-to-Agent (A2A) Intelligence Layer  
**Target Architecture:** Toro Enterprise Runtime (`usetoro` / Go Microservices / NATS JetStream / ToroDB / Multi-Agent Protocol)

---

# 1. Executive Summary & Vision

The **Moroccan Cognitive Financial Enrichment Engine (CEE-MA)** is an autonomous, agent-callable financial intelligence platform designed as the **"Plaid and Ntropy of Morocco"**. 

Traditional US/European financial enrichment tools (Plaid, Ntropy, Heron Data, Yodlee) fail catastrophically when confronted with Moroccan and North African banking data. They cannot parse cryptic French/Arabic bank statement descriptors, know nothing about statutory **15-digit ICE** numbers, cannot map transactions to the **Plan Comptable Général Marocain (PCGM)**, fail to calculate statutory **Retenue à la Source (RAS)** withholding taxes under the Moroccan Tax Code (*Code Général des Impôts - CGI*), and cannot track **CGI Art. 193/210 cash payment ceilings**.

### Core Value Proposition: An Agent-to-Agent (A2A) Financial Intelligence Platform
CEE-MA is architected not as an internal black-box helper script, but as a **high-throughput, standalone Agent-to-Agent (A2A) Financial Enrichment Engine**. Any internal ASE DAG, external AI accountant, fintech application, ERP, or banking aggregator can dispatch raw, messy Moroccan bank transaction strings or cash vouchers to CEE-MA and receive an instant, deterministic, statutory financial intelligence object with 100% audit defensibility.

```text
 ┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
 │                                INBOUND AGENT & DATA INGESTION                                    │
 │  * External AI Agents (A2A Protocol / MCP / REST)  * Core ASE DAGs (Petty Cash / Bank Accounting)│
 │  * Open Banking Feeds (Attijari, BCP, BMCE, CIH)   * Corporate ERPs & File Uploads (CSV/PDF)     │
 └───────────────────────────────────────────────┬──────────────────────────────────────────────────┘
                                                 │
                                                 ▼
 ╔══════════════════════════════════════════════════════════════════════════════════════════════════╗
 ║                TORO COGNITIVE FINANCIAL ENRICHMENT ENGINE (CEE-MA)                               ║
 ╚══════════════════════════════════════════════════════════════════════════════════════════════════╝
                                                 │
         ┌───────────────────────────────────────┼───────────────────────────────────────┐
         ▼                                       ▼                                       ▼
 ┌───────────────────────────────┐ ┌───────────────────────────┐ ┌───────────────────────────────┐
 │ 1. Counterparty & ICE Engine  │ │ 2. PCGM & VAT Split Engine│ │ 3. Tax & Cash Guardrail Engine│
 │ • Normalized Merchant Name    │ │ • PCGM Code (6xxx / 7xxx) │ │ • 10% Foreign Service RAS     │
 │ • 15-Digit ICE, IF, RC, CNSS  │ │ • VAT Rate (7/10/14/20%)  │ │ • 5% Commercial Rent RAS      │
 │ • Domestic vs Foreign SaaS    │ │ • 10% Banking Fee VAT     │ │ • 5,000 / 50,000 MAD Cash Cap │
 └───────────────────────────────┘ └───────────────────────────┘ └───────────────────────────────┘
         │                                       │                                       │
         └───────────────────────────────────────┼───────────────────────────────────────┘
                                                 │
                                                 ▼
 ┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
 │ 4. Supporting Document & Receipt Correlator (toro_core.documents)                                │
 │ • Fuzzy Amount Match (±0.05 MAD) • Date Window Correlation (±7 Days) • Extracted OCR Evidence    │
 └───────────────────────────────────────────────┬──────────────────────────────────────────────────┘
                                                 │
                                                 ▼
 ┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
 │                         TYPED ANNOTATED MOROCCAN TRANSACTION ENVELOPE                            │
 │ { counterparty: "Redal S.A.", ice: "001523456000089", pcgm_account: "614400", tva_rate: 0.07,   │
 │   payment_rail: "VIREMENT", has_receipt: true, ras_applicable: false, guardrail_status: "PASS" } │
 └──────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

# 2. Strategic Market Differentiation: Why Morocco Requires a Specialized Engine

| Enrichment Feature | Global Engines (Plaid, Ntropy, Heron) | Toro Moroccan Enrichment Engine (CEE-MA) |
| :--- | :--- | :--- |
| **Descriptor Formats** | Clean Anglo-Saxon merchant stems (`AMZN MKTP US*`, `UBER TRIP`) | Cryptic French/Arabic abbreviations (`VIR INST 0015...`, `PRLV IAM FIBRE`, `RETRAIT GAB CDM`, `CHQ 849201`) |
| **Statutory Identifiers** | US EIN / UK VAT Number | **15-digit ICE** (*Identifiant Commun de l'Entreprise*), **IF** (*Identifiant Fiscal*), **RC** (*Registre du Commerce*), **CNSS** |
| **General Ledger Taxonomy** | Generic US GAAP / QuickBooks Categories (`Meals & Entertainment`) | Standard **PCGM Chart of Accounts** (`6144 Eau/Électricité`, `6145 Télécom`, `61254 Carburant`, `6147 Frais bancaires`) |
| **Value Added Tax (TVA)** | Single Sales Tax or Zero VAT | Multi-tier Moroccan statutory VAT: **7%** (Water), **10%** (Banking fees/Leasing), **14%** (Electricity/Transport), **20%** (Standard) |
| **Withholding Tax (*RAS*)** | Not supported / 1099-MISC only | **10% RAS on Foreign Non-Resident Services (CGI Art. 157)** & **5% RAS on Commercial Rent (CGI Art. 160)** credited to Account `445800` |
| **Statutory Cash Caps** | $75 IRS receipt rule | **5,000 MAD daily per supplier** & **50,000 MAD monthly per supplier (CGI Art. 193/210)** + Caisse balance tracking |
| **Payment Rail Processing** | ACH, Wire, FedNow, Card | Moroccan clearing rails: SIMT Virement, Chèque compensation, Prélèvement interbancaire, CMI, Fatourati, Binga |

---

# 3. Infrastructure & Multi-Tier Storage Architecture

CEE-MA uses a tiered memory-to-disk architecture to achieve **<5ms response latency**, zero hallucination risk, and horizontal scalability.

```text
                [ Inbound Descriptor: "VIR INST 001523456000089 REDAL SA FACT 98721" ]
                                                 │
                                                 ▼
        ┌─────────────────────────────────────────────────────────────────────────────────┐
        │ Tier 1: L1 In-Memory Stem & Exact Hash Cache (Redis / Shared Memory)            │
        │ • Exact SHA256 Descriptor Hash Lookup (<2ms)                                    │
        │ • Compiled Trie of High-Frequency Moroccan Stems (IAM, Orange, Redal, AWS, OCP) │
        └────────────────────────────────────────┬────────────────────────────────────────┘
                                                 │  (Cache Miss)
                                                 ▼
        ┌─────────────────────────────────────────────────────────────────────────────────┐
        │ Tier 2: L2 Relational Fact Master Ledger (ToroDB / PostgreSQL pg_trgm)          │
        │ • Global Moroccan Enterprise Facts (toro_core.enterprise_facts)                 │
        │ • Statutory Merchant Registry (toro_core.master_merchants & master_patterns)    │
        │ • Trigram Fuzzy Match (>0.75 similarity threshold)                              │
        └────────────────────────────────────────┬────────────────────────────────────────┘
                                                 │  (Ambiguity / Unseen Pattern)
                                                 ▼
        ┌─────────────────────────────────────────────────────────────────────────────────┐
        │ Tier 3: L3 Autonomous Cognitive Node (Shannon Entropy Gated LLM)                │
        │ • Bounded Reasoning for Unseen Vendors & Ambiguous Transfers                    │
        │ • If Entropy H(X) <= 0.35 ➔ Commit New Fact to Global Master Cache             │
        │ • If Entropy H(X) > 0.35  ➔ Route to TAP Escalation (Human CPA / Client Review) │
        └─────────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 Database Schema Definitions (`sql/schema/`)

```sql
-- 1. Master Merchants Table: Golden registry of domestic and foreign counterparties
CREATE TABLE IF NOT EXISTS toro_core.master_merchants (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    normalized_name        TEXT NOT NULL,
    legal_name             TEXT,
    country_code           VARCHAR(2) NOT NULL DEFAULT 'MA',
    merchant_category      TEXT NOT NULL, -- 'UTILITY', 'TELECOM', 'FOREIGN_SAAS', 'FUEL', 'GOVERNMENT', 'BANK'
    ice                    VARCHAR(15),   -- 15-digit Identifiant Commun de l'Entreprise
    identifiant_fiscal     VARCHAR(10),   -- IF
    registre_commerce      TEXT,          -- RC (e.g. '42510_RABAT')
    cnss_number            VARCHAR(15),   -- CNSS
    primary_domain         TEXT,
    logo_url               TEXT,
    
    -- PCGM Statutory Accounting Mappings
    default_pcgm_account   VARCHAR(10) NOT NULL, -- e.g. '614400', '614500', '612540'
    default_tva_rate       NUMERIC(5,4) NOT NULL DEFAULT 0.2000, -- 0.0700, 0.1000, 0.1400, 0.2000
    default_tva_account    VARCHAR(10) NOT NULL DEFAULT '345510',
    is_tva_deductible      BOOLEAN NOT NULL DEFAULT TRUE,
    
    -- Tax Compliance & Withholding
    is_foreign_service     BOOLEAN NOT NULL DEFAULT FALSE,
    ras_applicable         BOOLEAN NOT NULL DEFAULT FALSE,
    ras_rate               NUMERIC(5,4) DEFAULT 0.0000,          -- 0.1000 for foreign SaaS
    
    -- Metadata & Auditing
    confidence_weight      NUMERIC(3,2) NOT NULL DEFAULT 1.00,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_master_merchants_ice ON toro_core.master_merchants(ice) WHERE ice IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_master_merchants_category ON toro_core.master_merchants(merchant_category);

-- 2. Moroccan Multilingual & Arabic Alias Registry: Resolves Arabic script, Darija, French abbreviations, and bank acronyms
CREATE TABLE IF NOT EXISTS toro_core.moroccan_merchant_multilingual_aliases (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    master_merchant_id UUID NOT NULL REFERENCES toro_core.master_merchants(id) ON DELETE CASCADE,
    alias_variant      TEXT NOT NULL, -- e.g. 'اتصالات المغرب', 'Itissalat Al-Maghrib', 'IAM', 'Maroc Telecom', 'Maroc T', 'M Telecom'
    script_type        VARCHAR(30) NOT NULL DEFAULT 'FRENCH_LEGAL', -- 'ARABIC_SCRIPT', 'ARABIC_TRANSLITERATED', 'FRENCH_LEGAL', 'BANK_ABBREVIATION', 'ACRONYM'
    language_code      VARCHAR(5) NOT NULL DEFAULT 'fr', -- 'ar', 'fr', 'en'
    is_primary         BOOLEAN NOT NULL DEFAULT FALSE,
    confidence_score   NUMERIC(3,2) NOT NULL DEFAULT 1.00,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_multilingual_aliases_variant_lower ON toro_core.moroccan_merchant_multilingual_aliases(LOWER(alias_variant));
CREATE INDEX IF NOT EXISTS idx_multilingual_aliases_trgm ON toro_core.moroccan_merchant_multilingual_aliases USING gin (alias_variant gin_trgm_ops);

-- 3. Master Patterns Table: Maps noisy bank descriptor strings to Golden Master Merchants
CREATE TABLE IF NOT EXISTS toro_core.master_patterns (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    master_merchant_id UUID NOT NULL REFERENCES toro_core.master_merchants(id) ON DELETE CASCADE,
    cleaned_stem       TEXT NOT NULL, -- e.g. 'REDAL SA', 'MAROC TELECOM', 'AMAZON WEB SERVICES'
    pattern_type       VARCHAR(20) NOT NULL DEFAULT 'EXACT', -- 'EXACT', 'PREFIX', 'REGEX', 'TRIGRAM'
    regex_pattern      TEXT,
    is_intermediary    BOOLEAN NOT NULL DEFAULT FALSE, -- CMI, Fatourati, Payzone
    match_count        BIGINT NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_master_patterns_trgm ON toro_core.master_patterns USING gin (cleaned_stem gin_trgm_ops);

-- 4. Non-Resident Foreign Service Providers Table (CGI Art. 157 10% Withholding Registry)
CREATE TABLE IF NOT EXISTS toro_core.non_resident_foreign_providers (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    master_merchant_id       UUID NOT NULL REFERENCES toro_core.master_merchants(id) ON DELETE CASCADE,
    provider_name            TEXT NOT NULL, -- e.g. 'Amazon Web Services EMEA SARL', 'Google Cloud EMEA Limited'
    headquarters_country     VARCHAR(2) NOT NULL, -- 'LU', 'IE', 'US'
    tax_residency_status     TEXT NOT NULL DEFAULT 'NON_RESIDENT',
    vat_withholding_rate     NUMERIC(5,4) NOT NULL DEFAULT 0.1000, -- Statutory 10% Retenue à la Source
    service_type             TEXT NOT NULL, -- 'SAAS_CLOUD', 'DIGITAL_ADVERTISING', 'INFRASTRUCTURE', 'TECHNICAL_SERVICES'
    pcgm_expense_account     VARCHAR(10) NOT NULL DEFAULT '613670', -- Logiciels et SaaS
    pcgm_withholding_account VARCHAR(10) NOT NULL DEFAULT '445800', -- État, impôts et taxes retenus à la source
    is_active                BOOLEAN NOT NULL DEFAULT TRUE,
    statutory_legal_basis    TEXT NOT NULL DEFAULT 'CGI Maroc - Article 157 (Retenue à la Source sur Prestations de Services Étrangères)',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 5. Foreign Service Cumulative Running Totals Table (Periodic DGI Tax Declaration Tracking)
CREATE TABLE IF NOT EXISTS toro_core.foreign_service_running_totals (
    id                            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id                      TEXT NOT NULL,
    provider_id                   UUID NOT NULL REFERENCES toro_core.non_resident_foreign_providers(id) ON DELETE RESTRICT,
    fiscal_year                   INT NOT NULL,
    fiscal_month                  INT NOT NULL, -- 1-12 (for monthly VAT & RAS declarations)
    cumulative_gross_invoiced_mad NUMERIC(15,2) NOT NULL DEFAULT 0.00, -- Total gross invoiced subject to 10% RAS
    cumulative_ras_withheld_mad   NUMERIC(15,2) NOT NULL DEFAULT 0.00, -- Cumulative 10% tax withheld (credited to 445800)
    cumulative_net_paid_mad       NUMERIC(15,2) NOT NULL DEFAULT 0.00, -- Net amount transferred abroad
    transaction_count             INT NOT NULL DEFAULT 0,
    dgi_declaration_status        VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'DECLARED', 'SETTLED'
    last_transaction_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_foreign_running_total UNIQUE (realm_id, provider_id, fiscal_year, fiscal_month)
);

CREATE INDEX IF NOT EXISTS idx_foreign_running_totals_realm_period ON toro_core.foreign_service_running_totals(realm_id, fiscal_year, fiscal_month);
```

---

# 4. The 5 Core Enrichment Dimensions

When a transaction enters CEE-MA, the engine executes **5 deterministic analysis passes**:

```text
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                      INPUT TRANSACTION: raw_description, amount, date                  │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │
      ┌──────────────────┬───────────────────┼───────────────────┬──────────────────┐
      ▼                  ▼                   ▼                   ▼                  ▼
[ Dimension 1 ]    [ Dimension 2 ]     [ Dimension 3 ]     [ Dimension 4 ]    [ Dimension 5 ]
 Counterparty &       PCGM & VAT          Tax Withholding      Banking Rails &   Document & OCR
 Identifiers         Classification       & Cash Caps          Intermediaries     Correlator
  • Clean Stem        • PCGM Account      • 10% Foreign RAS    • VIR, PRLV, CHQ   • Search receipts
  • 15-digit ICE      • Exact TVA Rate    • 5% Rent RAS        • Extract Check #  • Match ±0.05 MAD
  • Foreign/Local     • Deductibility     • 5k/50k Cash Cap    • Bank Fee Split   • Link DocumentID
```

### Dimension 1: Counterparty & Statutory ID Resolution (Multilingual & Alias Engine)
1. **Sanitization & Stemming**: Strips bank noise tokens (`VIR INST`, `PRLV SEPA`, `CARTE*`, transaction reference numbers, settlement dates, authorization codes).
2. **Moroccan Multilingual & Arabic Alias Resolution**:
   - In Morocco, transactions appear in Arabic script, transliterated Darija, French legal names, or banking acronyms.
   - The engine queries `toro_core.moroccan_merchant_multilingual_aliases` to resolve any variant to the canonical Master Merchant:
     - **Maroc Telecom**: `اتصالات المغرب` | `Itissalat Al-Maghrib` | `IAM` | `Maroc Telecom` | `Maroc T` | `M Telecom` | `MT` $\rightarrow$ `Master ID (IAM)`
     - **Barid Al-Maghrib / Post**: `بريد المغرب` | `Barid Al Maghrib` | `Poste Maroc` | `BAM` $\rightarrow$ `Master ID (BAM)`
     - **Water/Elec Utilities (ONEE)**: `المكتب الوطني للكهرباء والماء الصالح للشرب` | `ONEE` | `ONEP` | `ONE` $\rightarrow$ `Master ID (ONEE)`
     - **Redal**: `ريضال` | `Redal S.A.` | `Veolia Maroc` $\rightarrow$ `Master ID (Redal)`
     - **Lydec**: `ليدك` | `Lyonnaise des Eaux de Casablanca` $\rightarrow$ `Master ID (Lydec)`
     - **Afriquia Fuel**: `أفريقيا` | `Afriquia SMDC` | `Afriquia Gaz` | `Akwa Group` $\rightarrow$ `Master ID (Afriquia)`
     - **Autoroutes du Maroc**: `الشركة الوطنية للطرق السيارة بالمغرب` | `ADM` | `Jawaz Pass` $\rightarrow$ `Master ID (ADM)`
     - **Attijariwafa Bank**: `التجاري وفا بنك` | `ATW` | `Attijari` | `Wafacash` $\rightarrow$ `Master ID (Attijari)`
     - **Banque Centrale Populaire**: `البنك الشعبي` | `BCP` | `Banque Populaire` | `Chaabi Bank` $\rightarrow$ `Master ID (BCP)`
3. **Statutory ICE Extraction**: Uses compiled regex `\b(00\d{13})\b` to extract Moroccan 15-digit ICE numbers directly from wire references.
4. **Entity Match Matrix**: Resolves vendor identity against local tenant facts and the platform-wide Golden Master Merchant directory.
5. **Domestic vs. Non-Resident Foreign Classification**: Categorizes vendors into Domestic Moroccan Entities (having ICE/IF) vs. Non-Resident Foreign Providers (e.g. AWS, Google Cloud, Microsoft, Stripe, OpenAI, GitHub, Meta Ads).

### Dimension 2: Automated PCGM Chart of Accounts & VAT Split
1. **PCGM Account Code Assignment**: Maps normalized merchants to official French/Moroccan general ledger accounts:
   - `614400`: Water & Electricity (*Achats d'eau et d'électricité*)
   - `614510`: Postal & Telecom Expenses (*Frais postaux et télécom*)
   - `612540`: Fuel & Vehicle Maintenance (*Carburants et lubrifiants*)
   - `613100`: Real Estate Rent (*Locations et charges locatives*)
   - `613670`: Software Subscriptions & Cloud Infrastructure (*Logiciels et SaaS*)
   - `614700`: Bank Services & Account Management Fees (*Services bancaires*)
   - `631100`: Bank Loan Interest & Agios (*Intérêts des emprunts et dettes*)
2. **Moroccan Statutory VAT Engine**: Determines the exact statutory VAT rate:
   - **7%**: Water distribution (*Compte 345510*)
   - **10%**: Banking fees, agios, commission charges (*Compte 345520*)
   - **14%**: Electricity and freight transport (*Compte 345510*)
   - **20%**: General goods, consulting services, SaaS (*Compte 345510*)

### Dimension 3: Statutory Foreign Provider Withholding (*RAS*) & Cumulative Running Totals
1. **Non-Resident Foreign Provider Registry (`toro_core.non_resident_foreign_providers`)**:
   - Maintains a dedicated registry of foreign corporations supplying software, cloud hosting, advertising, and technical services into Morocco.
   - Enforces statutory **10% Retenue à la Source (RAS) under Moroccan CGI Art. 157** (and applicable double tax treaties):
     $$\text{Base RAS} = \text{Gross Invoiced Amount}, \quad \text{Tax Retenue à la Source} = \text{Base RAS} \times 10\%$$
   - Credit entry automatically generated to Account `445800` (*État, impôts et taxes retenus à la source*).
2. **Cumulative Foreign Expenditure Running Totals (`toro_core.foreign_service_running_totals`)**:
   - The engine automatically computes and updates a **running cumulative total** per company/realm and per foreign provider for each fiscal month and fiscal year.
   - Tracks:
     - **Cumulative Gross Invoiced (MAD)**: Total foreign service billings to date.
     - **Cumulative RAS Withheld (MAD)**: Running total of 10% tax liability to be remitted to the DGI.
     - **Cumulative Net Paid (MAD)**: Running total of net foreign currency remittances.
     - **Transaction Count & DGI Status**: Provides instant telemetry for statutory monthly/quarterly tax filings (*Bordereau de versement de la RAS*).
3. **Commercial Property Rent Withholding (CGI Art. 160)**:
   - If payment is for commercial leasehold rent from a non-corporate landlord, automatically triggers **5% RAS** credited to Account `445800`.
4. **Statutory Cash Ceilings & Caisse Integrity (CGI Art. 193 & 210)**:
   - For cash outflows (Petty Cash `5161`), verifies:
     - Outflow does not cause Compte 5161 to drop below 0 MAD (*Interdiction de la Caisse Créditrice*).
     - Daily cumulative cash payments to the same supplier do not exceed **5,000 MAD TTC**.
     - Monthly cumulative cash payments to the same supplier do not exceed **50,000 MAD TTC**.

### Dimension 4: Banking Rails & Payment Intermediary Extraction
1. **Banking Rail Classification**:
   - `VIREMENT`: Bank transfer (Standard / Instant).
   - `PRELEVEMENT`: Direct debit (Telecommunications, utility bills, leasing, CNSS).
   - `CHEQUE`: Check payment (automatically extracts check number from descriptor).
   - `RETRAIT_GAB`: ATM cash withdrawal (initiates `5115` funds transit to `5161` petty cash).
   - `PAIEMENT_CARTE`: POS / Online card purchase.
2. **Bank Fee & Agio Separation**:
   - For descriptors matching `AGIOS`, `COMMISSION DE MOUVEMENT`, `FRAIS TENUE DE COMPTE`, automatically isolates the base fee into `614700` and extracts the 10% banking VAT into `345520`.
3. **Intermediary Payment Processors**:
   - Strips and attributes gateway layers (CMI, Payzone, Fatourati, Binga, Wafacash, Cash Plus) while preserving the underlying beneficiary merchant.

### Dimension 5: Supporting Document & OCR Receipt Correlator
1. Queries `toro_core.documents` for verified invoices, bills, and cash vouchers.
2. **Correlation Logic**:
   - Matches amount within tolerance ($\pm 0.05$ MAD).
   - Matches transaction date within a sliding window ($\pm 7$ calendar days).
   - Matches extracted supplier ICE or OCR textual stems.
3. Attaches `has_receipt: true`, `matched_document_id`, `receipt_url`, and OCR-extracted Net HT / TVA amounts directly to the envelope.

---

# 5. Agent-to-Agent (A2A) Interface Contract

CEE-MA communicates across standard **REST, NATS JetStream, and MCP (Model Context Protocol)**.

### 5.1 Request Payload (`EnrichmentRequest`)

```json
{
  "request_id": "req_01HPX7K9V3Z",
  "client_id": "tenant_agadir_trading_sarl",
  "transactions": [
    {
      "transaction_id": "txn_904820",
      "raw_description": "VIR INST 001523456000089 REDAL SA FACTURE EAU 0426",
      "amount": 1450.00,
      "currency": "MAD",
      "cash_direction": "OUTFLOW",
      "transaction_date": "2026-08-14T09:30:00Z",
      "account_code": "514100"
    }
  ]
}
```

### 5.2 Response Payload (`AnnotatedMoroccanTransactionEnvelope`)

```json
{
  "request_id": "req_01HPX7K9V3Z",
  "enriched_transactions": [
    {
      "transaction_id": "txn_904820",
      "raw_description": "PRLV اتصالات المغرب FACTURE FIBRE PRO 0826",
      "sanitized_stem": "اتصالات المغرب",
      "counterparty": {
        "normalized_name": "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
        "merchant_category": "TELECOMMUNICATIONS",
        "country": "MA",
        "is_foreign_service": false,
        "matched_alias": {
          "raw_alias": "اتصالات المغرب",
          "script_type": "ARABIC_SCRIPT",
          "language": "ar"
        },
        "identifiers": {
          "ice": "000054238000045",
          "if": "01004523",
          "rc": "48920_RABAT",
          "cnss": "1203948"
        },
        "domain": "iam.ma",
        "logo_url": "https://cdn.usetoro.com/logos/iam.png"
      },
      "pcgm_accounting": {
        "suggested_account": "614510",
        "account_label": "Frais postaux et de télécommunications",
        "default_tva_rate": 0.20,
        "tva_account": "345510",
        "tva_amount": 166.67,
        "net_ht_amount": 833.33,
        "is_deductible": true
      },
      "tax_and_compliance": {
        "ras_applicable": false,
        "ras_rate": 0.00,
        "ras_account": null,
        "statutory_guardrails": {
          "is_cash_payment": false,
          "daily_vendor_cash_total": 0.00,
          "guardrail_status": "PASS"
        }
      },
      "banking_instrument": {
        "payment_rail": "PRELEVEMENT",
        "extracted_reference": "FACTURE FIBRE PRO 0826",
        "is_intermediated": false,
        "intermediary_name": null,
        "is_bank_fee": false
      },
      "document_evidence": {
        "has_receipt": true,
        "matched_document_id": "doc_8f1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d",
        "receipt_status": "MATCHED_HIGH_CONFIDENCE",
        "extracted_invoice_number": "FAC-IAM-2026-08"
      },
      "confidence_score": 1.00,
      "enrichment_source": "GLOBAL_MULTILINGUAL_ALIAS_NETWORK",
      "processing_duration_ms": 2.1
    },
    {
      "transaction_id": "txn_904821",
      "raw_description": "CARTE 14/08 AWS EMEA SARL LUXEMBOURG US REQ 89402",
      "sanitized_stem": "AWS EMEA SARL",
      "counterparty": {
        "normalized_name": "Amazon Web Services EMEA SARL",
        "merchant_category": "FOREIGN_SAAS_CLOUD",
        "country": "LU",
        "is_foreign_service": true,
        "matched_alias": {
          "raw_alias": "AWS EMEA SARL",
          "script_type": "FRENCH_LEGAL",
          "language": "fr"
        },
        "identifiers": {
          "ice": null,
          "foreign_tax_id": "LU26372894"
        },
        "domain": "aws.amazon.com",
        "logo_url": "https://cdn.usetoro.com/logos/aws.png"
      },
      "pcgm_accounting": {
        "suggested_account": "613670",
        "account_label": "Redevances pour logiciels et SaaS étrangers",
        "default_tva_rate": 0.20,
        "tva_account": "345510",
        "tva_amount": 1000.00,
        "net_ht_amount": 5000.00,
        "is_deductible": true
      },
      "tax_and_compliance": {
        "ras_applicable": true,
        "ras_rate": 0.10,
        "ras_account": "445800",
        "ras_amount_mad": 600.00,
        "net_transferred_mad": 5400.00,
        "foreign_provider_running_totals": {
          "fiscal_year": 2026,
          "fiscal_month": 8,
          "current_month_cumulative_invoiced_mad": 24500.00,
          "current_month_cumulative_ras_mad": 2450.00,
          "current_year_cumulative_invoiced_mad": 185000.00,
          "current_year_cumulative_ras_mad": 18500.00,
          "dgi_filing_deadline": "2026-09-20"
        },
        "statutory_guardrails": {
          "is_cash_payment": false,
          "guardrail_status": "PASS"
        }
      },
      "banking_instrument": {
        "payment_rail": "PAIEMENT_CARTE",
        "extracted_reference": "US REQ 89402",
        "is_intermediated": false,
        "is_bank_fee": false
      },
      "document_evidence": {
        "has_receipt": false,
        "matched_document_id": null,
        "receipt_status": "PENDING_ATTACHMENT"
      },
      "confidence_score": 1.00,
      "enrichment_source": "NON_RESIDENT_FOREIGN_REGISTRY",
      "processing_duration_ms": 3.8
    }
  ]
}
```

---

# 6. The Shared Network Learning Flywheel (Crowdsourced Intelligence)

One of CEE-MA's most powerful architectural advantages is its **Privacy-Preserving Cross-Tenant Learning Flywheel**:

```text
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                      Company A encounters an Unseen Vendor                             │
 │                     "STE PETROLES DU MAGHREB CARBURANT"                                │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │
                                             ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                        Human CPA / Reviewer verifies once                              │
 │   * Name: Société des Pétroles du Maghreb  * ICE: 001928374000045                      │
 │   * PCGM Account: 612540 (Carburants)      * TVA Rate: 0.14 (14% Fuel VAT)             │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │
                                             ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │            Fact committed to toro_core.master_merchants (Scope: GLOBAL)                │
 │       (Zero tenant data leaked: no transaction amounts, dates, or client IDs stored)   │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │
                                             ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │               Company B (or External Agent) transacts with same vendor                 │
 │              ➔ Instant 100% Deterministic Match (<2ms) with zero LLM compute           │
 └────────────────────────────────────────────────────────────────────────────────────────┘
```

---

# 7. Verification & Automated Test Suite

To guarantee 100% compliance with Moroccan tax law and sub-5ms performance, the test harness enforces:

1. **Stem Sanitization & Noise Removal**: Tests 100+ real-world Moroccan bank statement lines across Attijariwafa, BCP, BMCE/Bank of Africa, CIH, SGMB, and Crédit du Maroc.
2. **ICE Checksum & Regex Validation**: Asserts accurate extraction of 15-digit ICE numbers.
3. **PCGM & VAT Exact Match**: Verifies proper VAT rate assignment (7% water, 10% bank fees, 14% electricity, 20% standard).
4. **RAS Cross-Border Calculation**: Asserts 10% withholding on AWS, Google, Stripe, Zoom, OpenAI, and GitHub.
5. **CGI Art. 193/210 Cash Guardrails**: Asserts rejection/warning on 5,000 MAD daily and 50,000 MAD monthly cash payments to single suppliers.
6. **Sub-5ms Execution Latency**: Verifies that 95% of cached and regex queries complete in $< 5\text{ms}$.

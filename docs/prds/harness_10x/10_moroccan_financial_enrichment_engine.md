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

---

# 8. Reasoning-Normalized Enrichment Layer

This section incorporates the requirements from `Toro_Enrichment_Reasoning_Normalization_Mini_PRD.md`. CEE-MA must convert multilingual and Morocco-specific source evidence into a compact, canonical, English-first semantic state *before* a downstream LLM makes a bookkeeping, reconciliation, compliance, or export decision. It must preserve the original French, Arabic, Darija-influenced, and bank-specific text and its jurisdictional meaning; canonical English is a reasoning aid, not a replacement for the source evidence or Moroccan identity.

## 8.1 Goals and Operating Principles

- Normalize multilingual financial text into canonical semantic concepts once, then reuse that state across the DAG.
- Bind local concepts explicitly to Morocco: for example, retain `MA.SARL`, `MA.PCGE`, MAD, and Moroccan VAT concepts rather than substituting US equivalents.
- Retrieve applicable PCGE, tax, legal, document, and bank-label knowledge objects instead of asking the LLM to recall Moroccan law from model memory.
- Keep evidence immutable and traceable: every enriched field carries source provenance or a source reference.
- Package only the minimum sufficient facts and rules for each downstream node. Do not create a single giant enrichment prompt.
- Represent ambiguity explicitly with confidence, alternatives, and warnings. Never silently force an uncertain mapping.

The layer does not replace judgment-heavy accounting decisions with deterministic rules, translate and discard source documents, or embed full statutes and prior reasoning traces in every prompt. Accounting judgment and conflict explanation remain LLM responsibilities; known synonym/code mapping, rule retrieval, and arithmetic/balance checks should be deterministic or specialized operations.

## 8.2 Processing Contract

CEE-MA processes each event in the following order:

1. Ingest raw bank data, OCR, invoices, email/counterparty text, and metadata.
2. Detect language and script at field or span level (`en`, `fr`, Arabic, Darija variant, mixed, or unknown).
3. Extract entities and terminology: company forms, banks, payment rails, taxes, account labels, invoice references, dates, and identifiers.
4. Normalize linguistic variants into stable concepts, such as `PAYMENT.VENDOR.BANK_TRANSFER`, while retaining the source string.
5. Bind the concept to Moroccan jurisdictional context, including PCGE and legal/entity semantics.
6. Retrieve applicable knowledge objects: PCGE guidance, tax/legal rules, evidence requirements, bank codes, and counterparty facts.
7. Annotate confidence, unresolved alternatives, conflicts, and warnings.
8. Produce a compact task-specific payload for the next node.

The canonical payload must support source identity and original text; canonical text and concept ID; economic direction; currency; raw and canonical counterparty details; jurisdiction context; applicable rules with stable IDs, source references, original-language excerpt references, applicability, consequence, and effective-date metadata; evidence requirements; and normalization confidence, ambiguities, and warnings.

## 8.3 Example Normalizations

| Raw input | Canonical concept | Preserved local identity | Downstream use |
| :---- | :---- | :---- | :---- |
| `Règlement fournisseur par virement` | `PAYMENT.VENDOR.BANK_TRANSFER` | Original French evidence | Classification / reconciliation |
| `Honoraires expert-comptable` | `EXPENSE.PROFESSIONAL.ACCOUNTING_FEES` | Local wording | Expense categorization |
| `SARL` | `LEGAL_ENTITY` | `MA.SARL`, not `US.LLC` | Entity/legal context |
| `TVA déductible` | `TAX.INPUT_VAT.DEDUCTIBLE` | Moroccan VAT regime | Tax treatment |
| `VIR` / `VIREMENT` | `PAYMENT.BANK_TRANSFER` | Bank-specific code | Payment-rail inference |

## 8.4 Node-Specific Context and Prompt Rules

The transaction classifier receives normalized description, direction, amount, currency, counterparty type, and relevant PCGE taxonomy. The reconciliation node receives normalized transaction identity, candidate invoices/payments, dates, amounts, references, and matching evidence. The compliance/document node receives the canonical accounting decision, applicable rule objects, evidence requirements, and original references. The Sage export node receives only final account/classification, journal metadata, and required labels.

Downstream LLMs must treat enriched state as supplied context. They must not re-translate already normalized text, infer Moroccan law from memory when a rule object is supplied, or replace MA-specific concepts with foreign equivalents. They should use concept IDs as semantic anchors, inspect original evidence for high-impact or ambiguous decisions, return a structured conflict when evidence and enriched facts disagree, and request a missing knowledge object rather than inventing a rule.

## 8.5 Failure Handling, Measurement, and Acceptance

- Low-confidence normalization preserves scored candidate interpretations and routes to constrained LLM reasoning or review.
- Unknown local phrasing remains as raw text with concept `UNKNOWN`; the system requests targeted interpretation rather than inventing a mapping.
- Conflicting sources return both facts, provenance, and a conflict object.
- Missing rule context is represented as `RULE_CONTEXT_MISSING`; stale rules are prevented through effective-date checks.
- Original excerpts remain available where translation could distort meaning.

Evaluate paired raw and enriched variants of the same accounting case. Primary measures are accounting accuracy, cross-language consistency, normalization precision, and near-zero unsupported legal recall. Secondary measures are reasoning tokens per decision, prompt tokens per node, human-review rate, latency, and cost. V1 is accepted only when provenance is retained, common local phrases do not require repeated translation, ambiguous mappings remain explicit, node payloads are task-relevant, and paired evaluation shows no material accounting-accuracy degradation from the raw-context baseline.

Initial scope is the bank-statement/bookkeeping flow: French and English normalization (with preserved Arabic and targeted Arabic support), transaction labels, counterparty normalization, common Moroccan entity forms, core PCGE concepts, VAT/document requirements, and context packaging for classifier, reconciler, compliance/document, and Sage export. Future work may add firm dictionaries, accountant-feedback loops, new abbreviation discovery, Moroccan-accounting embeddings, jurisdiction packs, uncertainty-based model routing, and language/context regression suites.

**Core requirement:** CEE-MA must make the LLM spend its reasoning budget on the accounting consequence of a transaction—not on repeatedly translating French, guessing Moroccan terminology, recalling statutes, or rereading irrelevant context.


# *Reducing multilingual, jurisdictional, and recall burden before LLM bookkeeping reasoning*

Product area: Enrichment Pipeline  |  Jurisdiction focus: Morocco  |  Status: V1 design

# **1\. Executive Summary**

**Problem.** Raw Moroccan financial data frequently contains French, Arabic, Darija-influenced text, local abbreviations, Moroccan company forms, bank-specific labels, PCGE terminology, and references to Moroccan law. Passing this raw context directly into every downstream LLM node forces the model to spend reasoning capacity on translation, terminology resolution, legal recall, and repeated interpretation before it can perform the actual bookkeeping judgment.

**Product decision.** The enrichment pipeline will convert raw multilingual and jurisdiction-specific evidence into a compact, canonical, English-first semantic representation while preserving the original source text and jurisdiction-specific meaning. Downstream reasoning nodes should reason over enriched facts and retrieved rules, not repeatedly reinterpret raw language or recall Moroccan law from model memory.

**Expected outcome.** Lower unnecessary reasoning-token consumption, smaller prompt payloads, fewer interpretation errors, more stable classifications, more auditable legal/accounting decisions, and better cross-language consistency without erasing Moroccan specificity.

# **2\. Goals and Non-Goals**

## **2.1 Goals**

* Normalize multilingual financial text into canonical semantic concepts before consequential reasoning.  
* Provide explicit Moroccan accounting and legal context when required instead of relying on model recall.  
* Preserve original text, source provenance, and jurisdiction so normalization never destroys evidence.  
* Reduce repeated translation and interpretation across the DAG by enriching once and reusing the result.  
* Constrain each downstream node to the minimum context required for its task.  
* Make enriched payloads deterministic enough to serve as reusable state for classification, reconciliation, compliance, and export.  
* Measure whether enrichment reduces reasoning tokens and error rates without reducing accounting accuracy.

## **2.2 Non-Goals**

* Replacing the LLM with deterministic rules for judgment-heavy accounting decisions.  
* Translating all source documents into English and discarding the originals.  
* Treating Moroccan concepts as direct US equivalents. For example, SARL must remain MA.SARL even if an English gloss is provided.  
* Expecting the enrichment layer to independently make the final accounting classification unless explicitly assigned that responsibility.  
* Embedding full legal statutes, full documents, or complete prior reasoning traces into every downstream prompt.

# **3\. Product Hypothesis**

For structurally equivalent bookkeeping tasks, raw multilingual and locally specific context can impose additional interpretation burden on an LLM. The enrichment layer should reduce this burden by moving translation, terminology grounding, and rule retrieval upstream.

Raw multilingual evidence \+ model recall \+ accounting judgment  
    \=\> unnecessary reasoning burden

Enriched canonical facts \+ retrieved local rules \+ accounting judgment  
    \=\> focused reasoning

The system will treat reasoning-token count as an operational signal, not as a direct proxy for correctness. The primary success criterion is decision quality; token reduction is valuable only when accuracy is preserved or improved.

# **4\. Design Principles**

**English-first reasoning, multilingual evidence:** Use English canonical concepts for internal reasoning where practical, while retaining the original French/Arabic text as evidence.

**Normalize meaning, not jurisdiction:** Convert linguistic variation into canonical concepts without replacing Moroccan legal/accounting identity with foreign analogies.

**Retrieve, do not recall:** For Moroccan law, PCGE rules, tax treatment, document requirements, and other authoritative knowledge, provide the applicable rule and source to the reasoning node rather than asking the model to remember it.

**Enrich once, reuse many times:** A phrase or entity should be resolved once and persisted in enriched state so later DAG nodes do not repeatedly reinterpret it.

**Minimum sufficient context:** Each node receives only the normalized facts, evidence, and rules required for that node.

**Evidence is immutable:** Original text and document references remain available for audit and verification even after normalization.

**Uncertainty is explicit:** Ambiguous normalization must be represented as uncertainty or alternatives, never silently forced into a single interpretation.

# **5\. Enrichment Pipeline**

**1\. Raw ingestion** \- Receive bank data, statement OCR output, invoice data, email text, counterparty text, metadata, and source-language content.

**2\. Language and script detection** \- Identify language at field or span level where relevant: English, French, Modern Standard Arabic, Moroccan Arabic/Darija variants, mixed-script, unknown.

**3\. Entity and terminology extraction** \- Extract company names, legal forms, banks, payment rails, taxes, account labels, invoice references, dates, identifiers, and accounting phrases.

**4\. Canonical semantic normalization** \- Map local phrases and variants into stable internal concepts such as PAYMENT.VENDOR.BANK\_TRANSFER, while preserving the original string.

**5\. Jurisdiction binding** \- Attach Morocco-specific identity and constraints, such as MA.SARL, MA.PCGE, MAD, Moroccan VAT concepts, and applicable legal regime.

**6\. Knowledge enrichment** \- Retrieve applicable internal knowledge objects: PCGE guidance, tax rules, legal provisions, evidence/document requirements, known bank transaction codes, counterparty knowledge.

**7\. Ambiguity annotation** \- Attach confidence, unresolved alternatives, and normalization warnings when source text cannot be safely collapsed.

**8\. Context packaging** \- Build a compact node-specific payload containing only the facts and rule objects required by the next reasoning task.

# **6\. Canonical Enriched Payload Contract**

The enrichment layer should emit a stable machine-readable representation. The exact schema may evolve, but V1 must support the following semantic fields.

{  
  "source": {  
    "source\_id": "...",  
    "source\_type": "bank\_transaction",  
    "original\_text": "Règlement fournisseur par virement",  
    "language": "fr",  
    "jurisdiction": "MA"  
  },  
  "normalized": {  
    "canonical\_text": "supplier payment by bank transfer",  
    "concept\_id": "PAYMENT.VENDOR.BANK\_TRANSFER",  
    "economic\_direction": "OUTFLOW",  
    "currency": "MAD",  
    "counterparty": {  
      "raw\_name": "...",  
      "canonical\_name": "...",  
      "entity\_type": "SUPPLIER"  
    }  
  },  
  "jurisdiction\_context": {  
    "company\_form": {  
      "raw": "SARL",  
      "canonical\_id": "MA.SARL",  
      "english\_gloss": "Moroccan limited liability company"  
    },  
    "chart\_of\_accounts": "MA.PCGE"  
  },  
  "applicable\_rules": \[  
    {  
      "rule\_id": "MA\_...",  
      "canonical\_summary": "...",  
      "source\_reference": "...",  
      "original\_language\_excerpt\_ref": "..."  
    }  
  \],  
  "evidence\_requirements": \[  
    {  
      "document\_type": "SUPPLIER\_INVOICE",  
      "reason": "...",  
      "rule\_id": "MA\_...",  
      "status": "MISSING"  
    }  
  \],  
  "normalization": {  
    "confidence": 0.97,  
    "ambiguities": \[\],  
    "warnings": \[\]  
  }  
}

# **7\. Normalization Examples**

| Raw input | Canonical concept | Preserved local identity | Downstream use |
| :---- | :---- | :---- | :---- |
| Règlement fournisseur par virement | PAYMENT.VENDOR.BANK\_TRANSFER | Original French text retained | Classification / reconciliation |
| Honoraires expert-comptable | EXPENSE.PROFESSIONAL.ACCOUNTING\_FEES | Local wording retained | Expense categorization |
| SARL | LEGAL\_ENTITY | MA.SARL, not US.LLC | Entity/legal context |
| TVA déductible | TAX.INPUT\_VAT.DEDUCTIBLE | Moroccan VAT regime attached | Tax treatment |
| VIR / VIREMENT | PAYMENT.BANK\_TRANSFER | Bank-specific code retained | Payment rail inference |

# **8\. Moroccan Knowledge Objects**

The enrichment pipeline should expose structured knowledge objects instead of inserting unstructured statutory text into downstream prompts.

* PCGE concept and account mappings.  
* Moroccan VAT and tax treatment rules relevant to bookkeeping.  
* Document and evidence requirements, including the legal/accounting reason for each requirement.  
* Company legal forms and jurisdiction-specific semantics.  
* Bank-specific transaction labels and abbreviations.  
* Common supplier, customer, payroll, rent, fee, tax, transfer, refund, and financing terminology.  
* Known multilingual synonyms and abbreviations used by Moroccan accountants.

Each rule object should include: stable rule ID, canonical English summary, applicability conditions, consequence, evidence requirement, source reference, source language, and effective-date metadata where relevant.

# **9\. Node-Specific Context Packaging**

The enrichment layer must not create one giant enriched prompt. It must create reusable enriched state and then package only the subset required by each DAG node.

**Transaction classifier:** Normalized description, direction, amount, currency, counterparty type, relevant PCGE taxonomy. No full statute text.

**Reconciliation node:** Normalized transaction identity, candidate invoices/payments, dates, amounts, references, matching evidence. Usually no legal context.

**Compliance/document node:** Canonical accounting decision, applicable rule objects, evidence requirements, original source references.

**Export node:** Final account/classification, journal metadata, labels required for Sage 100\. No reasoning history.

# **10\. Downstream LLM Prompt Contract**

Reasoning nodes should be instructed to treat enriched fields as supplied context, not to recreate enrichment work unless a conflict is detected.

* Do not translate source text unless necessary to resolve an ambiguity.  
* Do not infer Moroccan law from memory when an applicable rule object is supplied.  
* Do not replace MA-specific legal/accounting concepts with US equivalents.  
* Use canonical concept IDs as the primary semantic anchors.  
* Reference original source text when validating a high-impact or ambiguous decision.  
* If enriched facts conflict with source evidence, return a structured conflict rather than silently overriding either side.  
* If required legal/accounting context is missing, request the missing knowledge object rather than hallucinating the rule.

# **11\. Deterministic vs LLM Responsibilities**

| Responsibility | Preferred mechanism | Rationale |
| :---- | :---- | :---- |
| Language detection / obvious field parsing | Deterministic or specialized model | Avoid spending general reasoning on mechanical work |
| Known synonym / code mapping | Deterministic lookup | Stable and auditable |
| Ambiguous semantic interpretation | LLM with constrained output | Requires judgment |
| Law / PCGE retrieval | Deterministic retrieval | Ground the model in authoritative context |
| Accounting judgment | LLM | Requires contextual reasoning |
| Arithmetic / balance checks | Deterministic code or optimizer | Exactness |
| Conflict explanation | LLM | Human-readable reasoning and escalation |

# **12\. Failure and Ambiguity Handling**

**Low-confidence normalization:** Preserve candidate interpretations with confidence scores and route the ambiguity to an LLM or review node.

**Unknown local phrase:** Retain raw text, mark canonical concept UNKNOWN, and request targeted interpretation. Do not invent a mapping.

**Conflicting sources:** Return both facts with provenance and an explicit conflict object.

**Missing legal knowledge:** Mark RULE\_CONTEXT\_MISSING and retrieve the required rule. The reasoning node must not fill the gap from memory.

**Potential translation distortion:** Keep the original excerpt attached and allow high-risk nodes to inspect it directly.

**Outdated rule:** Use effective-date metadata and prevent stale legal rules from being applied silently.

# **13\. Success Metrics and Experimentation**

Evaluation must use paired cases so that the underlying accounting problem is identical across raw and enriched variants.

| Metric | Priority | Definition |
| :---- | :---- | :---- |
| Accounting accuracy | Primary | Final classification/reconciliation/compliance correctness must improve or remain statistically non-inferior. |
| Reasoning tokens per decision | Secondary | Measure reduction after enrichment for the same task/model. |
| Prompt input tokens per node | Secondary | Measure context compression from node-specific packaging. |
| Cross-language consistency | Primary | Equivalent French/English representations should converge on the same canonical facts and final decision. |
| Normalization precision | Primary | Canonical mappings must preserve economic and jurisdictional meaning. |
| Unsupported legal recall rate | Primary | Target near zero cases where a model invents or recalls a Moroccan rule instead of using supplied knowledge. |
| Human review rate | Operational | Measure whether ambiguous cases are concentrated into a small, reviewable minority. |
| Latency/cost | Operational | Track whether enrichment reduces end-to-end inference cost despite adding an upstream normalization step. |

# **14\. V1 Acceptance Criteria**

* Every enriched field retains source provenance or a source reference.  
* French/Arabic/local wording is never discarded solely because an English canonical gloss exists.  
* Canonical concept IDs are stable and reusable across DAG nodes.  
* Moroccan legal/accounting context is provided through retrieved rule objects where required.  
* Downstream nodes can operate without re-translating common local phrases already normalized upstream.  
* Downstream nodes receive only task-relevant context rather than the entire enrichment state.  
* Ambiguous mappings produce explicit uncertainty rather than a forced deterministic answer.  
* Paired evaluation demonstrates no material degradation in accounting accuracy versus the raw-context baseline.  
* The system records token usage and error outcomes so the distributional-familiarity hypothesis can be tested empirically.

# **15\. Recommended V1 Scope**

Start narrowly with the bank-statement/bookkeeping flow rather than attempting a universal multilingual ontology.

* French and English first; preserve Arabic text and support targeted Arabic normalization where it occurs in documents.  
* Bank transaction labels and descriptions.  
* Counterparty/vendor normalization.  
* Moroccan entity forms commonly encountered by accountants.  
* Core PCGE concepts needed by the existing classification DAG.  
* VAT/document-requirement knowledge needed to annotate missing evidence.  
* Node-specific payload generation for classifier, reconciler, compliance/document, and Sage export stages.

# **16\. Future Extensions**

* Learned multilingual embeddings specialized for Moroccan accounting terminology.  
* Firm-specific terminology dictionaries and accountant feedback loops.  
* Automatic discovery of new bank abbreviations and merchant aliases.  
* Jurisdiction packs for additional countries using the same canonical enrichment contract.  
* Empirical routing based on measured uncertainty, where difficult/localized inputs receive stronger models or additional retrieval.  
* Automated regression tests comparing language/context variants of the same accounting case.

# **17\. Core Product Requirement**

**The enrichment layer exists to convert a linguistically and jurisdictionally messy real-world financial event into a compact, grounded, auditable semantic state before an LLM is asked to make a consequential accounting judgment.** The model should spend its reasoning budget deciding what the transaction means for the books, not repeatedly translating French, guessing Moroccan terminology, recalling statutes from memory, or rereading irrelevant context.
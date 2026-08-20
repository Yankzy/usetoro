-- +goose Up
-- =========================================================================
-- SCHEMA: toro_core
-- MIGRATION: 047_create_moroccan_cee_master_tables.sql
-- DESCRIPTION: Master Tables & Seed Data for Moroccan Cognitive Financial Enrichment Engine (CEE-MA)
-- =========================================================================

CREATE EXTENSION IF NOT EXISTS pg_trgm;

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
    default_pcgm_account   VARCHAR(10) NOT NULL, -- e.g. '614400', '614510', '612540'
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

-- =========================================================================
-- SEED DATA: Golden Master Merchants, Multilingual Aliases, & Foreign Providers
-- =========================================================================

-- A. UTILITIES
INSERT INTO toro_core.master_merchants (id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate)
VALUES
('00000000-0000-0000-0001-000000000001', 'Redal S.A. (Veolia Maroc)', 'Redal S.A.', 'MA', 'UTILITY_WATER_ELEC', '001523456000089', '01085241', '42510_RABAT', '1849204', 'redal.ma', '614400', 0.0700, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0001-000000000002', 'Lydec (Lyonnaise des Eaux de Casablanca)', 'Lydec S.A.', 'MA', 'UTILITY_WATER_ELEC', '001511223000012', '01089944', '58210_CASABLANCA', '1849999', 'lydec.ma', '614400', 0.0700, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0001-000000000003', 'Amendis (Veolia Maroc)', 'Amendis S.A.', 'MA', 'UTILITY_WATER_ELEC', '001533445000034', '01091234', '23456_TANGER', '1920394', 'amendis.ma', '614400', 0.0700, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0001-000000000004', 'ONEE (Office National de l''Electricité et de l''Eau Potable)', 'ONEE', 'MA', 'UTILITY_ELECTRICITY', '001500000000001', '01000001', '10001_RABAT', '1000001', 'one.org.ma', '614400', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0001-000000000005', 'Société Nationale des Autoroutes du Maroc (ADM)', 'ADM S.A.', 'MA', 'INFRASTRUCTURE_TOLL', '001600000000055', '01055555', '33445_RABAT', '1555555', 'adm.co.ma', '614350', 0.2000, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0001-000000000006', 'Office National des Chemins de Fer (ONCF)', 'ONCF', 'MA', 'TRANSPORT_RAIL', '001500000000099', '01099999', '12345_RABAT', '1999999', 'oncf.ma', '614300', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000)
ON CONFLICT (id) DO NOTHING;

-- Multilingual Aliases for Utilities
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (master_merchant_id, alias_variant, script_type, language_code, is_primary)
VALUES
('00000000-0000-0000-0001-000000000001', 'ريضال', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000001', 'Redal', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0001-000000000001', 'Redal SA', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000001', 'Veolia Maroc', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000002', 'ليدك', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000002', 'Lydec', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0001-000000000002', 'Lydec SA', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000003', 'أمانديس', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000003', 'Amendis', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0001-000000000003', 'Amendis Tanger', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000003', 'Amendis Tetouan', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000004', 'المكتب الوطني للكهرباء والماء الصالح للشرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000004', 'ONEE', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0001-000000000004', 'ONEP', 'ACRONYM', 'fr', FALSE),
('00000000-0000-0000-0001-000000000004', 'ONE', 'ACRONYM', 'fr', FALSE),
('00000000-0000-0000-0001-000000000004', 'ONEE Branche Eau', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000004', 'ONEE Branche Elec', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000005', 'الشركة الوطنية للطرق السيارة بالمغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000005', 'ADM', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0001-000000000005', 'Autoroutes du Maroc', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0001-000000000005', 'Jawaz Pass', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0001-000000000005', 'Telepeage Jawaz', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0001-000000000006', 'المكتب الوطني للسكك الحديدية', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0001-000000000006', 'ONCF', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0001-000000000006', 'Al Boraq', 'ARABIC_TRANSLITERATED', 'fr', FALSE),
('00000000-0000-0000-0001-000000000006', 'ONCF Voyages', 'FRENCH_LEGAL', 'fr', FALSE)
ON CONFLICT DO NOTHING;

-- B. TELECOMS & POST
INSERT INTO toro_core.master_merchants (id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate)
VALUES
('00000000-0000-0000-0002-000000000001', 'Maroc Telecom (Itissalat Al-Maghrib S.A.)', 'Itissalat Al-Maghrib S.A.', 'MA', 'TELECOMMUNICATIONS', '000054238000045', '01004523', '48920_RABAT', '1203948', 'iam.ma', '614510', 0.2000, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0002-000000000002', 'Orange Maroc (Médi Telecom S.A.)', 'Médi Telecom S.A.', 'MA', 'TELECOMMUNICATIONS', '000089123000078', '01023456', '67890_CASABLANCA', '1345678', 'orange.ma', '614510', 0.2000, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0002-000000000003', 'Inwi (Wana Corporate S.A.)', 'Wana Corporate S.A.', 'MA', 'TELECOMMUNICATIONS', '000112233000099', '01034567', '78901_CASABLANCA', '1456789', 'inwi.ma', '614510', 0.2000, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0002-000000000004', 'Barid Al-Maghrib (Poste Maroc)', 'Barid Al-Maghrib', 'MA', 'POSTAL_AND_LOGISTICS', '001500000000077', '01077777', '11223_RABAT', '1777777', 'poste.ma', '614510', 0.2000, '345510', TRUE, FALSE, FALSE, 0.0000)
ON CONFLICT (id) DO NOTHING;

-- Multilingual Aliases for Telecoms
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (master_merchant_id, alias_variant, script_type, language_code, is_primary)
VALUES
('00000000-0000-0000-0002-000000000001', 'اتصالات المغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0002-000000000001', 'Itissalat Al-Maghrib', 'ARABIC_TRANSLITERATED', 'ar', FALSE),
('00000000-0000-0000-0002-000000000001', 'IAM', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0002-000000000001', 'Maroc Telecom', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0002-000000000001', 'Maroc T', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0002-000000000001', 'M Telecom', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0002-000000000001', 'MT', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0002-000000000002', 'أورنج المغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0002-000000000002', 'Orange Maroc', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0002-000000000002', 'Orange', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0002-000000000002', 'Medi Telecom', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0002-000000000002', 'Meditel', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0002-000000000003', 'إنوي', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0002-000000000003', 'Inwi', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0002-000000000003', 'Wana Corporate', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0002-000000000003', 'Wana', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0002-000000000004', 'بريد المغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0002-000000000004', 'Barid Al Maghrib', 'ARABIC_TRANSLITERATED', 'ar', FALSE),
('00000000-0000-0000-0002-000000000004', 'Poste Maroc', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0002-000000000004', 'BAM', 'ACRONYM', 'fr', FALSE),
('00000000-0000-0000-0002-000000000004', 'Amana Express', 'BANK_ABBREVIATION', 'fr', FALSE)
ON CONFLICT DO NOTHING;

-- C. FUEL & ENERGY
INSERT INTO toro_core.master_merchants (id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate)
VALUES
('00000000-0000-0000-0003-000000000001', 'TotalEnergies Marketing Maroc S.A.', 'TotalEnergies Marketing Maroc S.A.', 'MA', 'FUEL_STATION', '000023456000011', '01011122', '34567_CASABLANCA', '1112233', 'totalenergies.ma', '612540', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0003-000000000002', 'Afriquia SMDC (Akwa Group)', 'Société Marocaine de Distribution de Carburants (Afriquia)', 'MA', 'FUEL_STATION', '000034567000022', '01022233', '45678_CASABLANCA', '1223344', 'afriquia.ma', '612540', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0003-000000000003', 'Vivo Energy Maroc (Shell)', 'Vivo Energy Maroc S.A.', 'MA', 'FUEL_STATION', '000045678000033', '01033344', '56789_CASABLANCA', '1334455', 'vivoenergy.com', '612540', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0003-000000000004', 'Petrom (Pétroles du Maghreb)', 'Pétroles du Maghreb S.A.', 'MA', 'FUEL_STATION', '000056789000044', '01044455', '67890_CASABLANCA', '1445566', 'petrom.ma', '612540', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0003-000000000005', 'Winxo (Winxo Carburants)', 'Winxo S.A.', 'MA', 'FUEL_STATION', '000067890000055', '01055566', '78901_CASABLANCA', '1556677', 'winxo.com', '612540', 0.1400, '345510', TRUE, FALSE, FALSE, 0.0000)
ON CONFLICT (id) DO NOTHING;

-- Multilingual Aliases for Fuel
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (master_merchant_id, alias_variant, script_type, language_code, is_primary)
VALUES
('00000000-0000-0000-0003-000000000001', 'طوطال إنرجيز', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0003-000000000001', 'TotalEnergies', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0003-000000000001', 'Total Maroc', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0003-000000000001', 'Total Station', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0003-000000000002', 'أفريقيا', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0003-000000000002', 'Afriquia', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0003-000000000002', 'Afriquia SMDC', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0003-000000000002', 'Afriquia Gaz', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0003-000000000002', 'Akwa Group', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0003-000000000003', 'شيل', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0003-000000000003', 'Vivo Energy', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0003-000000000003', 'Shell Maroc', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0003-000000000003', 'Station Shell', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0003-000000000004', 'بتروم', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0003-000000000004', 'Petrom', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0003-000000000004', 'Petromin', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0003-000000000005', 'وينكسو', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0003-000000000005', 'Winxo', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0003-000000000005', 'Winxo Station', 'BANK_ABBREVIATION', 'fr', FALSE)
ON CONFLICT DO NOTHING;

-- D. BANKS (10% Banking TVA Compte 345520)
INSERT INTO toro_core.master_merchants (id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate)
VALUES
('00000000-0000-0000-0004-000000000001', 'Attijariwafa Bank S.A.', 'Attijariwafa Bank S.A.', 'MA', 'BANK_FINANCIAL', '000011111000001', '01066677', '23456_CASABLANCA', '1667788', 'attijariwafabank.com', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000002', 'Banque Centrale Populaire (BCP)', 'Banque Centrale Populaire', 'MA', 'BANK_FINANCIAL', '000022222000002', '01077788', '34567_CASABLANCA', '1778899', 'groupebcp.com', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000003', 'BMCE Bank of Africa', 'Bank of Africa - BMCE Group S.A.', 'MA', 'BANK_FINANCIAL', '000033333000003', '01088899', '45678_CASABLANCA', '1889900', 'bankofafrica.ma', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000004', 'CIH Bank (Crédit Immobilier et Hôtelier)', 'CIH Bank S.A.', 'MA', 'BANK_FINANCIAL', '000044444000004', '01099900', '56789_CASABLANCA', '1990011', 'cihbank.ma', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000005', 'Société Générale Maroc (SGMB)', 'Société Générale Marocaine de Banques', 'MA', 'BANK_FINANCIAL', '000055555000005', '01011223', '67890_CASABLANCA', '1122334', 'sgmaroc.com', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000006', 'Crédit du Maroc (CDM)', 'Crédit du Maroc S.A.', 'MA', 'BANK_FINANCIAL', '000066666000006', '01022334', '78901_CASABLANCA', '1233445', 'creditdumaroc.ma', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000),
('00000000-0000-0000-0004-000000000007', 'Al Barid Bank', 'Al Barid Bank S.A.', 'MA', 'BANK_FINANCIAL', '000077777000007', '01033445', '89012_RABAT', '1344556', 'albaridbank.ma', '614700', 0.1000, '345520', TRUE, FALSE, FALSE, 0.0000)
ON CONFLICT (id) DO NOTHING;

-- Multilingual Aliases for Banks
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (master_merchant_id, alias_variant, script_type, language_code, is_primary)
VALUES
('00000000-0000-0000-0004-000000000001', 'التجاري وفا بنك', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000001', 'Attijariwafa Bank', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000001', 'ATW', 'ACRONYM', 'fr', FALSE),
('00000000-0000-0000-0004-000000000001', 'Attijari', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0004-000000000001', 'Wafacash', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0004-000000000002', 'البنك الشعبي', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000002', 'Banque Centrale Populaire', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000002', 'BCP', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0004-000000000002', 'Banque Populaire', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0004-000000000002', 'Chaabi Bank', 'BANK_ABBREVIATION', 'fr', FALSE),
('00000000-0000-0000-0004-000000000003', 'البنك المغربي للتجارة الخارجية', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000003', 'BMCE Bank', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000003', 'Bank of Africa', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0004-000000000003', 'BOA BMCE', 'ACRONYM', 'fr', FALSE),
('00000000-0000-0000-0004-000000000004', 'بنك القرض العقاري والسياحي', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000004', 'CIH Bank', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000004', 'CIH', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0004-000000000005', 'الشركة العامة بالمغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000005', 'Societe Generale Maroc', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000005', 'SGMB', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0004-000000000006', 'مصرف المغرب', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000006', 'Credit du Maroc', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000006', 'CDM', 'ACRONYM', 'fr', TRUE),
('00000000-0000-0000-0004-000000000007', 'البريد بنك', 'ARABIC_SCRIPT', 'ar', TRUE),
('00000000-0000-0000-0004-000000000007', 'Al Barid Bank', 'FRENCH_LEGAL', 'fr', TRUE),
('00000000-0000-0000-0004-000000000007', 'ABB', 'ACRONYM', 'fr', FALSE)
ON CONFLICT DO NOTHING;

-- E. NON-RESIDENT FOREIGN SERVICE PROVIDERS (CGI Art. 157 10% Withholding)
INSERT INTO toro_core.master_merchants (id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate)
VALUES
('00000000-0000-0000-0005-000000000001', 'Amazon Web Services EMEA SARL', 'Amazon Web Services EMEA SARL', 'LU', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'aws.amazon.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000002', 'Google Cloud EMEA Limited', 'Google Cloud EMEA Limited', 'IE', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'cloud.google.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000003', 'Microsoft Ireland Operations Limited', 'Microsoft Ireland Operations Limited', 'IE', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'azure.microsoft.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000004', 'Stripe Technology Europe Limited', 'Stripe Technology Europe Limited', 'IE', 'FOREIGN_FINTECH', NULL, NULL, NULL, NULL, 'stripe.com', '614700', 0.1000, '345520', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000005', 'GitHub, Inc.', 'GitHub, Inc.', 'US', 'FOREIGN_SAAS_DEV', NULL, NULL, NULL, NULL, 'github.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000006', 'OpenAI LLC', 'OpenAI LLC', 'US', 'FOREIGN_SAAS_AI', NULL, NULL, NULL, NULL, 'openai.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000007', 'Slack Technologies Limited', 'Slack Technologies Limited', 'US', 'FOREIGN_SAAS_COMMS', NULL, NULL, NULL, NULL, 'slack.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000008', 'Zoom Video Communications, Inc.', 'Zoom Video Communications, Inc.', 'US', 'FOREIGN_SAAS_COMMS', NULL, NULL, NULL, NULL, 'zoom.us', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000009', 'Meta Platforms Ireland Limited', 'Meta Platforms Ireland Limited', 'IE', 'FOREIGN_DIGITAL_ADS', NULL, NULL, NULL, NULL, 'meta.com', '614410', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000010', 'LinkedIn Ireland Unlimited Company', 'LinkedIn Ireland Unlimited Company', 'IE', 'FOREIGN_DIGITAL_ADS', NULL, NULL, NULL, NULL, 'linkedin.com', '614410', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000011', 'Vercel Inc.', 'Vercel Inc.', 'US', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'vercel.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000012', 'Notion Labs, Inc.', 'Notion Labs, Inc.', 'US', 'FOREIGN_SAAS_PRODUCTIVITY', NULL, NULL, NULL, NULL, 'notion.so', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000013', 'DigitalOcean, LLC', 'DigitalOcean, LLC', 'US', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'digitalocean.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000),
('00000000-0000-0000-0005-000000000014', 'Heroku (Salesforce, Inc.)', 'Salesforce, Inc. (Heroku)', 'US', 'FOREIGN_SAAS_CLOUD', NULL, NULL, NULL, NULL, 'heroku.com', '613670', 0.2000, '345510', TRUE, TRUE, TRUE, 0.1000)
ON CONFLICT (id) DO NOTHING;

-- Aliases for Foreign Providers
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (master_merchant_id, alias_variant, script_type, language_code, is_primary)
VALUES
('00000000-0000-0000-0005-000000000001', 'AWS', 'ACRONYM', 'en', TRUE),
('00000000-0000-0000-0005-000000000001', 'Amazon Web Services', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000001', 'AWS EMEA SARL', 'FRENCH_LEGAL', 'fr', FALSE),
('00000000-0000-0000-0005-000000000001', 'AWS Cloud', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000002', 'Google Cloud', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000002', 'Google Workspace', 'FRENCH_LEGAL', 'en', FALSE),
('00000000-0000-0000-0005-000000000002', 'Google Ireland', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000002', 'GSuite', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000003', 'Microsoft Azure', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000003', 'MSFT Azure', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000003', 'Microsoft 365', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000003', 'Office 365', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000004', 'Stripe', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000004', 'Stripe Payments', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000004', 'Stripe Europe', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000005', 'GitHub', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000005', 'GitHub Inc', 'FRENCH_LEGAL', 'en', FALSE),
('00000000-0000-0000-0005-000000000006', 'OpenAI', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000006', 'OpenAI API', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000006', 'ChatGPT Subscription', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000007', 'Slack', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000007', 'Slack Technologies', 'FRENCH_LEGAL', 'en', FALSE),
('00000000-0000-0000-0005-000000000008', 'Zoom', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000008', 'Zoom.us', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000009', 'Meta Ads', 'BANK_ABBREVIATION', 'en', TRUE),
('00000000-0000-0000-0005-000000000009', 'Facebook Ads', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000009', 'Instagram Ads', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000010', 'LinkedIn', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000010', 'LinkedIn Ads', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000010', 'LinkedIn Premium', 'BANK_ABBREVIATION', 'en', FALSE),
('00000000-0000-0000-0005-000000000011', 'Vercel', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000011', 'Vercel Inc', 'FRENCH_LEGAL', 'en', FALSE),
('00000000-0000-0000-0005-000000000012', 'Notion', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000012', 'Notion Labs', 'FRENCH_LEGAL', 'en', FALSE),
('00000000-0000-0000-0005-000000000013', 'DigitalOcean', 'FRENCH_LEGAL', 'en', TRUE),
('00000000-0000-0000-0005-000000000014', 'Heroku', 'FRENCH_LEGAL', 'en', TRUE)
ON CONFLICT DO NOTHING;

-- Populate non_resident_foreign_providers
INSERT INTO toro_core.non_resident_foreign_providers (master_merchant_id, provider_name, headquarters_country, vat_withholding_rate, service_type, pcgm_expense_account, pcgm_withholding_account)
SELECT id, normalized_name, country_code, ras_rate, merchant_category, default_pcgm_account, '445800'
FROM toro_core.master_merchants
WHERE is_foreign_service = TRUE
ON CONFLICT DO NOTHING;


-- +goose Down
DROP TABLE IF EXISTS toro_core.foreign_service_running_totals;
DROP TABLE IF EXISTS toro_core.non_resident_foreign_providers;
DROP INDEX IF EXISTS toro_core.idx_master_patterns_trgm;
DROP TABLE IF EXISTS toro_core.master_patterns;
DROP INDEX IF EXISTS toro_core.idx_multilingual_aliases_trgm;
DROP INDEX IF EXISTS toro_core.idx_multilingual_aliases_variant_lower;
DROP TABLE IF EXISTS toro_core.moroccan_merchant_multilingual_aliases;
DROP INDEX IF EXISTS toro_core.idx_master_merchants_category;
DROP INDEX IF EXISTS toro_core.idx_master_merchants_ice;
DROP TABLE IF EXISTS toro_core.master_merchants;

-- +goose Up
-- =========================================================================
-- SCHEMA EXTENSIONS & SEED DATA: Moroccan Demo (rap_atlas_sarl)
-- =========================================================================

-- 1. shadow_erp.accounts extensions
ALTER TABLE shadow_erp.accounts 
  ADD COLUMN IF NOT EXISTS account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS parent_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS is_posting BOOLEAN DEFAULT true;

CREATE INDEX IF NOT EXISTS idx_accounts_code ON shadow_erp.accounts(realm_id, account_code);

-- 2. shadow_erp.vendors extensions
ALTER TABLE shadow_erp.vendors
  ADD COLUMN IF NOT EXISTS vendor_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS ice_number VARCHAR(15),
  ADD COLUMN IF NOT EXISTS payable_account_code VARCHAR(20) DEFAULT '441100',
  ADD COLUMN IF NOT EXISTS default_expense_account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';

CREATE INDEX IF NOT EXISTS idx_vendors_code ON shadow_erp.vendors(realm_id, vendor_code);

-- 3. shadow_erp.customers extensions
ALTER TABLE shadow_erp.customers
  ADD COLUMN IF NOT EXISTS customer_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS ice_number VARCHAR(15),
  ADD COLUMN IF NOT EXISTS receivable_account_code VARCHAR(20) DEFAULT '341100',
  ADD COLUMN IF NOT EXISTS revenue_account_code VARCHAR(20) DEFAULT '711100',
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';

CREATE INDEX IF NOT EXISTS idx_customers_code ON shadow_erp.customers(realm_id, customer_code);

-- 4. fignode.canonical_vendors extensions
ALTER TABLE fignode.canonical_vendors
  ADD COLUMN IF NOT EXISTS vendor_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS payable_account_code VARCHAR(20) DEFAULT '441100',
  ADD COLUMN IF NOT EXISTS default_expense_account_code VARCHAR(20),
  ADD COLUMN IF NOT EXISTS default_vat_rule_code VARCHAR(20) DEFAULT 'TVA20';

-- 5. New Table: shadow_erp.vat_rules
CREATE TABLE IF NOT EXISTS shadow_erp.vat_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    vat_code VARCHAR(20) NOT NULL,
    description TEXT NOT NULL,
    rate DECIMAL(5,2) NOT NULL,
    input_account_code VARCHAR(20) NOT NULL,
    output_account_code VARCHAR(20) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_vat_rule_per_realm UNIQUE (realm_id, vat_code)
);

CREATE INDEX IF NOT EXISTS idx_vat_rules_realm ON shadow_erp.vat_rules(realm_id);

-- 6. New Table: shadow_erp.journals
CREATE TABLE IF NOT EXISTS shadow_erp.journals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    journal_code VARCHAR(10) NOT NULL,
    journal_name TEXT NOT NULL,
    journal_type VARCHAR(20) NOT NULL,
    default_account_code VARCHAR(20),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_journal_per_realm UNIQUE (realm_id, journal_code)
);

CREATE INDEX IF NOT EXISTS idx_journals_realm ON shadow_erp.journals(realm_id);

-- 7. New Table: shadow_erp.bank_accounts
CREATE TABLE IF NOT EXISTS shadow_erp.bank_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    bank_name TEXT NOT NULL,
    account_number TEXT NOT NULL,
    rib VARCHAR(24),
    iban VARCHAR(34),
    ledger_account_code VARCHAR(20) NOT NULL,
    currency VARCHAR(3) DEFAULT 'MAD',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT unique_bank_account_per_realm UNIQUE (realm_id, account_number)
);

CREATE INDEX IF NOT EXISTS idx_bank_accounts_realm ON shadow_erp.bank_accounts(realm_id);


-- =========================================================================
-- SEED DATA: Demo Tenant Atlas Office Solutions SARL (rap_atlas_sarl)
-- =========================================================================

-- A. Sage Import Template Profile
INSERT INTO shadow_erp.sage_import_templates (id, realm_id, template_name, delimiter, date_format, column_mapping)
VALUES (
    'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
    'rap_atlas_sarl',
    'Atlas Sage 100 Coala Standard',
    ';',
    '020106',
    '{"columns": ["journal_code", "date", "general_account", "auxiliary_account", "piece_ref", "libelle", "debit", "credit"], "has_header": false}'::jsonb
) ON CONFLICT DO NOTHING;

-- B. Client Dossier Master
INSERT INTO shadow_erp.client_dossiers (id, realm_id, fiduciaire_id, dossier_code, company_name, ice_number, sage_template_profile_id)
VALUES (
    uuid_generate_v4(),
    'rap_atlas_sarl',
    '11111111-2222-3333-4444-555555555555',
    'atlas_sarl',
    'Atlas Office Solutions SARL',
    '001234567000089',
    'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
) ON CONFLICT (realm_id) DO NOTHING;

-- C. Chart of Accounts
INSERT INTO shadow_erp.accounts (id, erp_id, realm_id, name, account_type, account_code, parent_code, is_posting, sync_token)
VALUES
    (uuid_generate_v4(), '111100', 'rap_atlas_sarl', 'Banque Attijariwafa', 'Asset', '111100', '100000', true, '1'),
    (uuid_generate_v4(), '111200', 'rap_atlas_sarl', 'Caisse Principale', 'Asset', '111200', '100000', true, '1'),
    (uuid_generate_v4(), '341100', 'rap_atlas_sarl', 'Clients Ordinaire', 'Asset', '341100', '300000', true, '1'),
    (uuid_generate_v4(), '345520', 'rap_atlas_sarl', 'Etat - TVA récupérable sur charges', 'Asset', '345520', '300000', true, '1'),
    (uuid_generate_v4(), '345510', 'rap_atlas_sarl', 'Etat - TVA récupérable sur immobilisations', 'Asset', '345510', '300000', true, '1'),
    (uuid_generate_v4(), '441100', 'rap_atlas_sarl', 'Fournisseurs Ordinaire', 'Liability', '441100', '400000', true, '1'),
    (uuid_generate_v4(), '445510', 'rap_atlas_sarl', 'Etat - TVA facturée', 'Liability', '445510', '400000', true, '1'),
    (uuid_generate_v4(), '511100', 'rap_atlas_sarl', 'Capital Social', 'Equity', '511100', '500000', true, '1'),
    (uuid_generate_v4(), '118100', 'rap_atlas_sarl', 'Résultat de l''exercice', 'Equity', '118100', '500000', true, '1'),
    (uuid_generate_v4(), '611100', 'rap_atlas_sarl', 'Achats de marchandises', 'Expense', '611100', '600000', true, '1'),
    (uuid_generate_v4(), '613100', 'rap_atlas_sarl', 'Locations et charges locatives (Loyer)', 'Expense', '613100', '600000', true, '1'),
    (uuid_generate_v4(), '614200', 'rap_atlas_sarl', 'Eau, électricité et carburant', 'Expense', '614200', '600000', true, '1'),
    (uuid_generate_v4(), '614300', 'rap_atlas_sarl', 'Frais postaux et télécommunications', 'Expense', '614300', '600000', true, '1'),
    (uuid_generate_v4(), '616100', 'rap_atlas_sarl', 'Fournitures de bureau non stockables', 'Expense', '616100', '600000', true, '1'),
    (uuid_generate_v4(), '617100', 'rap_atlas_sarl', 'Honoraires d''experts et comptables', 'Expense', '617100', '600000', true, '1'),
    (uuid_generate_v4(), '627100', 'rap_atlas_sarl', 'Services bancaires et agios', 'Expense', '627100', '600000', true, '1'),
    (uuid_generate_v4(), '711100', 'rap_atlas_sarl', 'Ventes de marchandises au Maroc', 'Revenue', '711100', '700000', true, '1')
ON CONFLICT (realm_id, erp_id) DO NOTHING;

-- D. VAT Rules
INSERT INTO shadow_erp.vat_rules (realm_id, vat_code, description, rate, input_account_code, output_account_code)
VALUES
    ('rap_atlas_sarl', 'TVA20', 'TVA Standard 20%', 20.00, '345520', '445510'),
    ('rap_atlas_sarl', 'TVA10', 'TVA Réduite 10%', 10.00, '345520', '445510'),
    ('rap_atlas_sarl', 'TVA7',  'TVA Eau/Electricité 7%', 7.00, '345520', '445510'),
    ('rap_atlas_sarl', 'TVA0',  'Exonéré / Hors Champ', 0.00, '345000', '445000')
ON CONFLICT (realm_id, vat_code) DO NOTHING;

-- E. Journals
INSERT INTO shadow_erp.journals (realm_id, journal_code, journal_name, journal_type, default_account_code)
VALUES
    ('rap_atlas_sarl', 'VE', 'Journal des Ventes', 'Sales', '711100'),
    ('rap_atlas_sarl', 'AC', 'Journal des Achats', 'Purchases', '611100'),
    ('rap_atlas_sarl', 'BQ', 'Journal de Banque', 'Bank', '514100'),
    ('rap_atlas_sarl', 'CA', 'Journal de Caisse', 'Cash', '111200'),
    ('rap_atlas_sarl', 'OD', 'Operations Diverses', 'General', NULL)
ON CONFLICT (realm_id, journal_code) DO NOTHING;

-- F. Bank Accounts
INSERT INTO shadow_erp.bank_accounts (realm_id, bank_name, account_number, rib, iban, ledger_account_code, currency)
VALUES
    ('rap_atlas_sarl', 'Attijariwafa Bank', '007810000012345678901234', '007810000012345678901234', 'MA64007810000012345678901234', '514100', 'MAD')
ON CONFLICT (realm_id, account_number) DO NOTHING;

-- G. Vendors (shadow_erp.vendors & fignode.canonical_vendors)
INSERT INTO shadow_erp.vendors (id, erp_id, realm_id, display_name, sync_token, vendor_code, ice_number, payable_account_code, default_expense_account_code, default_vat_rule_code)
VALUES
    ('b0000000-0000-0000-0000-000000000001', 'V0001', 'rap_atlas_sarl', 'Orange Maroc SA', '1', 'V0001', '001524311000045', '441100', '614300', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000002', 'V0002', 'rap_atlas_sarl', 'Maroc Telecom (IAM)', '1', 'V0002', '001511223000012', '441100', '614300', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000003', 'V0003', 'rap_atlas_sarl', 'Marjane Business HQ', '1', 'V0003', '001655443000088', '441100', '616100', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000004', 'V0004', 'rap_atlas_sarl', 'Station Afriquia Oasis', '1', 'V0004', '001788990000033', '441100', '614200', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000005', 'V0005', 'rap_atlas_sarl', 'Lydec Casablanca', '1', 'V0005', '001433221000099', '441100', '614200', 'TVA7'),
    ('b0000000-0000-0000-0000-000000000006', 'V0006', 'rap_atlas_sarl', 'Cabinet Comptable El Fassi', '1', 'V0006', '001899112000077', '441100', '617100', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000007', 'V0007', 'rap_atlas_sarl', 'Attijariwafa Bank (Frais)', '1', 'V0007', '001100229000055', '441100', '627100', 'TVA10'),
    ('b0000000-0000-0000-0000-000000000008', 'V0008', 'rap_atlas_sarl', 'Imprimerie Moderne SARL', '1', 'V0008', '001922334000066', '441100', '616100', 'TVA20'),
    ('b0000000-0000-0000-0000-000000000009', 'V0009', 'rap_atlas_sarl', 'Société Immobilière Anfa', '1', 'V0009', '001222111000044', '441100', '613100', 'TVA0')
ON CONFLICT (realm_id, erp_id) DO NOTHING;

INSERT INTO fignode.canonical_vendors (id, entity_id, realm_id, display_name, ice_number, default_account, vendor_code, payable_account_code, default_expense_account_code, default_vat_rule_code)
VALUES
    ('c0000000-0000-0000-0000-000000000001', uuid_generate_v4(), 'rap_atlas_sarl', 'Orange Maroc SA', '001524311000045', '614300', 'V0001', '441100', '614300', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000002', uuid_generate_v4(), 'rap_atlas_sarl', 'Maroc Telecom (IAM)', '001511223000012', '614300', 'V0002', '441100', '614300', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000003', uuid_generate_v4(), 'rap_atlas_sarl', 'Marjane Business HQ', '001655443000088', '616100', 'V0003', '441100', '616100', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000004', uuid_generate_v4(), 'rap_atlas_sarl', 'Station Afriquia Oasis', '001788990000033', '614200', 'V0004', '441100', '614200', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000005', uuid_generate_v4(), 'rap_atlas_sarl', 'Lydec Casablanca', '001433221000099', '614200', 'V0005', '441100', '614200', 'TVA7'),
    ('c0000000-0000-0000-0000-000000000006', uuid_generate_v4(), 'rap_atlas_sarl', 'Cabinet Comptable El Fassi', '001899112000077', '617100', 'V0006', '441100', '617100', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000007', uuid_generate_v4(), 'rap_atlas_sarl', 'Attijariwafa Bank (Frais)', '001100229000055', '627100', 'V0007', '441100', '627100', 'TVA10'),
    ('c0000000-0000-0000-0000-000000000008', uuid_generate_v4(), 'rap_atlas_sarl', 'Imprimerie Moderne SARL', '001922334000066', '616100', 'V0008', '441100', '616100', 'TVA20'),
    ('c0000000-0000-0000-0000-000000000009', uuid_generate_v4(), 'rap_atlas_sarl', 'Société Immobilière Anfa', '001222111000044', '613100', 'V0009', '441100', '613100', 'TVA0')
ON CONFLICT DO NOTHING;

-- H. Customers (shadow_erp.customers)
INSERT INTO shadow_erp.customers (id, erp_id, realm_id, display_name, sync_token, customer_code, ice_number, receivable_account_code, revenue_account_code, default_vat_rule_code)
VALUES
    (uuid_generate_v4(), 'C0001', 'rap_atlas_sarl', 'ABC Construction SARL', '1', 'C0001', '002111222000011', '341100', '711100', 'TVA20'),
    (uuid_generate_v4(), 'C0002', 'rap_atlas_sarl', 'XYZ Industrie SA', '1', 'C0002', '002333444000022', '341100', '711100', 'TVA20'),
    (uuid_generate_v4(), 'C0003', 'rap_atlas_sarl', 'Clinique Médicale Atlas', '1', 'C0003', '002555666000033', '341100', '711100', 'TVA20'),
    (uuid_generate_v4(), 'C0004', 'rap_atlas_sarl', 'Cabinet Avocats Tazi', '1', 'C0004', '002777888000044', '341100', '711100', 'TVA20'),
    (uuid_generate_v4(), 'C0005', 'rap_atlas_sarl', 'Technopark IT Solutions', '1', 'C0005', '002999000000055', '341100', '711100', 'TVA20')
ON CONFLICT (realm_id, erp_id) DO NOTHING;

-- +goose Down
DELETE FROM shadow_erp.customers WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM fignode.canonical_vendors WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.vendors WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.bank_accounts WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.journals WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.vat_rules WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.accounts WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.client_dossiers WHERE realm_id = 'rap_atlas_sarl';
DELETE FROM shadow_erp.sage_import_templates WHERE realm_id = 'rap_atlas_sarl';

DROP TABLE IF EXISTS shadow_erp.bank_accounts;
DROP TABLE IF EXISTS shadow_erp.journals;
DROP TABLE IF EXISTS shadow_erp.vat_rules;

ALTER TABLE fignode.canonical_vendors
  DROP COLUMN IF EXISTS default_vat_rule_code,
  DROP COLUMN IF EXISTS default_expense_account_code,
  DROP COLUMN IF EXISTS payable_account_code,
  DROP COLUMN IF EXISTS vendor_code;

ALTER TABLE shadow_erp.customers
  DROP COLUMN IF EXISTS default_vat_rule_code,
  DROP COLUMN IF EXISTS revenue_account_code,
  DROP COLUMN IF EXISTS receivable_account_code,
  DROP COLUMN IF EXISTS ice_number,
  DROP COLUMN IF EXISTS customer_code;

ALTER TABLE shadow_erp.vendors
  DROP COLUMN IF EXISTS default_vat_rule_code,
  DROP COLUMN IF EXISTS default_expense_account_code,
  DROP COLUMN IF EXISTS payable_account_code,
  DROP COLUMN IF EXISTS ice_number,
  DROP COLUMN IF EXISTS vendor_code;

ALTER TABLE shadow_erp.accounts
  DROP COLUMN IF EXISTS is_posting,
  DROP COLUMN IF EXISTS parent_code,
  DROP COLUMN IF EXISTS account_code;

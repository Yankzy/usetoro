-- +goose Up
-- Immutable Shadow ERP bank-reconciliation evidence and state snapshots.
--
-- PURPOSE
-- -------
-- This migration creates the durable boundary between observed bank activity
-- and Shadow ERP accounting. It deliberately does not make a reconciliation
-- state a copy of a source document or ledger: state memberships point to
-- canonical bank_statement_lines and journal_lines instead. The source bank
-- statement itself remains toro_core.documents.
--
-- RELATIONSHIP MAP
-- ----------------
-- shadow_erp.bank_accounts
--   ├─< bank_statement_lines ─> toro_core.documents
--   ├─< bank_reconciliation_states ─< bank_reconciliation_state_memberships
--   │                                      ├─> bank_statement_lines
--   │                                      ├─> journal_lines
--   │                                      └─> reconciliation_match_groups
--   └─< reconciliation_match_groups ─< reconciliation_match_members
--                                          ├─> bank_statement_lines
--                                          └─> journal_lines
--
-- shadow_erp.journal_entries ─< journal_lines ─> shadow_erp.accounts
--
-- STATE FLOW
-- ----------
-- 1. The bank statement remains one toro_core.documents record. Its parsed,
--    normalized movements are persisted as bank_statement_lines, each pointing
--    back to that document and optionally to the fignode staging intake row.
-- 2. An approved accounting treatment is posted once as journal_entries and
--    journal_lines. Those lines, not a Sage export, are the book-side evidence.
--    The journal entry retains the originating bank line, staging row, and
--    accepted treatment payload directly; V1 does not introduce a second
--    provenance aggregate for that one bank movement -> one journal entry flow.
-- 3. Matching creates reconciliation_match_groups and members. V1 creates
--    automated 1:1 candidates; accountant-created groups support 1:N and N:1.
-- 4. Creating an OPEN or CLOSED state writes one bank_reconciliation_states
--    snapshot and state memberships referencing the unresolved/reconciled
--    evidence. The state header stores only calculated balances and its hash.
-- 5. A later period loads CARRIED_FORWARD memberships from the latest usable
--    CLOSED predecessor. It does not duplicate their financial attributes.
-- 6. A correction writes a new state chain plus an immutable invalidation event
--    for the obsolete predecessor; no historical state row is updated.
--
-- AMOUNT CONVENTION
-- -----------------
-- NUMERIC(24,4) is used at the persistence boundary. Go service code converts
-- it to fixed-scale big.Int units before arithmetic and equality checks. Bank
-- line amount is absolute; direction supplies its economic sign. Journal debit
-- and credit are stored separately and must be normalized by the service when
-- compared with a bank movement.

-- Stage 2 is an append-only proposal stream on the intake record. The current
-- hash is the optimistic-concurrency boundary used by human review and posting;
-- an approval is always tied to the exact proposal payload that was reviewed.
ALTER TABLE fignode.staging_transactions
    ADD COLUMN stage2_proposals JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN stage2_current_hash CHAR(64),
    ADD COLUMN stage2_outcome TEXT,
    ADD COLUMN stage2_review_status TEXT NOT NULL DEFAULT 'NOT_REVIEWED',
    ADD COLUMN stage2_approved_payload JSONB,
    ADD COLUMN stage2_approved_hash CHAR(64),
    ADD COLUMN stage2_reviewed_by UUID REFERENCES toro_core.users(id),
    ADD COLUMN stage2_reviewed_at TIMESTAMPTZ,
    ADD CONSTRAINT staging_transactions_stage2_proposals_array
        CHECK (jsonb_typeof(stage2_proposals) = 'array'),
    ADD CONSTRAINT staging_transactions_stage2_outcome_valid
        CHECK (stage2_outcome IS NULL OR stage2_outcome IN (
            'PROPOSED_ACCOUNTING_TREATMENT', 'HUMAN_REVIEW',
            'HOLD_UNRELIABLE_INPUT', 'HOLD_BANK_ACCOUNT_CONFIGURATION',
            'HOLD_ACCOUNT_CONFIGURATION', 'HOLD_UNSUPPORTED_TREATMENT'
        )),
    ADD CONSTRAINT staging_transactions_stage2_review_status_valid
        CHECK (stage2_review_status IN ('NOT_REVIEWED', 'PENDING', 'APPROVED', 'REJECTED')),
    ADD CONSTRAINT staging_transactions_stage2_hash_valid
        CHECK (stage2_current_hash IS NULL OR stage2_current_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT staging_transactions_stage2_approval_hash_valid
        CHECK (stage2_approved_hash IS NULL OR stage2_approved_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT staging_transactions_stage2_approval_complete
        CHECK (
            (stage2_review_status = 'APPROVED') =
            (stage2_approved_payload IS NOT NULL AND stage2_approved_hash IS NOT NULL
             AND stage2_reviewed_by IS NOT NULL AND stage2_reviewed_at IS NOT NULL)
        );

-- A source statement may be delivered more than once by Postmark/CDC. Reuse
-- the same staging session and row IDs so the canonical bank-line provenance
-- remains valid across retries instead of leaving orphan staging rows.
ALTER TABLE fignode.staging_sessions
    ADD COLUMN source_document_set_hash CHAR(64),
    ADD CONSTRAINT staging_sessions_source_document_set_hash_valid
        CHECK (source_document_set_hash IS NULL OR source_document_set_hash ~ '^[0-9a-f]{64}$');

CREATE UNIQUE INDEX idx_staging_sessions_statement_intake
    ON fignode.staging_sessions (realm_id, source_document_set_hash)
    WHERE source_document_set_hash IS NOT NULL;

CREATE TABLE shadow_erp.bank_statement_lines (
    -- Canonical, observed bank movement. This is never a proposed journal.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    bank_account_id UUID NOT NULL REFERENCES shadow_erp.bank_accounts(id),
    source_document_id UUID NOT NULL REFERENCES toro_core.documents(id),
    line_index INTEGER NOT NULL,
    external_reference TEXT,
    operation_date DATE,
    value_date DATE,
    direction TEXT NOT NULL CHECK (direction IN ('INFLOW', 'OUTFLOW')),
    amount NUMERIC(24,4) NOT NULL CHECK (amount >= 0),
    currency VARCHAR(3) NOT NULL,
    description TEXT NOT NULL,
    counterparty_name TEXT,
    source_staging_transaction_id UUID REFERENCES fignode.staging_transactions(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_document_id, line_index),
    UNIQUE (source_staging_transaction_id)
);

CREATE TABLE shadow_erp.treasury_account_mappings (
    -- Realm-owned PCGM account mapping for mechanisms that are not a physical
    -- bank account (for example card-settlement and cheque clearing).
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    treasury_role TEXT NOT NULL CHECK (treasury_role IN (
        'CARD_SETTLEMENT_CLEARING',
        'CHEQUE_RECEIVABLE_CLEARING',
        'CHEQUE_PAYABLE_CLEARING',
        'INTERNAL_TRANSFER_CLEARING'
    )),
    account_id UUID NOT NULL REFERENCES shadow_erp.accounts(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (realm_id, treasury_role)
);

CREATE TABLE shadow_erp.stage2_treatment_account_mappings (
    -- Realm-owned intent-to-counterpart configuration used by the deterministic
    -- proposal builder. Classification labels never become hard-coded postings.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    intent TEXT NOT NULL,
    account_id UUID NOT NULL REFERENCES shadow_erp.accounts(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (realm_id, intent)
);

CREATE TABLE shadow_erp.journal_entries (
    -- Posted Shadow ERP accounting document from which Sage export is derived.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    journal_id UUID REFERENCES shadow_erp.journals(id),
    entry_date DATE NOT NULL,
    currency VARCHAR(3) NOT NULL,
    piece_reference TEXT NOT NULL,
    label TEXT NOT NULL,
    source_bank_statement_line_id UUID REFERENCES shadow_erp.bank_statement_lines(id),
    source_staging_transaction_id UUID REFERENCES fignode.staging_transactions(id),
    source_treatment_payload JSONB,
    source_treatment_hash CHAR(64),
    source_workflow_trace_id TEXT,
    approved_by UUID NOT NULL REFERENCES toro_core.users(id),
    approved_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'POSTED' CHECK (status = 'POSTED'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (realm_id, piece_reference),
    UNIQUE (source_bank_statement_line_id),
    CHECK ((source_treatment_payload IS NULL) = (source_treatment_hash IS NULL)),
    CHECK (source_treatment_hash IS NULL OR source_treatment_hash ~ '^[0-9a-f]{64}$'),
    CHECK (source_workflow_trace_id IS NULL OR source_treatment_payload IS NOT NULL)
);

CREATE TABLE shadow_erp.journal_lines (
    -- Canonical book-side movement; debit XOR credit preserves double-entry form.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    journal_entry_id UUID NOT NULL REFERENCES shadow_erp.journal_entries(id),
    line_index INTEGER NOT NULL,
    account_id UUID NOT NULL REFERENCES shadow_erp.accounts(id),
    auxiliary_account_code TEXT,
    label TEXT NOT NULL,
    debit NUMERIC(24,4) NOT NULL DEFAULT 0 CHECK (debit >= 0),
    credit NUMERIC(24,4) NOT NULL DEFAULT 0 CHECK (credit >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (journal_entry_id, line_index),
    CHECK ((debit = 0) <> (credit = 0))
);

CREATE TABLE shadow_erp.reconciliation_match_groups (
    -- Evidence relationship, not a state: may be candidate, confirmed, or rejected.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    idempotency_key TEXT NOT NULL UNIQUE,
    request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    realm_id TEXT NOT NULL,
    bank_account_id UUID NOT NULL REFERENCES shadow_erp.bank_accounts(id),
    currency VARCHAR(3) NOT NULL,
    item_type TEXT NOT NULL CHECK (item_type IN ('INTERNAL_TRANSFER', 'CARD_TPE', 'GENERIC', 'CHEQUE_BILL')),
    direction TEXT NOT NULL CHECK (direction IN ('INFLOW', 'OUTFLOW')),
    amount NUMERIC(24,4) NOT NULL CHECK (amount > 0),
    bank_reference_date DATE NOT NULL,
    book_reference_date DATE NOT NULL,
    policy_max_days INTEGER NOT NULL CHECK (policy_max_days >= 0),
    policy_snapshot JSONB NOT NULL,
    evidence_ranking JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL CHECK (status IN ('CANDIDATE', 'CONFIRMED', 'REJECTED')),
    created_by UUID REFERENCES toro_core.users(id),
    created_by_kind TEXT NOT NULL CHECK (created_by_kind IN ('SYSTEM', 'USER')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_by UUID REFERENCES toro_core.users(id),
    confirmed_at TIMESTAMPTZ,
    CHECK ((status = 'CONFIRMED') = (confirmed_at IS NOT NULL AND confirmed_by IS NOT NULL)),
    CHECK (created_by_kind <> 'USER' OR created_by IS NOT NULL)
);

CREATE TABLE shadow_erp.reconciliation_match_members (
    -- Each member references exactly one bank or book source; a group contains both sides.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    match_group_id UUID NOT NULL REFERENCES shadow_erp.reconciliation_match_groups(id),
    bank_statement_line_id UUID REFERENCES shadow_erp.bank_statement_lines(id),
    journal_line_id UUID REFERENCES shadow_erp.journal_lines(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((bank_statement_line_id IS NOT NULL) <> (journal_line_id IS NOT NULL)),
    UNIQUE (match_group_id, bank_statement_line_id),
    UNIQUE (match_group_id, journal_line_id)
);

CREATE TABLE shadow_erp.bank_reconciliation_states (
    -- Append-only snapshot header. Balances are calculated state outputs, not source data.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    bank_account_id UUID NOT NULL REFERENCES shadow_erp.bank_accounts(id),
    period_key CHAR(7) NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    previous_state_id UUID REFERENCES shadow_erp.bank_reconciliation_states(id),
    supersedes_state_id UUID REFERENCES shadow_erp.bank_reconciliation_states(id),
    state_kind TEXT NOT NULL CHECK (state_kind IN ('MIGRATED_BASELINE', 'PERIOD', 'CORRECTION')),
    idempotency_key TEXT NOT NULL,
    request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED')),
    currency VARCHAR(3) NOT NULL,
    statement_opening_balance NUMERIC(24,4) NOT NULL,
    book_opening_balance NUMERIC(24,4) NOT NULL,
    bank_statement_balance NUMERIC(24,4) NOT NULL,
    book_bank_balance NUMERIC(24,4) NOT NULL,
    outstanding_book_inflows NUMERIC(24,4) NOT NULL,
    outstanding_book_outflows NUMERIC(24,4) NOT NULL,
    outstanding_bank_net NUMERIC(24,4) NOT NULL,
    expected_bank_balance NUMERIC(24,4) NOT NULL,
    difference NUMERIC(24,4) NOT NULL,
    state_hash CHAR(64) NOT NULL,
    created_by UUID NOT NULL REFERENCES toro_core.users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (bank_account_id, period_key, revision),
    UNIQUE (realm_id, bank_account_id, idempotency_key),
    UNIQUE (state_hash),
    CHECK (period_key ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    CHECK (status <> 'CLOSED' OR difference = 0)
);

CREATE TABLE shadow_erp.bank_reconciliation_state_invalidations (
    -- Append-only correction event. A state remains readable but cannot seed a new opening.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    state_id UUID NOT NULL UNIQUE REFERENCES shadow_erp.bank_reconciliation_states(id),
    reason TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES toro_core.users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE shadow_erp.bank_reconciliation_state_memberships (
    -- State-specific disposition of one source record, without copying transaction fields.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    state_id UUID NOT NULL REFERENCES shadow_erp.bank_reconciliation_states(id),
    bank_statement_line_id UUID REFERENCES shadow_erp.bank_statement_lines(id),
    journal_line_id UUID REFERENCES shadow_erp.journal_lines(id),
    disposition TEXT NOT NULL CHECK (disposition IN (
        'RECONCILED', 'UNRECONCILED', 'CARRIED_FORWARD', 'MISSING_EVIDENCE', 'HUMAN_REVIEW'
    )),
    reconciliation_match_group_id UUID REFERENCES shadow_erp.reconciliation_match_groups(id),
    carry_forward BOOLEAN NOT NULL DEFAULT FALSE,
    review_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((bank_statement_line_id IS NOT NULL) <> (journal_line_id IS NOT NULL)),
    UNIQUE (state_id, bank_statement_line_id),
    UNIQUE (state_id, journal_line_id),
    CHECK (NOT carry_forward OR disposition = 'CARRIED_FORWARD')
);

CREATE TABLE shadow_erp.reconciliation_match_windows (
    -- Realm-owned policy for automatic 1:1 date tolerance by treasury item type.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    item_type TEXT NOT NULL,
    max_days INTEGER NOT NULL CHECK (max_days >= 0),
    ageing_days INTEGER NOT NULL CHECK (ageing_days >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (realm_id, item_type)
);

CREATE TABLE shadow_erp.reconciliation_review_queue (
    -- Durable work item produced by ambiguity, policy exceptions, or ageing.
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dedupe_key TEXT NOT NULL UNIQUE,
    realm_id TEXT NOT NULL,
    bank_account_id UUID NOT NULL REFERENCES shadow_erp.bank_accounts(id),
    bank_statement_line_id UUID REFERENCES shadow_erp.bank_statement_lines(id),
    journal_line_id UUID REFERENCES shadow_erp.journal_lines(id),
    match_group_id UUID REFERENCES shadow_erp.reconciliation_match_groups(id),
    reason TEXT NOT NULL CHECK (reason IN ('AMBIGUOUS_MATCH', 'AGED_UNMATCHED', 'POLICY_EXCEPTION', 'MANUAL_REVIEW')),
    policy_snapshot JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'RESOLVED', 'DISMISSED')),
    resolved_by UUID REFERENCES toro_core.users(id),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (num_nonnulls(bank_statement_line_id, journal_line_id, match_group_id) >= 1),
    CHECK ((status = 'OPEN') = (resolved_by IS NULL AND resolved_at IS NULL))
);

INSERT INTO shadow_erp.reconciliation_match_windows (realm_id, item_type, max_days, ageing_days)
VALUES
    ('*', 'INTERNAL_TRANSFER', 1, 3),
    ('*', 'CARD_TPE', 5, 10),
    ('*', 'GENERIC', 3, 30),
    ('*', 'CHEQUE_BILL', 30, 60);

-- Atlas' historical demo seed used a non-PCGM bank account code while its
-- physical bank configuration correctly points to 514100. Supply the missing
-- canonical ledger account so the July acceptance scenario exercises posting.
INSERT INTO shadow_erp.accounts (
    erp_id, realm_id, name, account_type, account_code, parent_code,
    is_posting, active, sync_token
)
SELECT '514100', 'rap_atlas_sarl', 'Banque Attijariwafa - PCGM', 'Asset',
       '514100', '500000', TRUE, TRUE, '1'
WHERE EXISTS (SELECT 1 FROM shadow_erp.bank_accounts WHERE realm_id = 'rap_atlas_sarl' AND ledger_account_code = '514100')
  AND NOT EXISTS (SELECT 1 FROM shadow_erp.accounts WHERE realm_id = 'rap_atlas_sarl' AND account_code = '514100');

INSERT INTO shadow_erp.stage2_treatment_account_mappings (realm_id, intent, account_id)
SELECT 'rap_atlas_sarl', mapping.intent, account.id
FROM (VALUES
    ('BANK_FEE_COMMISSION', '627100'),
    ('BANK_INTEREST_AGIOS_EXPENSE', '627100'),
    ('CUSTOMER_INVOICE_RECEIPT_3421', '341100'),
    ('FOREIGN_CUSTOMER_RECEIPT', '341100'),
    ('SUPPLIER_INVOICE_PAYMENT_4411', '441100'),
    ('SUPPLIER_REFUND_RECEIPT_341X', '441100'),
    ('DIRECT_OPERATING_REVENUE_71X', '711100'),
    ('RENTAL_INCOME_RECEIPT', '711100'),
    ('DOMESTIC_RENTAL_LEASE_PAYMENT', '613100')
) AS mapping(intent, account_code)
JOIN shadow_erp.accounts account
  ON account.realm_id = 'rap_atlas_sarl' AND account.account_code = mapping.account_code
ON CONFLICT (realm_id, intent) DO NOTHING;

CREATE INDEX idx_bank_statement_lines_document ON shadow_erp.bank_statement_lines(source_document_id);
CREATE INDEX idx_journal_lines_account ON shadow_erp.journal_lines(account_id);
CREATE INDEX idx_journal_entries_bank_line ON shadow_erp.journal_entries(source_bank_statement_line_id);
CREATE INDEX idx_reconciliation_states_account_period ON shadow_erp.bank_reconciliation_states(bank_account_id, period_key, revision DESC);
CREATE INDEX idx_reconciliation_memberships_state ON shadow_erp.bank_reconciliation_state_memberships(state_id);
CREATE INDEX idx_reconciliation_review_queue_open ON shadow_erp.reconciliation_review_queue(realm_id, bank_account_id, created_at) WHERE status = 'OPEN';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% records are immutable; create a new reconciliation state or correction event', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_treasury_account_mapping_realm()
RETURNS TRIGGER AS $$
DECLARE
    account_realm TEXT;
BEGIN
    SELECT realm_id INTO account_realm
    FROM shadow_erp.accounts
    WHERE id = NEW.account_id;

    IF account_realm IS NULL OR account_realm <> NEW.realm_id THEN
        RAISE EXCEPTION 'treasury account mapping realm % must reference an account in the same realm', NEW.realm_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_journal_entry_bank_line_realm()
RETURNS TRIGGER AS $$
DECLARE
    bank_line_realm TEXT;
BEGIN
    IF NEW.source_bank_statement_line_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT realm_id INTO bank_line_realm
    FROM shadow_erp.bank_statement_lines
    WHERE id = NEW.source_bank_statement_line_id;

    IF bank_line_realm IS NULL OR bank_line_realm <> NEW.realm_id THEN
        RAISE EXCEPTION 'journal entry realm % must reference a bank line in the same realm', NEW.realm_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_stage2_proposal_transition()
RETURNS TRIGGER AS $$
DECLARE
    old_count INTEGER := jsonb_array_length(OLD.stage2_proposals);
    new_count INTEGER := jsonb_array_length(NEW.stage2_proposals);
    item_index INTEGER;
BEGIN
    IF new_count < old_count THEN
        RAISE EXCEPTION 'Stage-2 proposal history is append-only';
    END IF;
    IF old_count > 0 THEN
        FOR item_index IN 0..(old_count - 1) LOOP
            IF NEW.stage2_proposals -> item_index <> OLD.stage2_proposals -> item_index THEN
                RAISE EXCEPTION 'Stage-2 proposal history cannot be rewritten';
            END IF;
        END LOOP;
    END IF;
    IF (new_count = 0) <> (NEW.stage2_current_hash IS NULL) THEN
        RAISE EXCEPTION 'Stage-2 current hash and proposal history are inconsistent';
    END IF;
    IF new_count > 0 AND (
        NEW.stage2_proposals -> (new_count - 1) ->> 'hash' <> NEW.stage2_current_hash
        OR NEW.stage2_proposals -> (new_count - 1) -> 'output' ->> 'outcome' <> NEW.stage2_outcome
    ) THEN
        RAISE EXCEPTION 'Stage-2 current fields must identify the latest proposal revision';
    END IF;
    IF NEW.stage2_review_status = 'APPROVED' AND (
        NEW.stage2_outcome <> 'PROPOSED_ACCOUNTING_TREATMENT'
        OR NEW.stage2_approved_hash <> NEW.stage2_current_hash
    ) THEN
        RAISE EXCEPTION 'Stage-2 approval must bind the current accounting proposal hash';
    END IF;
    IF OLD.stage2_review_status = 'APPROVED'
       AND NEW.stage2_current_hash = OLD.stage2_current_hash
       AND (NEW.stage2_review_status, NEW.stage2_approved_hash, NEW.stage2_approved_payload, NEW.stage2_reviewed_by)
           IS DISTINCT FROM
           (OLD.stage2_review_status, OLD.stage2_approved_hash, OLD.stage2_approved_payload, OLD.stage2_reviewed_by) THEN
        RAISE EXCEPTION 'An approved Stage-2 revision is immutable while its proposal hash is current';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_journal_line_integrity()
RETURNS TRIGGER AS $$
DECLARE
    entry_realm TEXT;
    account_realm TEXT;
BEGIN
    SELECT realm_id INTO entry_realm FROM shadow_erp.journal_entries WHERE id = NEW.journal_entry_id;
    SELECT realm_id INTO account_realm FROM shadow_erp.accounts WHERE id = NEW.account_id;
    IF entry_realm IS NULL OR account_realm IS NULL OR entry_realm <> account_realm THEN
        RAISE EXCEPTION 'journal line account and entry must belong to the same realm';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_balanced_journal_entry()
RETURNS TRIGGER AS $$
DECLARE
    entry_id UUID;
    line_count INTEGER;
    debit_total NUMERIC(24,4);
    credit_total NUMERIC(24,4);
    bank_line_count INTEGER;
    source_direction TEXT;
    source_amount NUMERIC(24,4);
    bank_debit NUMERIC(24,4);
    bank_credit NUMERIC(24,4);
BEGIN
    entry_id := COALESCE(NEW.journal_entry_id, OLD.journal_entry_id);
    SELECT COUNT(*), COALESCE(SUM(debit), 0), COALESCE(SUM(credit), 0)
      INTO line_count, debit_total, credit_total
      FROM shadow_erp.journal_lines WHERE journal_entry_id = entry_id;
    IF line_count < 2 OR debit_total = 0 OR debit_total <> credit_total THEN
        RAISE EXCEPTION 'journal entry % must contain at least two balanced non-zero lines', entry_id;
    END IF;
    SELECT b.direction, b.amount INTO source_direction, source_amount
      FROM shadow_erp.journal_entries je
      JOIN shadow_erp.bank_statement_lines b ON b.id = je.source_bank_statement_line_id
      WHERE je.id = entry_id;
    IF source_direction IS NOT NULL THEN
      SELECT COUNT(*), COALESCE(SUM(jl.debit), 0), COALESCE(SUM(jl.credit), 0)
        INTO bank_line_count, bank_debit, bank_credit
        FROM shadow_erp.journal_entries je
        JOIN shadow_erp.bank_statement_lines b ON b.id = je.source_bank_statement_line_id
        JOIN shadow_erp.bank_accounts ba ON ba.id = b.bank_account_id
        JOIN shadow_erp.journal_lines jl ON jl.journal_entry_id = je.id
        JOIN shadow_erp.accounts a ON a.id = jl.account_id AND a.account_code = ba.ledger_account_code
        WHERE je.id = entry_id;
    END IF;
    IF source_direction IS NOT NULL AND (
        bank_line_count <> 1 OR
        (source_direction = 'INFLOW' AND (bank_debit <> source_amount OR bank_credit <> 0)) OR
        (source_direction = 'OUTFLOW' AND (bank_credit <> source_amount OR bank_debit <> 0))
    ) THEN
        RAISE EXCEPTION 'journal entry % must contain exactly one bank-ledger line matching source direction and amount', entry_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_reconciliation_state_realm()
RETURNS TRIGGER AS $$
DECLARE
    account_realm TEXT;
BEGIN
    SELECT realm_id INTO account_realm FROM shadow_erp.bank_accounts WHERE id = NEW.bank_account_id;
    IF account_realm IS NULL OR account_realm <> NEW.realm_id THEN
        RAISE EXCEPTION 'reconciliation state and bank account must belong to the same realm';
    END IF;
    IF NEW.previous_state_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.bank_reconciliation_states p
        WHERE p.id = NEW.previous_state_id AND p.realm_id = NEW.realm_id AND p.bank_account_id = NEW.bank_account_id
    ) THEN
        RAISE EXCEPTION 'previous reconciliation state must belong to the same realm and bank account';
    END IF;
    IF NEW.supersedes_state_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.bank_reconciliation_states p
        WHERE p.id = NEW.supersedes_state_id AND p.realm_id = NEW.realm_id AND p.bank_account_id = NEW.bank_account_id
    ) THEN
        RAISE EXCEPTION 'superseded reconciliation state must belong to the same realm and bank account';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_reconciliation_match_member_scope()
RETURNS TRIGGER AS $$
DECLARE
    group_realm TEXT;
    group_bank UUID;
    group_currency TEXT;
BEGIN
    SELECT realm_id, bank_account_id, currency
      INTO group_realm, group_bank, group_currency
      FROM shadow_erp.reconciliation_match_groups WHERE id = NEW.match_group_id;
    IF NEW.bank_statement_line_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.bank_statement_lines b
        WHERE b.id = NEW.bank_statement_line_id AND b.realm_id = group_realm
          AND b.bank_account_id = group_bank AND b.currency = group_currency
    ) THEN
        RAISE EXCEPTION 'bank match member is outside match-group scope';
    END IF;
    IF NEW.journal_line_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.journal_lines jl
        JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
        JOIN shadow_erp.accounts a ON a.id = jl.account_id
        JOIN shadow_erp.bank_accounts ba ON ba.id = group_bank
        WHERE jl.id = NEW.journal_line_id AND je.realm_id = group_realm
          AND je.currency = group_currency AND a.account_code = ba.ledger_account_code
    ) THEN
        RAISE EXCEPTION 'journal match member is outside match-group bank-ledger scope';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_reconciliation_match_confirmation()
RETURNS TRIGGER AS $$
DECLARE
    bank_count INTEGER;
    journal_count INTEGER;
    bank_total NUMERIC(24,4);
    journal_total NUMERIC(24,4);
    invalid_bank BOOLEAN;
    invalid_journal BOOLEAN;
BEGIN
    IF NEW.status <> 'CONFIRMED' THEN
        RETURN NEW;
    END IF;
    SELECT COUNT(*), COALESCE(SUM(b.amount), 0),
           COALESCE(BOOL_OR(b.realm_id <> NEW.realm_id OR b.bank_account_id <> NEW.bank_account_id
             OR b.currency <> NEW.currency OR b.direction <> NEW.direction), FALSE)
      INTO bank_count, bank_total, invalid_bank
      FROM shadow_erp.reconciliation_match_members m
      JOIN shadow_erp.bank_statement_lines b ON b.id = m.bank_statement_line_id
      WHERE m.match_group_id = NEW.id;
    SELECT COUNT(*), COALESCE(SUM(CASE WHEN NEW.direction = 'INFLOW' THEN jl.debit ELSE jl.credit END), 0),
           COALESCE(BOOL_OR(je.realm_id <> NEW.realm_id OR je.currency <> NEW.currency
             OR a.account_code <> ba.ledger_account_code
             OR (NEW.direction = 'INFLOW' AND (jl.debit = 0 OR jl.credit <> 0))
             OR (NEW.direction = 'OUTFLOW' AND (jl.credit = 0 OR jl.debit <> 0))), FALSE)
      INTO journal_count, journal_total, invalid_journal
      FROM shadow_erp.reconciliation_match_members m
      JOIN shadow_erp.journal_lines jl ON jl.id = m.journal_line_id
      JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
      JOIN shadow_erp.accounts a ON a.id = jl.account_id
      JOIN shadow_erp.bank_accounts ba ON ba.id = NEW.bank_account_id
      WHERE m.match_group_id = NEW.id;
    IF bank_count < 1 OR journal_count < 1 OR invalid_bank OR invalid_journal
       OR bank_total <> NEW.amount OR journal_total <> NEW.amount THEN
        RAISE EXCEPTION 'confirmed match group must contain exact, scoped, balanced bank and book evidence';
    END IF;
    IF EXISTS (
        SELECT 1 FROM shadow_erp.reconciliation_match_members selected
        JOIN shadow_erp.reconciliation_match_members other ON
          (selected.bank_statement_line_id IS NOT NULL AND selected.bank_statement_line_id = other.bank_statement_line_id)
          OR (selected.journal_line_id IS NOT NULL AND selected.journal_line_id = other.journal_line_id)
        JOIN shadow_erp.reconciliation_match_groups other_group ON other_group.id = other.match_group_id
        WHERE selected.match_group_id = NEW.id AND other.match_group_id <> NEW.id
          AND other_group.status = 'CONFIRMED'
    ) THEN
        RAISE EXCEPTION 'canonical evidence is already included in another confirmed match';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.validate_reconciliation_membership_scope()
RETURNS TRIGGER AS $$
DECLARE
    state_realm TEXT;
    state_bank UUID;
BEGIN
    SELECT realm_id, bank_account_id INTO state_realm, state_bank
    FROM shadow_erp.bank_reconciliation_states WHERE id = NEW.state_id;
    IF NEW.bank_statement_line_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.bank_statement_lines b
        WHERE b.id = NEW.bank_statement_line_id AND b.realm_id = state_realm AND b.bank_account_id = state_bank
    ) THEN
        RAISE EXCEPTION 'bank membership evidence is outside state scope';
    END IF;
    IF NEW.journal_line_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.journal_lines jl
        JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
        JOIN shadow_erp.accounts a ON a.id = jl.account_id
        JOIN shadow_erp.bank_accounts ba ON ba.id = state_bank
        WHERE jl.id = NEW.journal_line_id AND je.realm_id = state_realm AND a.account_code = ba.ledger_account_code
    ) THEN
        RAISE EXCEPTION 'journal membership evidence is outside state bank-ledger scope';
    END IF;
    IF NEW.reconciliation_match_group_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.reconciliation_match_groups g
        WHERE g.id = NEW.reconciliation_match_group_id AND g.realm_id = state_realm AND g.bank_account_id = state_bank
    ) THEN
        RAISE EXCEPTION 'match membership evidence is outside state scope';
    END IF;
    IF NEW.reconciliation_match_group_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM shadow_erp.reconciliation_match_members m
        WHERE m.match_group_id = NEW.reconciliation_match_group_id
          AND ((NEW.bank_statement_line_id IS NOT NULL AND m.bank_statement_line_id = NEW.bank_statement_line_id)
            OR (NEW.journal_line_id IS NOT NULL AND m.journal_line_id = NEW.journal_line_id))
    ) THEN
        RAISE EXCEPTION 'state evidence is not a member of its referenced match group';
    END IF;
    IF NEW.disposition = 'RECONCILED' AND (
        NEW.reconciliation_match_group_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM shadow_erp.reconciliation_match_groups g
            WHERE g.id = NEW.reconciliation_match_group_id AND g.status = 'CONFIRMED'
        )
    ) THEN
        RAISE EXCEPTION 'RECONCILED state evidence requires a confirmed match group';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION shadow_erp.prevent_closed_match_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM shadow_erp.bank_reconciliation_state_memberships m
        JOIN shadow_erp.bank_reconciliation_states s ON s.id = m.state_id
        WHERE m.reconciliation_match_group_id = OLD.id AND s.status = 'CLOSED'
    ) THEN
        RAISE EXCEPTION 'match group is immutable after inclusion in a CLOSED reconciliation state';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER treasury_account_mappings_same_realm
BEFORE INSERT OR UPDATE ON shadow_erp.treasury_account_mappings
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_treasury_account_mapping_realm();

CREATE TRIGGER stage2_treatment_account_mappings_same_realm
BEFORE INSERT OR UPDATE ON shadow_erp.stage2_treatment_account_mappings
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_treasury_account_mapping_realm();

CREATE TRIGGER journal_entries_bank_line_same_realm
BEFORE INSERT OR UPDATE ON shadow_erp.journal_entries
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_journal_entry_bank_line_realm();

CREATE TRIGGER staging_transactions_stage2_append_only
BEFORE UPDATE OF stage2_proposals, stage2_current_hash, stage2_outcome,
    stage2_review_status, stage2_approved_payload, stage2_approved_hash,
    stage2_reviewed_by, stage2_reviewed_at
ON fignode.staging_transactions
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_stage2_proposal_transition();

CREATE TRIGGER journal_lines_same_realm
BEFORE INSERT OR UPDATE ON shadow_erp.journal_lines
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_journal_line_integrity();

CREATE CONSTRAINT TRIGGER journal_lines_balanced_entry
AFTER INSERT OR UPDATE OR DELETE ON shadow_erp.journal_lines
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_balanced_journal_entry();

CREATE TRIGGER bank_reconciliation_states_same_realm
BEFORE INSERT OR UPDATE ON shadow_erp.bank_reconciliation_states
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_reconciliation_state_realm();

CREATE TRIGGER reconciliation_match_members_same_scope
BEFORE INSERT ON shadow_erp.reconciliation_match_members
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_reconciliation_match_member_scope();

CREATE TRIGGER reconciliation_match_groups_confirm_valid
BEFORE INSERT OR UPDATE OF status ON shadow_erp.reconciliation_match_groups
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_reconciliation_match_confirmation();

CREATE TRIGGER bank_reconciliation_memberships_same_scope
BEFORE INSERT OR UPDATE ON shadow_erp.bank_reconciliation_state_memberships
FOR EACH ROW EXECUTE FUNCTION shadow_erp.validate_reconciliation_membership_scope();

CREATE TRIGGER reconciliation_match_groups_closed_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.reconciliation_match_groups
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_closed_match_mutation();

CREATE TRIGGER reconciliation_match_members_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.reconciliation_match_members
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

CREATE TRIGGER bank_statement_lines_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.bank_statement_lines
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

CREATE TRIGGER journal_entries_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.journal_entries
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

CREATE TRIGGER journal_lines_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.journal_lines
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

CREATE TRIGGER bank_reconciliation_states_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.bank_reconciliation_states
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

CREATE TRIGGER bank_reconciliation_state_memberships_immutable
BEFORE UPDATE OR DELETE ON shadow_erp.bank_reconciliation_state_memberships
FOR EACH ROW EXECUTE FUNCTION shadow_erp.prevent_immutable_reconciliation_mutation();

-- The state header and membership rows are immutable. Match groups remain
-- mutable while moving from CANDIDATE to CONFIRMED/REJECTED; once a match is
-- referenced by a CLOSED state, application code must treat it as evidence.

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.reconciliation_review_queue;
DROP TABLE IF EXISTS shadow_erp.reconciliation_match_windows;
DROP TABLE IF EXISTS shadow_erp.bank_reconciliation_state_memberships;
DROP TABLE IF EXISTS shadow_erp.bank_reconciliation_state_invalidations;
DROP TABLE IF EXISTS shadow_erp.bank_reconciliation_states;
DROP TABLE IF EXISTS shadow_erp.reconciliation_match_members;
DROP TABLE IF EXISTS shadow_erp.reconciliation_match_groups;
DROP TABLE IF EXISTS shadow_erp.journal_lines;
DROP TABLE IF EXISTS shadow_erp.journal_entries;
DROP TABLE IF EXISTS shadow_erp.stage2_treatment_account_mappings;
DROP TABLE IF EXISTS shadow_erp.treasury_account_mappings;
DROP TABLE IF EXISTS shadow_erp.bank_statement_lines;
ALTER TABLE fignode.staging_transactions
    DROP COLUMN IF EXISTS stage2_reviewed_at,
    DROP COLUMN IF EXISTS stage2_reviewed_by,
    DROP COLUMN IF EXISTS stage2_approved_hash,
    DROP COLUMN IF EXISTS stage2_approved_payload,
    DROP COLUMN IF EXISTS stage2_review_status,
    DROP COLUMN IF EXISTS stage2_outcome,
    DROP COLUMN IF EXISTS stage2_current_hash,
    DROP COLUMN IF EXISTS stage2_proposals;
DROP INDEX IF EXISTS fignode.idx_staging_sessions_statement_intake;
ALTER TABLE fignode.staging_sessions
    DROP COLUMN IF EXISTS source_document_set_hash;
DROP FUNCTION IF EXISTS shadow_erp.validate_reconciliation_state_realm();
DROP FUNCTION IF EXISTS shadow_erp.validate_reconciliation_membership_scope();
DROP FUNCTION IF EXISTS shadow_erp.prevent_closed_match_mutation();
DROP FUNCTION IF EXISTS shadow_erp.validate_balanced_journal_entry();
DROP FUNCTION IF EXISTS shadow_erp.validate_journal_line_integrity();
DROP FUNCTION IF EXISTS shadow_erp.validate_journal_entry_bank_line_realm();
DROP FUNCTION IF EXISTS shadow_erp.validate_stage2_proposal_transition();
DROP FUNCTION IF EXISTS shadow_erp.validate_treasury_account_mapping_realm();
DROP FUNCTION IF EXISTS shadow_erp.validate_reconciliation_match_confirmation();
DROP FUNCTION IF EXISTS shadow_erp.validate_reconciliation_match_member_scope();
DROP FUNCTION IF EXISTS shadow_erp.prevent_immutable_reconciliation_mutation();

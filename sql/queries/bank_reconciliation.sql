-- name: GetLatestBankReconciliationState :one
SELECT *
FROM shadow_erp.bank_reconciliation_states
WHERE bank_account_id = $1
  AND period_key = $2
  AND NOT EXISTS (
      SELECT 1 FROM shadow_erp.bank_reconciliation_state_invalidations i
      WHERE i.state_id = shadow_erp.bank_reconciliation_states.id
  )
ORDER BY revision DESC
LIMIT 1;

-- name: GetLatestClosedBankReconciliationStateBefore :one
SELECT *
FROM shadow_erp.bank_reconciliation_states
WHERE bank_account_id = $1
  AND period_key < $2
  AND status = 'CLOSED'
  AND NOT EXISTS (
      SELECT 1 FROM shadow_erp.bank_reconciliation_state_invalidations i
      WHERE i.state_id = shadow_erp.bank_reconciliation_states.id
  )
ORDER BY period_key DESC, revision DESC
LIMIT 1;

-- name: GetLatestBankReconciliationStateBeforeOrAt :one
SELECT *
FROM shadow_erp.bank_reconciliation_states
WHERE bank_account_id = $1
  AND period_key <= $2
  AND NOT EXISTS (
      SELECT 1 FROM shadow_erp.bank_reconciliation_state_invalidations i
      WHERE i.state_id = shadow_erp.bank_reconciliation_states.id
  )
ORDER BY period_key DESC, revision DESC
LIMIT 1;

-- name: ListBankReconciliationStateMemberships :many
SELECT *
FROM shadow_erp.bank_reconciliation_state_memberships
WHERE state_id = $1
ORDER BY created_at, id;

-- name: CreateBankReconciliationState :one
INSERT INTO shadow_erp.bank_reconciliation_states (
    realm_id, bank_account_id, period_key, revision, previous_state_id,
    supersedes_state_id, state_kind, idempotency_key, request_hash, status, currency,
    statement_opening_balance, book_opening_balance, bank_statement_balance,
    book_bank_balance, outstanding_book_inflows, outstanding_book_outflows,
    outstanding_bank_net, expected_bank_balance, difference, state_hash, created_by
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20, $21, $22
)
RETURNING *;

-- name: CreateBankReconciliationStateMembership :one
INSERT INTO shadow_erp.bank_reconciliation_state_memberships (
    state_id, bank_statement_line_id, journal_line_id, disposition,
    reconciliation_match_group_id, carry_forward, review_reason
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: InvalidateBankReconciliationState :one
INSERT INTO shadow_erp.bank_reconciliation_state_invalidations (
    state_id, reason, created_by
) VALUES ($1, $2, $3)
RETURNING *;

-- name: GetReconciliationMatchWindow :one
SELECT *
FROM shadow_erp.reconciliation_match_windows
WHERE realm_id IN ($1, '*')
  AND item_type = $2
ORDER BY (realm_id = $1) DESC
LIMIT 1;

-- name: ListBankAccountsByNormalizedIdentifiers :many
-- Values are normalized by the caller and compared without separators or case.
-- The resolver must hold rather than select when this returns zero or multiple rows.
SELECT *
FROM shadow_erp.bank_accounts
WHERE realm_id = $1
  AND (
      (sqlc.arg(normalized_account_number)::text <> '' AND regexp_replace(upper(account_number), '[^A-Z0-9]', '', 'g') = sqlc.arg(normalized_account_number)::text)
      OR (sqlc.arg(normalized_rib)::text <> '' AND regexp_replace(upper(COALESCE(rib, '')), '[^A-Z0-9]', '', 'g') = sqlc.arg(normalized_rib)::text)
      OR (sqlc.arg(normalized_iban)::text <> '' AND regexp_replace(upper(COALESCE(iban, '')), '[^A-Z0-9]', '', 'g') = sqlc.arg(normalized_iban)::text)
  )
ORDER BY id;

-- name: CreateOrGetStatementIntakeSession :one
INSERT INTO fignode.staging_sessions (
    realm_id, kind, created_by, file_name, row_count, status, outflow_is,
    source_document_set_hash
) VALUES ($1, 'CSV', $2, $3, $4, 'PENDING', $5, $6)
ON CONFLICT (realm_id, source_document_set_hash)
    WHERE source_document_set_hash IS NOT NULL
DO UPDATE SET source_document_set_hash = EXCLUDED.source_document_set_hash
RETURNING *;

-- name: CreateOrGetStatementIntakeRow :one
WITH inserted AS (
    INSERT INTO fignode.staging_transactions (
        session_id, row_index, source_type, raw_description, raw_amount,
        raw_date, status
    ) VALUES ($1, $2, 'BankStatement', $3, $4, $5, 'PENDING')
    ON CONFLICT (session_id, row_index)
        WHERE session_id IS NOT NULL AND row_index IS NOT NULL
    DO NOTHING
    RETURNING id
)
SELECT id FROM inserted
UNION ALL
SELECT id FROM fignode.staging_transactions
WHERE session_id = $1 AND row_index = $2
LIMIT 1;

-- name: InsertBankStatementLine :execrows
-- Canonical statement evidence is immutable. A replay of the same source
-- document therefore leaves the existing line untouched and lets the caller
-- verify that the normalized evidence is identical.
INSERT INTO shadow_erp.bank_statement_lines (
    realm_id,
    bank_account_id,
    source_document_id,
    line_index,
    external_reference,
    operation_date,
    value_date,
    direction,
    amount,
    currency,
    description,
    counterparty_name,
    source_staging_transaction_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13
)
ON CONFLICT (source_document_id, line_index) DO NOTHING;

-- name: GetBankStatementLineByDocumentIndex :one
SELECT *
FROM shadow_erp.bank_statement_lines
WHERE source_document_id = $1
  AND line_index = $2;

-- name: ListBankStatementLinesByDocument :many
SELECT *
FROM shadow_erp.bank_statement_lines
WHERE source_document_id = $1
ORDER BY line_index;

-- name: GetTreasuryAccountMapping :one
SELECT *
FROM shadow_erp.treasury_account_mappings
WHERE realm_id = $1
  AND treasury_role = $2;

-- name: GetStage2TreatmentAccountMapping :one
SELECT m.*, a.account_code
FROM shadow_erp.stage2_treatment_account_mappings m
JOIN shadow_erp.accounts a ON a.id = m.account_id
WHERE m.realm_id = $1 AND m.intent = $2
  AND a.active = TRUE AND a.deleted_at IS NULL;

-- name: GetBankStatementLineByStagingTransaction :one
SELECT *
FROM shadow_erp.bank_statement_lines
WHERE source_staging_transaction_id = $1;

-- name: PersistStage2Proposal :one
UPDATE fignode.staging_transactions
SET stage2_proposals = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_proposals
        ELSE stage2_proposals || jsonb_build_array(jsonb_build_object(
            'revision', jsonb_array_length(stage2_proposals) + 1,
            'hash', sqlc.arg(proposal_hash)::text,
            'recorded_at', NOW(),
            'output', sqlc.arg(proposal)::jsonb
        ))
    END,
    stage2_current_hash = sqlc.arg(proposal_hash)::text,
    stage2_outcome = sqlc.arg(outcome)::text,
    stage2_review_status = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_review_status
        ELSE sqlc.arg(review_status)::text
    END,
    stage2_approved_payload = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_approved_payload
        ELSE NULL
    END,
    stage2_approved_hash = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_approved_hash
        ELSE NULL
    END,
    stage2_reviewed_by = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_reviewed_by
        ELSE NULL
    END,
    stage2_reviewed_at = CASE
        WHEN stage2_current_hash = sqlc.arg(proposal_hash)::text THEN stage2_reviewed_at
        ELSE NULL
    END,
    updated_at = NOW()
WHERE id = sqlc.arg(staging_transaction_id)::uuid
RETURNING *;

-- name: ApproveStage2Proposal :one
UPDATE fignode.staging_transactions
SET stage2_review_status = 'APPROVED',
    stage2_approved_payload = sqlc.arg(approved_payload)::jsonb,
    stage2_approved_hash = sqlc.arg(expected_hash)::text,
    stage2_reviewed_by = sqlc.arg(actor_user_id)::uuid,
    stage2_reviewed_at = COALESCE(stage2_reviewed_at, NOW()),
    updated_at = NOW()
WHERE id = sqlc.arg(staging_transaction_id)::uuid
  AND stage2_outcome = 'PROPOSED_ACCOUNTING_TREATMENT'
  AND (
      stage2_review_status = 'PENDING'
      OR (stage2_review_status = 'APPROVED'
          AND stage2_approved_hash = sqlc.arg(expected_hash)::text
          AND stage2_approved_payload = sqlc.arg(approved_payload)::jsonb
          AND stage2_reviewed_by = sqlc.arg(actor_user_id)::uuid)
  )
  AND stage2_current_hash = sqlc.arg(expected_hash)::text
RETURNING *;

-- name: RejectStage2Proposal :one
UPDATE fignode.staging_transactions
SET stage2_review_status = 'REJECTED',
    stage2_approved_payload = NULL,
    stage2_approved_hash = NULL,
    stage2_reviewed_by = NULL,
    stage2_reviewed_at = NULL,
    updated_at = NOW()
WHERE id = sqlc.arg(staging_transaction_id)::uuid
  AND stage2_current_hash = sqlc.arg(expected_hash)::text
RETURNING *;

-- name: GetActiveUserInEntity :one
SELECT *
FROM toro_core.users
WHERE id = $1
  AND entity_id = $2
  AND is_active = TRUE;

-- name: GetAccountByRealmAndCode :one
SELECT *
FROM shadow_erp.accounts
WHERE realm_id = $1
  AND account_code = $2
  AND active = TRUE
  AND deleted_at IS NULL;

-- name: GetBankJournalForRealm :one
SELECT *
FROM shadow_erp.journals
WHERE realm_id = $1
  AND journal_type = 'Bank'
ORDER BY journal_code
LIMIT 1;

-- name: GetJournalEntryByBankStatementLine :one
SELECT *
FROM shadow_erp.journal_entries
WHERE source_bank_statement_line_id = $1;

-- name: CreateJournalEntry :one
INSERT INTO shadow_erp.journal_entries (
    realm_id, journal_id, entry_date, currency, piece_reference, label,
    source_bank_statement_line_id, source_staging_transaction_id,
    source_treatment_payload, source_treatment_hash, source_workflow_trace_id,
    approved_by, approved_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13
)
RETURNING *;

-- name: CreateJournalLine :one
INSERT INTO shadow_erp.journal_lines (
    journal_entry_id, line_index, account_id, auxiliary_account_code,
    label, debit, credit
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListJournalLinesByEntry :many
SELECT * FROM shadow_erp.journal_lines
WHERE journal_entry_id = $1
ORDER BY line_index;

-- name: GetBankReconciliationStateByIdempotencyKey :one
SELECT * FROM shadow_erp.bank_reconciliation_states
WHERE realm_id = $1 AND bank_account_id = $2 AND idempotency_key = $3;

-- name: ListUsableClosedStateDescendants :many
WITH RECURSIVE descendants AS (
    SELECT s.* FROM shadow_erp.bank_reconciliation_states s WHERE s.previous_state_id = $1
    UNION ALL
    SELECT s.* FROM shadow_erp.bank_reconciliation_states s
    JOIN descendants d ON s.previous_state_id = d.id
)
SELECT d.* FROM descendants d
WHERE NOT EXISTS (
    SELECT 1 FROM shadow_erp.bank_reconciliation_state_invalidations i WHERE i.state_id = d.id
)
ORDER BY d.period_key, d.revision;

-- name: ListUnmatchedBankStatementLines :many
SELECT b.*
FROM shadow_erp.bank_statement_lines b
WHERE b.realm_id = $1 AND b.bank_account_id = $2
  AND NOT EXISTS (
      SELECT 1 FROM shadow_erp.reconciliation_match_members m
      JOIN shadow_erp.reconciliation_match_groups g ON g.id = m.match_group_id
      WHERE m.bank_statement_line_id = b.id AND g.status = 'CONFIRMED'
  )
ORDER BY COALESCE(b.operation_date, b.value_date), b.id;

-- name: ListUnmatchedBankJournalLines :many
SELECT jl.*, je.entry_date, je.currency, je.source_bank_statement_line_id
FROM shadow_erp.journal_lines jl
JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
JOIN shadow_erp.bank_accounts ba ON ba.realm_id = je.realm_id
JOIN shadow_erp.accounts a ON a.id = jl.account_id AND a.account_code = ba.ledger_account_code
WHERE je.realm_id = $1 AND ba.id = $2
  AND NOT EXISTS (
      SELECT 1 FROM shadow_erp.reconciliation_match_members m
      JOIN shadow_erp.reconciliation_match_groups g ON g.id = m.match_group_id
      WHERE m.journal_line_id = jl.id AND g.status = 'CONFIRMED'
  )
ORDER BY je.entry_date, jl.id;

-- name: CreateReconciliationMatchGroup :one
INSERT INTO shadow_erp.reconciliation_match_groups (
    idempotency_key, request_hash, realm_id, bank_account_id, currency, item_type, direction, amount,
    bank_reference_date, book_reference_date, policy_max_days,
    policy_snapshot, evidence_ranking, status, created_by, created_by_kind
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: CreateReconciliationMatchMember :one
INSERT INTO shadow_erp.reconciliation_match_members (
    match_group_id, bank_statement_line_id, journal_line_id
) VALUES ($1, $2, $3)
RETURNING *;

-- name: ConfirmReconciliationMatchGroup :one
UPDATE shadow_erp.reconciliation_match_groups
SET status = 'CONFIRMED', confirmed_by = $2, confirmed_at = NOW()
WHERE id = $1 AND status = 'CANDIDATE'
RETURNING *;

-- name: CreateReconciliationReviewQueueItem :one
INSERT INTO shadow_erp.reconciliation_review_queue (
    dedupe_key, realm_id, bank_account_id, bank_statement_line_id, journal_line_id,
    match_group_id, reason, policy_snapshot
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = EXCLUDED.dedupe_key
RETURNING *;

-- name: GetReconciliationMatchGroupByIdempotencyKey :one
SELECT * FROM shadow_erp.reconciliation_match_groups WHERE idempotency_key = $1;

-- name: ListOpenReconciliationReviewQueue :many
SELECT * FROM shadow_erp.reconciliation_review_queue
WHERE realm_id = $1 AND bank_account_id = $2 AND status = 'OPEN'
ORDER BY created_at, id;

-- name: ResolveReviewQueueForMatchGroup :execrows
UPDATE shadow_erp.reconciliation_review_queue
SET status = 'RESOLVED', resolved_by = $2, resolved_at = NOW()
WHERE match_group_id = $1 AND status = 'OPEN';

-- name: ResolveReconciliationReviewQueueItem :one
UPDATE shadow_erp.reconciliation_review_queue
SET status = $3, resolved_by = $2, resolved_at = NOW()
WHERE id = $1 AND status = 'OPEN' AND $3 IN ('RESOLVED', 'DISMISSED')
RETURNING *;

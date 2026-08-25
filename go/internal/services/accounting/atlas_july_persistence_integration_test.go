package accounting

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAtlasJulyPersistencePath(t *testing.T) {
	databaseURL := os.Getenv("BANK_RECONCILIATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set BANK_RECONCILIATION_TEST_DATABASE_URL to run migration-backed acceptance")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	entityID, actorID := uuid.NewString(), uuid.NewString()
	bankAccountID, bankLedgerID, expenseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	documentID, stagingSessionID, stagingID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	bankLineID, journalID := uuid.NewString(), uuid.NewString()
	realmID := "atlas_acceptance_" + uuid.NewString()[:8]

	mustExec := func(query string, args ...interface{}) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed acceptance fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO toro_core.entities (id, name, entity_type) VALUES ($1, 'Atlas Acceptance SARL', 'client')`, entityID)
	mustExec(`INSERT INTO toro_core.users (id, entity_id, email, password_hash, is_active) VALUES ($1, $2, $3, 'integration-only', TRUE)`, actorID, entityID, "atlas-"+uuid.NewString()+"@example.test")
	mustExec(`INSERT INTO toro_core.erp_connections (entity_id, erp_system, realm_id, access_token, refresh_token, expires_at) VALUES ($1, 'sage', $2, 'integration-only', 'integration-only', NOW() + INTERVAL '1 hour')`, entityID, realmID)
	mustExec(`INSERT INTO shadow_erp.accounts (id, erp_id, realm_id, name, account_type, account_code, parent_code, is_posting, active, sync_token) VALUES
		($1, '514100', $3, 'Banque Atlas', 'Asset', '514100', '500000', TRUE, TRUE, '1'),
		($2, '627100', $3, 'Frais bancaires', 'Expense', '627100', '620000', TRUE, TRUE, '1')`, bankLedgerID, expenseID, realmID)
	mustExec(`INSERT INTO shadow_erp.bank_accounts (id, realm_id, bank_name, account_number, rib, iban, ledger_account_code, currency) VALUES ($1, $2, 'Atlas Bank', 'ATLAS-001', 'RIB-ATLAS-001', 'MA00ATLAS001', '514100', 'MAD')`, bankAccountID, realmID)
	mustExec(`INSERT INTO shadow_erp.journals (id, realm_id, journal_code, journal_name, journal_type) VALUES ($1, $2, 'BQ', 'Banque', 'Bank')`, journalID, realmID)
	mustExec(`INSERT INTO toro_core.documents (id, session_id, document_type, file_name, mime_type, s3_url, sha256, ocr_status, raw_ocr_json, source_channel) VALUES ($1, $2, 'BANK_STATEMENT', 'atlas-july.pdf', 'application/pdf', 'integration/atlas-july.pdf', $3, 'EMBEDDINGS_SUCCESS', '{}'::jsonb, 'EMAIL')`, documentID, stagingSessionID, uuid.NewString())
	mustExec(`INSERT INTO fignode.staging_sessions (id, realm_id, kind, created_by, file_name, row_count, status, outflow_is, source_document_set_hash) VALUES ($1, $2, 'CSV', $3, 'atlas-july.pdf', 1, 'PENDING', 'NEGATIVE', $4)`, stagingSessionID, realmID, actorID, StateHash([]string{documentID}, nil))
	mustExec(`INSERT INTO fignode.staging_transactions (id, session_id, row_index, source_type, raw_description, raw_amount, raw_date, status) VALUES ($1, $2, 0, 'BankStatement', 'FRAIS BANCAIRES', '-250.0000', '2026-07-02', 'PENDING')`, stagingID, stagingSessionID)
	mustExec(`INSERT INTO shadow_erp.bank_statement_lines (id, realm_id, bank_account_id, source_document_id, line_index, operation_date, direction, amount, currency, description, source_staging_transaction_id) VALUES ($1, $2, $3, $4, 0, DATE '2026-07-02', 'OUTFLOW', 250.0000, 'MAD', 'FRAIS BANCAIRES', $5)`, bankLineID, realmID, bankAccountID, documentID, stagingID)

	stage2 := Stage2Output{
		SchemaVersion: Stage2SchemaVersion,
		Context: Stage2WorkflowContext{
			EntityID: entityID, RealmID: realmID, WorkflowID: uuid.NewString(), WorkflowTraceID: "postmark/atlas-july",
			SessionID: stagingSessionID, SourceDocumentIDs: []string{documentID}, StatementPeriodKey: "2026-07",
			StatementOpeningBalance: "1000.0000", StatementClosingBalance: "750.0000",
		},
		BankLine: Stage2BankLine{
			StagingTransactionID: stagingID, BankStatementLineID: bankLineID, BankAccountID: bankAccountID,
			BankLedgerAccountCode: "514100", OperationDate: "2026-07-02", Direction: "OUTFLOW",
			Amount: "250.0000", Currency: "MAD", Description: "FRAIS BANCAIRES",
		},
		Classification: Stage2Classification{Intent: "BANK_FEE_COMMISSION", Confidence: 0.99},
		Outcome:        Stage2ProposedTreatment,
		Treatment: &ProposedAccountingTreatment{
			EntryDate: "2026-07-02", Currency: "MAD", Label: "FRAIS BANCAIRES",
			Lines: []ProposedJournalLine{
				{LineIndex: 0, AccountID: expenseID, AccountCode: "627100", Label: "FRAIS BANCAIRES", Debit: "250.0000", Credit: "0.0000"},
				{LineIndex: 1, AccountID: bankLedgerID, AccountCode: "514100", Label: "FRAIS BANCAIRES", Debit: "0.0000", Credit: "250.0000"},
			},
		},
	}
	proposalHash, err := stage2.Hash()
	if err != nil {
		t.Fatal(err)
	}
	proposalJSON, _ := json.Marshal(stage2)
	queries := database.New(pool)
	if _, err := queries.PersistStage2Proposal(ctx, database.PersistStage2ProposalParams{
		ProposalHash: proposalHash, Proposal: proposalJSON, Outcome: stage2.Outcome,
		ReviewStatus: "PENDING", StagingTransactionID: pgUUID(t, stagingID),
	}); err != nil {
		t.Fatalf("persist Stage-2: %v", err)
	}

	posting := NewJournalPostingService(pool)
	posted, err := posting.PostApprovedProposal(ctx, PostApprovedProposalCommand{EntityID: entityID, ActorUserID: actorID, ExpectedProposalHash: proposalHash, Output: stage2})
	if err != nil || len(posted.JournalLineIDs) != 2 {
		t.Fatalf("post canonical journal: result=%+v err=%v", posted, err)
	}
	replay, err := posting.PostApprovedProposal(ctx, PostApprovedProposalCommand{EntityID: entityID, ActorUserID: actorID, ExpectedProposalHash: proposalHash, Output: stage2})
	if err != nil || !replay.IdempotentReplay || replay.JournalEntryID != posted.JournalEntryID {
		t.Fatalf("journal retry was not idempotent: result=%+v err=%v", replay, err)
	}

	matching := NewReconciliationMatchingService(pool)
	candidates, err := matching.GenerateOneToOneCandidates(ctx, GenerateOneToOneCandidatesCommand{RealmID: realmID, BankAccountID: bankAccountID, AsOf: time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC)})
	if err != nil || len(candidates.CandidateGroupIDs) != 1 {
		t.Fatalf("generate exact match: result=%+v err=%v", candidates, err)
	}
	confirmed, err := matching.ConfirmCandidate(ctx, ConfirmMatchCandidateCommand{ReconciliationActor: ReconciliationActor{EntityID: entityID, ActorUserID: actorID}, MatchGroupID: candidates.CandidateGroupIDs[0]})
	if err != nil || confirmed.Status != "CONFIRMED" {
		t.Fatalf("confirm exact match: result=%+v err=%v", confirmed, err)
	}

	lifecycle := NewBankReconciliationService(pool)
	actor := ReconciliationActor{EntityID: entityID, ActorUserID: actorID}
	baselineCommand := CreateMigratedBaselineCommand{
		ReconciliationActor: actor, RealmID: realmID, BankAccountID: bankAccountID,
		PeriodKey: "2026-06", Currency: "MAD", StatementBalance: "1000", BookBalance: "1000", IdempotencyKey: "atlas-june-baseline",
	}
	baselineResults, baselineErrors := runConcurrentStateCommands(func() (ReconciliationStateResult, error) {
		return lifecycle.CreateMigratedBaseline(ctx, baselineCommand)
	})
	if baselineErrors[0] != nil || baselineErrors[1] != nil || baselineResults[0].StateID != baselineResults[1].StateID || baselineResults[0].IdempotentReplay == baselineResults[1].IdempotentReplay {
		t.Fatalf("concurrent June baseline was not serialized/idempotent: results=%+v errors=%v", baselineResults, baselineErrors)
	}
	bookJournalLineID := posted.JournalLineIDs[1]
	memberships := []ReconciliationEvidenceRef{
		{BankStatementLineID: bankLineID, Disposition: "RECONCILED", MatchGroupID: confirmed.MatchGroupID},
		{JournalLineID: bookJournalLineID, Disposition: "RECONCILED", MatchGroupID: confirmed.MatchGroupID},
	}
	julyOpen, err := lifecycle.PreparePeriod(ctx, PrepareReconciliationPeriodCommand{
		ReconciliationActor: actor, RealmID: realmID, BankAccountID: bankAccountID, PeriodKey: "2026-07", Currency: "MAD",
		StatementOpeningBalance: "1000", StatementBalance: "750", IdempotencyKey: "atlas-july-open", Memberships: memberships,
	})
	if err != nil || julyOpen.Status != "OPEN" {
		t.Fatalf("prepare July: result=%+v err=%v", julyOpen, err)
	}
	closeCommand := CloseReconciliationPeriodCommand{
		ReconciliationActor: actor, RealmID: realmID, BankAccountID: bankAccountID,
		PeriodKey: "2026-07", IdempotencyKey: "atlas-july-close", ExpectedStateHash: julyOpen.StateHash,
	}
	closeResults, closeErrors := runConcurrentStateCommands(func() (ReconciliationStateResult, error) {
		return lifecycle.ClosePeriod(ctx, closeCommand)
	})
	if closeErrors[0] != nil || closeErrors[1] != nil || closeResults[0].StateID != closeResults[1].StateID || closeResults[0].IdempotentReplay == closeResults[1].IdempotentReplay {
		t.Fatalf("concurrent July close was not serialized/idempotent: results=%+v errors=%v", closeResults, closeErrors)
	}
	julyClosed := closeResults[0]
	augustOpen, err := lifecycle.PreparePeriod(ctx, PrepareReconciliationPeriodCommand{
		ReconciliationActor: actor, RealmID: realmID, BankAccountID: bankAccountID, PeriodKey: "2026-08", Currency: "MAD",
		StatementOpeningBalance: "750", StatementBalance: "750", IdempotencyKey: "atlas-august-open",
	})
	if err != nil || augustOpen.PreviousStateID != julyClosed.StateID {
		t.Fatalf("derive August opening: result=%+v err=%v", augustOpen, err)
	}
	var statementOpening, bookOpening pgtype.Numeric
	if err := pool.QueryRow(ctx, `SELECT statement_opening_balance, book_opening_balance FROM shadow_erp.bank_reconciliation_states WHERE id = $1`, augustOpen.StateID).Scan(&statementOpening, &bookOpening); err != nil {
		t.Fatal(err)
	}
	statementText, _ := numericText(statementOpening)
	bookText, _ := numericText(bookOpening)
	if MustReconciliationMoney(statementText).String() != "750.0000" || MustReconciliationMoney(bookText).String() != "750.0000" {
		t.Fatalf("August opening did not derive from July close: statement=%s book=%s", statementText, bookText)
	}
}

func pgUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := postingUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func runConcurrentStateCommands(command func() (ReconciliationStateResult, error)) ([2]ReconciliationStateResult, [2]error) {
	var results [2]ReconciliationStateResult
	var errors [2]error
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range results {
		go func() {
			defer wait.Done()
			results[index], errors[index] = command()
		}()
	}
	wait.Wait()
	return results, errors
}

package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BankReconciliationService struct{ pool *pgxpool.Pool }

func NewBankReconciliationService(pool *pgxpool.Pool) *BankReconciliationService {
	return &BankReconciliationService{pool: pool}
}

type ReconciliationActor struct {
	EntityID    string `json:"entity_id"`
	ActorUserID string `json:"actor_user_id"`
}

type ReconciliationEvidenceRef struct {
	BankStatementLineID string `json:"bank_statement_line_id,omitempty"`
	JournalLineID       string `json:"journal_line_id,omitempty"`
	Disposition         string `json:"disposition"`
	MatchGroupID        string `json:"match_group_id,omitempty"`
	CarryForward        bool   `json:"carry_forward,omitempty"`
	ReviewReason        string `json:"review_reason,omitempty"`
}

type CreateMigratedBaselineCommand struct {
	ReconciliationActor
	RealmID          string                      `json:"realm_id"`
	BankAccountID    string                      `json:"bank_account_id"`
	PeriodKey        string                      `json:"period_key"`
	Currency         string                      `json:"currency"`
	StatementBalance string                      `json:"statement_balance"`
	BookBalance      string                      `json:"book_balance"`
	IdempotencyKey   string                      `json:"idempotency_key"`
	Memberships      []ReconciliationEvidenceRef `json:"memberships"`
}

type PrepareReconciliationPeriodCommand struct {
	ReconciliationActor
	RealmID                 string                      `json:"realm_id"`
	BankAccountID           string                      `json:"bank_account_id"`
	PeriodKey               string                      `json:"period_key"`
	Currency                string                      `json:"currency"`
	StatementOpeningBalance string                      `json:"statement_opening_balance"`
	StatementBalance        string                      `json:"statement_balance"`
	IdempotencyKey          string                      `json:"idempotency_key"`
	Memberships             []ReconciliationEvidenceRef `json:"memberships,omitempty"`
}

type ReviseOpenReconciliationCommand struct {
	PrepareReconciliationPeriodCommand
}

type CloseReconciliationPeriodCommand struct {
	ReconciliationActor
	RealmID           string `json:"realm_id"`
	BankAccountID     string `json:"bank_account_id"`
	PeriodKey         string `json:"period_key"`
	IdempotencyKey    string `json:"idempotency_key"`
	ExpectedStateHash string `json:"expected_state_hash"`
}

type CorrectClosedReconciliationCommand struct {
	PrepareReconciliationPeriodCommand
	ClosedStateID string `json:"closed_state_id"`
	Reason        string `json:"reason"`
}

type ReconciliationStateResult struct {
	StateID          string `json:"state_id"`
	StateHash        string `json:"state_hash"`
	Status           string `json:"status"`
	StateKind        string `json:"state_kind"`
	PeriodKey        string `json:"period_key"`
	Revision         int32  `json:"revision"`
	PreviousStateID  string `json:"previous_state_id,omitempty"`
	IdempotentReplay bool   `json:"idempotent_replay"`
}

func (s *BankReconciliationService) CreateMigratedBaseline(ctx context.Context, command CreateMigratedBaselineCommand) (ReconciliationStateResult, error) {
	statement, err := NewReconciliationMoney(command.StatementBalance)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	book, err := NewReconciliationMoney(command.BookBalance)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	requestHash, err := reconciliationRequestHash([]string{"MIGRATED_BASELINE", command.RealmID, command.BankAccountID, command.PeriodKey, command.Currency, statement.String(), book.String()}, command.Memberships)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	return s.withLockedAccount(ctx, command.ReconciliationActor, command.RealmID, command.BankAccountID, command.IdempotencyKey,
		func(ctx context.Context, tx pgx.Tx, q *database.Queries, bankID, actorID pgtype.UUID) (ReconciliationStateResult, error) {
			if replay, ok, err := stateReplay(ctx, q, command.RealmID, bankID, command.IdempotencyKey, func(state database.ShadowErpBankReconciliationState) bool {
				statementText, _ := numericText(state.BankStatementBalance)
				bookText, _ := numericText(state.BookBankBalance)
				return state.StateKind == "MIGRATED_BASELINE" && state.RequestHash == requestHash && state.PeriodKey == command.PeriodKey && state.Currency == command.Currency && MustReconciliationMoney(statementText).Equal(statement) && MustReconciliationMoney(bookText).Equal(book)
			}); err != nil || ok {
				return replay, err
			}
			var anyPriorState bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM shadow_erp.bank_reconciliation_states WHERE bank_account_id = $1)", bankID).Scan(&anyPriorState); err != nil {
				return ReconciliationStateResult{}, err
			}
			if anyPriorState {
				return ReconciliationStateResult{}, fmt.Errorf("migrated baseline is allowed only for an account with no prior state")
			}
			memberships, position, err := s.resolvePosition(ctx, tx, command.RealmID, bankID, command.Currency, command.PeriodKey, statement, command.Memberships, book, time.Time{})
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			position.StatementOpeningBalance = statement
			position.BookOpeningBalance = book
			return s.insertState(ctx, q, stateInsertSpec{RealmID: command.RealmID, BankAccountID: bankID, PeriodKey: command.PeriodKey, Revision: 1, StateKind: "MIGRATED_BASELINE", IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, Status: "CLOSED", Currency: command.Currency, ActorID: actorID, Position: position, Memberships: memberships})
		})
}

func (s *BankReconciliationService) PreparePeriod(ctx context.Context, command PrepareReconciliationPeriodCommand) (ReconciliationStateResult, error) {
	return s.prepareOrRevise(ctx, command, false, "PERIOD", pgtype.UUID{})
}

func (s *BankReconciliationService) ReviseOpenState(ctx context.Context, command ReviseOpenReconciliationCommand) (ReconciliationStateResult, error) {
	return s.prepareOrRevise(ctx, command.PrepareReconciliationPeriodCommand, true, "PERIOD", pgtype.UUID{})
}

func (s *BankReconciliationService) ClosePeriod(ctx context.Context, command CloseReconciliationPeriodCommand) (ReconciliationStateResult, error) {
	requestHash, err := reconciliationRequestHash([]string{"CLOSE", command.RealmID, command.BankAccountID, command.PeriodKey, command.ExpectedStateHash}, nil)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	return s.withLockedAccount(ctx, command.ReconciliationActor, command.RealmID, command.BankAccountID, command.IdempotencyKey,
		func(ctx context.Context, _ pgx.Tx, q *database.Queries, bankID, actorID pgtype.UUID) (ReconciliationStateResult, error) {
			if replay, ok, err := stateReplay(ctx, q, command.RealmID, bankID, command.IdempotencyKey, func(state database.ShadowErpBankReconciliationState) bool {
				return state.Status == "CLOSED" && state.RequestHash == requestHash && state.PeriodKey == command.PeriodKey && state.PreviousStateID.Valid
			}); err != nil || ok {
				return replay, err
			}
			latest, err := q.GetLatestBankReconciliationState(ctx, database.GetLatestBankReconciliationStateParams{BankAccountID: bankID, PeriodKey: command.PeriodKey})
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			if latest.Status != "OPEN" {
				return ReconciliationStateResult{}, fmt.Errorf("latest state is not OPEN")
			}
			if command.ExpectedStateHash == "" || latest.StateHash != command.ExpectedStateHash {
				return ReconciliationStateResult{}, fmt.Errorf("OPEN state changed after close authorization")
			}
			difference, err := numericText(latest.Difference)
			if err != nil || !MustReconciliationMoney(difference).IsZero() {
				return ReconciliationStateResult{}, fmt.Errorf("cannot close reconciliation with difference %s", difference)
			}
			memberships, err := q.ListBankReconciliationStateMemberships(ctx, latest.ID)
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			refs := membershipModelsForClose(memberships)
			position, err := positionFromState(latest)
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			return s.insertState(ctx, q, stateInsertSpec{RealmID: command.RealmID, BankAccountID: bankID, PeriodKey: command.PeriodKey, Revision: latest.Revision + 1, PreviousStateID: latest.ID, StateKind: latest.StateKind, IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, Status: "CLOSED", Currency: latest.Currency, ActorID: actorID, Position: position, Memberships: refs})
		})
}

func (s *BankReconciliationService) CorrectClosedPeriod(ctx context.Context, command CorrectClosedReconciliationCommand) (ReconciliationStateResult, error) {
	if command.Reason == "" {
		return ReconciliationStateResult{}, fmt.Errorf("correction reason is required")
	}
	closedID, err := postingUUID(command.ClosedStateID)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	statement, err := NewReconciliationMoney(command.StatementBalance)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	reportedOpening, err := NewReconciliationMoney(command.StatementOpeningBalance)
	if err != nil {
		return ReconciliationStateResult{}, fmt.Errorf("statement opening balance: %w", err)
	}
	requestHash, err := reconciliationRequestHash([]string{"CORRECT", command.RealmID, command.BankAccountID, command.PeriodKey, command.Currency, reportedOpening.String(), statement.String(), command.ClosedStateID, command.Reason}, command.Memberships)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	return s.withLockedAccount(ctx, command.ReconciliationActor, command.RealmID, command.BankAccountID, command.IdempotencyKey,
		func(ctx context.Context, tx pgx.Tx, q *database.Queries, bankID, actorID pgtype.UUID) (ReconciliationStateResult, error) {
			if replay, ok, err := stateReplay(ctx, q, command.RealmID, bankID, command.IdempotencyKey, func(state database.ShadowErpBankReconciliationState) bool {
				return state.StateKind == "CORRECTION" && state.RequestHash == requestHash && state.PeriodKey == command.PeriodKey && state.SupersedesStateID == closedID
			}); err != nil || ok {
				return replay, err
			}
			var target database.ShadowErpBankReconciliationState
			if err := tx.QueryRow(ctx, `SELECT id, realm_id, bank_account_id, period_key, revision, previous_state_id,
				supersedes_state_id, state_kind, idempotency_key, request_hash, status, currency,
				statement_opening_balance, book_opening_balance, bank_statement_balance,
				book_bank_balance, outstanding_book_inflows, outstanding_book_outflows,
				outstanding_bank_net, expected_bank_balance, difference, state_hash, created_by, created_at
				FROM shadow_erp.bank_reconciliation_states WHERE id = $1`, closedID).Scan(
				&target.ID, &target.RealmID, &target.BankAccountID, &target.PeriodKey, &target.Revision,
				&target.PreviousStateID, &target.SupersedesStateID, &target.StateKind, &target.IdempotencyKey, &target.RequestHash,
				&target.Status, &target.Currency, &target.StatementOpeningBalance, &target.BookOpeningBalance,
				&target.BankStatementBalance, &target.BookBankBalance, &target.OutstandingBookInflows,
				&target.OutstandingBookOutflows, &target.OutstandingBankNet, &target.ExpectedBankBalance,
				&target.Difference, &target.StateHash, &target.CreatedBy, &target.CreatedAt,
			); err != nil {
				return ReconciliationStateResult{}, err
			}
			if target.Status != "CLOSED" || target.RealmID != command.RealmID || target.BankAccountID != bankID || target.PeriodKey != command.PeriodKey {
				return ReconciliationStateResult{}, fmt.Errorf("correction target is not the requested closed state")
			}
			descendants, err := q.ListUsableClosedStateDescendants(ctx, target.ID)
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			if _, err := q.InvalidateBankReconciliationState(ctx, database.InvalidateBankReconciliationStateParams{StateID: target.ID, Reason: command.Reason, CreatedBy: actorID}); err != nil {
				return ReconciliationStateResult{}, err
			}
			for _, state := range descendants {
				if _, err := q.InvalidateBankReconciliationState(ctx, database.InvalidateBankReconciliationStateParams{StateID: state.ID, Reason: command.Reason, CreatedBy: actorID}); err != nil {
					return ReconciliationStateResult{}, err
				}
			}
			openingStatement, _ := numericText(target.StatementOpeningBalance)
			openingBook, _ := numericText(target.BookOpeningBalance)
			if !MustReconciliationMoney(openingStatement).Equal(reportedOpening) {
				return ReconciliationStateResult{}, fmt.Errorf("reported statement opening does not match the corrected state's derived opening")
			}
			periodStart, _ := time.Parse("2006-01", command.PeriodKey)
			memberships, position, err := s.resolvePosition(ctx, tx, command.RealmID, bankID, command.Currency, command.PeriodKey, statement, command.Memberships, MustReconciliationMoney(openingBook), periodStart)
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			position.StatementOpeningBalance = MustReconciliationMoney(openingStatement)
			position.BookOpeningBalance = MustReconciliationMoney(openingBook)
			var nextRevision int32
			if err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(revision), 0) + 1 FROM shadow_erp.bank_reconciliation_states WHERE bank_account_id = $1 AND period_key = $2", bankID, command.PeriodKey).Scan(&nextRevision); err != nil {
				return ReconciliationStateResult{}, err
			}
			return s.insertState(ctx, q, stateInsertSpec{RealmID: command.RealmID, BankAccountID: bankID, PeriodKey: command.PeriodKey, Revision: nextRevision, PreviousStateID: target.ID, SupersedesStateID: target.ID, StateKind: "CORRECTION", IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, Status: "OPEN", Currency: command.Currency, ActorID: actorID, Position: position, Memberships: memberships})
		})
}

func (s *BankReconciliationService) prepareOrRevise(ctx context.Context, command PrepareReconciliationPeriodCommand, revise bool, stateKind string, supersedes pgtype.UUID) (ReconciliationStateResult, error) {
	statement, err := NewReconciliationMoney(command.StatementBalance)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	reportedOpening, err := NewReconciliationMoney(command.StatementOpeningBalance)
	if err != nil {
		return ReconciliationStateResult{}, fmt.Errorf("statement opening balance: %w", err)
	}
	operation := "PREPARE"
	if revise {
		operation = "REVISE"
	}
	requestHash, err := reconciliationRequestHash([]string{operation, command.RealmID, command.BankAccountID, command.PeriodKey, command.Currency, reportedOpening.String(), statement.String()}, command.Memberships)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	return s.withLockedAccount(ctx, command.ReconciliationActor, command.RealmID, command.BankAccountID, command.IdempotencyKey,
		func(ctx context.Context, tx pgx.Tx, q *database.Queries, bankID, actorID pgtype.UUID) (ReconciliationStateResult, error) {
			if replay, ok, err := stateReplay(ctx, q, command.RealmID, bankID, command.IdempotencyKey, func(state database.ShadowErpBankReconciliationState) bool {
				statementText, _ := numericText(state.BankStatementBalance)
				return state.Status == "OPEN" && state.RequestHash == requestHash && state.PeriodKey == command.PeriodKey && state.Currency == command.Currency && MustReconciliationMoney(statementText).Equal(statement)
			}); err != nil || ok {
				return replay, err
			}
			latest, latestErr := q.GetLatestBankReconciliationState(ctx, database.GetLatestBankReconciliationStateParams{BankAccountID: bankID, PeriodKey: command.PeriodKey})
			if revise {
				if latestErr != nil || latest.Status != "OPEN" {
					return ReconciliationStateResult{}, fmt.Errorf("an OPEN state is required for revision")
				}
			} else if latestErr == nil {
				return ReconciliationStateResult{}, fmt.Errorf("period already has a usable reconciliation state")
			} else if !errors.Is(latestErr, pgx.ErrNoRows) {
				return ReconciliationStateResult{}, latestErr
			}

			var predecessor database.ShadowErpBankReconciliationState
			var previousID pgtype.UUID
			var revision int32 = 1
			memberships := append([]ReconciliationEvidenceRef(nil), command.Memberships...)
			if revise {
				predecessor, previousID, revision = latest, latest.ID, latest.Revision+1
			} else {
				predecessor, err = q.GetLatestClosedBankReconciliationStateBefore(ctx, database.GetLatestClosedBankReconciliationStateBeforeParams{BankAccountID: bankID, PeriodKey: command.PeriodKey})
				if err != nil {
					return ReconciliationStateResult{}, fmt.Errorf("period requires a usable closed predecessor or migrated baseline: %w", err)
				}
				previousID = predecessor.ID
				expectedPredecessor, periodErr := previousReconciliationPeriod(command.PeriodKey)
				if periodErr != nil || predecessor.PeriodKey != expectedPredecessor {
					return ReconciliationStateResult{}, fmt.Errorf("period %s requires a usable CLOSED predecessor for %s", command.PeriodKey, expectedPredecessor)
				}
				carried, err := q.ListBankReconciliationStateMemberships(ctx, predecessor.ID)
				if err != nil {
					return ReconciliationStateResult{}, err
				}
				memberships = append(membershipModelsToCarryForward(carried), memberships...)
			}
			openingStatement, _ := numericText(predecessor.BankStatementBalance)
			openingBook, _ := numericText(predecessor.BookBankBalance)
			if revise {
				openingStatement, _ = numericText(predecessor.StatementOpeningBalance)
				openingBook, _ = numericText(predecessor.BookOpeningBalance)
			}
			if !MustReconciliationMoney(openingStatement).Equal(reportedOpening) {
				return ReconciliationStateResult{}, fmt.Errorf("reported statement opening %s does not match derived predecessor opening %s", reportedOpening.String(), openingStatement)
			}
			periodStart, _ := time.Parse("2006-01", command.PeriodKey)
			resolved, position, err := s.resolvePosition(ctx, tx, command.RealmID, bankID, command.Currency, command.PeriodKey, statement, memberships, MustReconciliationMoney(openingBook), periodStart)
			if err != nil {
				return ReconciliationStateResult{}, err
			}
			position.StatementOpeningBalance = MustReconciliationMoney(openingStatement)
			position.BookOpeningBalance = MustReconciliationMoney(openingBook)
			return s.insertState(ctx, q, stateInsertSpec{RealmID: command.RealmID, BankAccountID: bankID, PeriodKey: command.PeriodKey, Revision: revision, PreviousStateID: previousID, SupersedesStateID: supersedes, StateKind: stateKind, IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, Status: "OPEN", Currency: command.Currency, ActorID: actorID, Position: position, Memberships: resolved})
		})
}

type resolvedMembership struct {
	BankStatementLineID pgtype.UUID
	JournalLineID       pgtype.UUID
	Disposition         string
	MatchGroupID        pgtype.UUID
	CarryForward        bool
	ReviewReason        pgtype.Text
}

func (s *BankReconciliationService) resolvePosition(ctx context.Context, tx pgx.Tx, realmID string, bankID pgtype.UUID, currency, periodKey string, statement ReconciliationMoney, refs []ReconciliationEvidenceRef, bookOpening ReconciliationMoney, movementStart time.Time) ([]resolvedMembership, ClosingPosition, error) {
	periodEnd, err := reconciliationPeriodEnd(periodKey)
	if err != nil {
		return nil, ClosingPosition{}, err
	}
	book := bookOpening
	if !movementStart.IsZero() {
		var movementNumeric pgtype.Numeric
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(jl.debit - jl.credit), 0)::numeric(24,4)
			FROM shadow_erp.journal_lines jl
			JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
			JOIN shadow_erp.accounts a ON a.id = jl.account_id
			JOIN shadow_erp.bank_accounts ba ON ba.id = $2
			WHERE je.realm_id = $1 AND a.realm_id = $1 AND a.account_code = ba.ledger_account_code
			  AND je.currency = $3 AND je.entry_date >= $4 AND je.entry_date <= $5`, realmID, bankID, currency, movementStart, periodEnd).Scan(&movementNumeric); err != nil {
			return nil, ClosingPosition{}, err
		}
		movementText, err := numericText(movementNumeric)
		if err != nil {
			return nil, ClosingPosition{}, err
		}
		book = bookOpening.Add(MustReconciliationMoney(movementText))
	}
	inflows, outflows, bankNet := MustReconciliationMoney("0"), MustReconciliationMoney("0"), MustReconciliationMoney("0")
	resolved := make([]resolvedMembership, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	reconciledGroupSeen := make(map[string]int)
	reconciledGroupExpected := make(map[string]int)
	for _, ref := range refs {
		domainRef := StateMembershipRef{BankStatementLineID: ref.BankStatementLineID, JournalLineID: ref.JournalLineID, Disposition: ref.Disposition, CarryForward: ref.CarryForward, MatchGroupID: ref.MatchGroupID, ReviewReason: ref.ReviewReason}
		key, err := domainRef.CanonicalKey()
		if err != nil {
			return nil, ClosingPosition{}, err
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, ClosingPosition{}, fmt.Errorf("duplicate state membership %s", key)
		}
		seen[key] = struct{}{}
		item := resolvedMembership{Disposition: ref.Disposition, CarryForward: ref.CarryForward, ReviewReason: optionalText(ref.ReviewReason)}
		if ref.MatchGroupID != "" {
			item.MatchGroupID, err = postingUUID(ref.MatchGroupID)
			if err != nil {
				return nil, ClosingPosition{}, err
			}
		}
		unresolved := ref.Disposition != "RECONCILED"
		if ref.BankStatementLineID != "" {
			item.BankStatementLineID, err = postingUUID(ref.BankStatementLineID)
			if err != nil {
				return nil, ClosingPosition{}, err
			}
			var lineRealm, lineCurrency, direction string
			var lineBank pgtype.UUID
			var amount pgtype.Numeric
			if err := tx.QueryRow(ctx, `SELECT realm_id, bank_account_id, currency, direction, amount FROM shadow_erp.bank_statement_lines WHERE id = $1`, item.BankStatementLineID).Scan(&lineRealm, &lineBank, &lineCurrency, &direction, &amount); err != nil {
				return nil, ClosingPosition{}, err
			}
			if lineRealm != realmID || lineBank != bankID || lineCurrency != currency {
				return nil, ClosingPosition{}, fmt.Errorf("bank membership evidence is outside reconciliation scope")
			}
			if unresolved {
				value, _ := numericText(amount)
				money := MustReconciliationMoney(value)
				if direction == "INFLOW" {
					bankNet = bankNet.Add(money)
				} else {
					bankNet = bankNet.Sub(money)
				}
			}
		} else {
			item.JournalLineID, err = postingUUID(ref.JournalLineID)
			if err != nil {
				return nil, ClosingPosition{}, err
			}
			var lineRealm, lineCurrency, accountCode, ledgerCode string
			var debit, credit pgtype.Numeric
			if err := tx.QueryRow(ctx, `SELECT je.realm_id, je.currency, a.account_code, ba.ledger_account_code, jl.debit, jl.credit
				FROM shadow_erp.journal_lines jl JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id
				JOIN shadow_erp.accounts a ON a.id = jl.account_id JOIN shadow_erp.bank_accounts ba ON ba.id = $2
				WHERE jl.id = $1`, item.JournalLineID, bankID).Scan(&lineRealm, &lineCurrency, &accountCode, &ledgerCode, &debit, &credit); err != nil {
				return nil, ClosingPosition{}, err
			}
			if lineRealm != realmID || lineCurrency != currency || accountCode != ledgerCode {
				return nil, ClosingPosition{}, fmt.Errorf("journal membership is not a bank-ledger line in reconciliation scope")
			}
			if unresolved {
				debitText, _ := numericText(debit)
				creditText, _ := numericText(credit)
				inflows = inflows.Add(MustReconciliationMoney(debitText))
				outflows = outflows.Add(MustReconciliationMoney(creditText))
			}
		}
		if item.MatchGroupID.Valid {
			var groupStatus string
			var memberExists bool
			var memberCount int
			if err := tx.QueryRow(ctx, `SELECT g.status,
				EXISTS (SELECT 1 FROM shadow_erp.reconciliation_match_members m
					WHERE m.match_group_id = g.id
					  AND (($4::uuid IS NOT NULL AND m.bank_statement_line_id = $4)
					    OR ($5::uuid IS NOT NULL AND m.journal_line_id = $5))),
				(SELECT COUNT(*) FROM shadow_erp.reconciliation_match_members m WHERE m.match_group_id = g.id)
				FROM shadow_erp.reconciliation_match_groups g
				WHERE g.id = $1 AND g.realm_id = $2 AND g.bank_account_id = $3`,
				item.MatchGroupID, realmID, bankID, item.BankStatementLineID, item.JournalLineID,
			).Scan(&groupStatus, &memberExists, &memberCount); err != nil {
				return nil, ClosingPosition{}, fmt.Errorf("resolve reconciliation match evidence: %w", err)
			}
			if !memberExists {
				return nil, ClosingPosition{}, fmt.Errorf("state membership does not belong to its referenced match group")
			}
			if ref.Disposition == "RECONCILED" {
				if groupStatus != "CONFIRMED" {
					return nil, ClosingPosition{}, fmt.Errorf("RECONCILED membership requires a confirmed match group")
				}
				groupKey := uuidString(item.MatchGroupID)
				reconciledGroupSeen[groupKey]++
				reconciledGroupExpected[groupKey] = memberCount
			}
		} else if ref.Disposition == "RECONCILED" {
			return nil, ClosingPosition{}, fmt.Errorf("RECONCILED membership requires a match group")
		}
		resolved = append(resolved, item)
	}
	for groupID, expectedCount := range reconciledGroupExpected {
		if reconciledGroupSeen[groupID] != expectedCount {
			return nil, ClosingPosition{}, fmt.Errorf("confirmed match group %s must be included with all %d canonical members", groupID, expectedCount)
		}
	}
	position := CalculateClosingPosition(book, inflows, outflows, bankNet, statement)
	return resolved, position, nil
}

type stateInsertSpec struct {
	RealmID, PeriodKey, StateKind, IdempotencyKey, RequestHash, Status, Currency string
	BankAccountID, PreviousStateID, SupersedesStateID, ActorID                   pgtype.UUID
	Revision                                                                     int32
	Position                                                                     ClosingPosition
	Memberships                                                                  []resolvedMembership
}

func (s *BankReconciliationService) insertState(ctx context.Context, q *database.Queries, spec stateInsertSpec) (ReconciliationStateResult, error) {
	domainMemberships := make([]StateMembershipRef, 0, len(spec.Memberships))
	for _, member := range spec.Memberships {
		domainMemberships = append(domainMemberships, StateMembershipRef{BankStatementLineID: uuidString(member.BankStatementLineID), JournalLineID: uuidString(member.JournalLineID), Disposition: member.Disposition, CarryForward: member.CarryForward, MatchGroupID: uuidString(member.MatchGroupID), ReviewReason: member.ReviewReason.String})
	}
	snapshot, err := BuildStateSnapshot(StateSnapshotInput{RealmID: spec.RealmID, BankAccountID: uuidString(spec.BankAccountID), PeriodKey: spec.PeriodKey, Revision: spec.Revision, PreviousStateID: uuidString(spec.PreviousStateID), SupersedesStateID: uuidString(spec.SupersedesStateID), StateKind: spec.StateKind, IdempotencyKey: spec.IdempotencyKey, RequestHash: spec.RequestHash, Status: spec.Status, Currency: spec.Currency, CreatedBy: uuidString(spec.ActorID), Position: spec.Position, Memberships: domainMemberships})
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	if existing, err := q.GetBankReconciliationStateByIdempotencyKey(ctx, database.GetBankReconciliationStateByIdempotencyKeyParams{RealmID: spec.RealmID, BankAccountID: spec.BankAccountID, IdempotencyKey: spec.IdempotencyKey}); err == nil {
		if existing.StateHash != snapshot.StateHash {
			return ReconciliationStateResult{}, fmt.Errorf("reconciliation idempotency key was reused with different state content")
		}
		result := stateModelResult(existing)
		result.IdempotentReplay = true
		return result, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReconciliationStateResult{}, err
	}
	toNumeric := func(value ReconciliationMoney) pgtype.Numeric {
		numeric, _ := postingNumeric(value.String())
		return numeric
	}
	state, err := q.CreateBankReconciliationState(ctx, database.CreateBankReconciliationStateParams{
		RealmID: spec.RealmID, BankAccountID: spec.BankAccountID, PeriodKey: spec.PeriodKey,
		Revision: spec.Revision, PreviousStateID: spec.PreviousStateID, SupersedesStateID: spec.SupersedesStateID,
		StateKind: spec.StateKind, IdempotencyKey: spec.IdempotencyKey, RequestHash: spec.RequestHash, Status: spec.Status, Currency: spec.Currency,
		StatementOpeningBalance: toNumeric(spec.Position.StatementOpeningBalance), BookOpeningBalance: toNumeric(spec.Position.BookOpeningBalance),
		BankStatementBalance: toNumeric(spec.Position.StatementBalance), BookBankBalance: toNumeric(spec.Position.BookBankBalance),
		OutstandingBookInflows: toNumeric(spec.Position.OutstandingBookInflows), OutstandingBookOutflows: toNumeric(spec.Position.OutstandingBookOutflows),
		OutstandingBankNet: toNumeric(spec.Position.OutstandingBankNet), ExpectedBankBalance: toNumeric(spec.Position.ExpectedBankBalance),
		Difference: toNumeric(spec.Position.Difference), StateHash: snapshot.StateHash, CreatedBy: spec.ActorID,
	})
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	for _, member := range spec.Memberships {
		if _, err := q.CreateBankReconciliationStateMembership(ctx, database.CreateBankReconciliationStateMembershipParams{StateID: state.ID, BankStatementLineID: member.BankStatementLineID, JournalLineID: member.JournalLineID, Disposition: member.Disposition, ReconciliationMatchGroupID: member.MatchGroupID, CarryForward: member.CarryForward, ReviewReason: member.ReviewReason}); err != nil {
			return ReconciliationStateResult{}, err
		}
	}
	return stateModelResult(state), nil
}

func (s *BankReconciliationService) withLockedAccount(ctx context.Context, actor ReconciliationActor, realmID, bankAccountID, idempotencyKey string, fn func(context.Context, pgx.Tx, *database.Queries, pgtype.UUID, pgtype.UUID) (ReconciliationStateResult, error)) (ReconciliationStateResult, error) {
	if s == nil || s.pool == nil || realmID == "" || idempotencyKey == "" {
		return ReconciliationStateResult{}, fmt.Errorf("reconciliation database, realm, and idempotency key are required")
	}
	bankID, err := postingUUID(bankAccountID)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	actorID, err := postingUUID(actor.ActorUserID)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	entityID, err := postingUUID(actor.EntityID)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1 || ':' || $2::text, 0))", realmID, bankAccountID); err != nil {
		return ReconciliationStateResult{}, err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockContext, "SELECT pg_advisory_unlock(hashtextextended($1 || ':' || $2::text, 0))", realmID, bankAccountID)
	}()
	// The session lock is acquired before BEGIN so a waiter takes its
	// SERIALIZABLE snapshot only after the preceding state commit is visible.
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	defer tx.Rollback(ctx)
	q := database.New(tx)
	if _, err := q.GetActiveUserInEntity(ctx, database.GetActiveUserInEntityParams{ID: actorID, EntityID: entityID}); err != nil {
		return ReconciliationStateResult{}, fmt.Errorf("actor is not an active user in the entity: %w", err)
	}
	var ownsRealm bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM toro_core.erp_connections WHERE entity_id = $1 AND realm_id = $2)", entityID, realmID).Scan(&ownsRealm); err != nil || !ownsRealm {
		return ReconciliationStateResult{}, fmt.Errorf("reconciliation realm does not belong to the actor entity")
	}
	var bankRealm string
	if err := tx.QueryRow(ctx, "SELECT realm_id FROM shadow_erp.bank_accounts WHERE id = $1", bankID).Scan(&bankRealm); err != nil || bankRealm != realmID {
		return ReconciliationStateResult{}, fmt.Errorf("bank account does not belong to realm")
	}
	result, err := fn(ctx, tx, q, bankID, actorID)
	if err != nil {
		return ReconciliationStateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReconciliationStateResult{}, err
	}
	return result, nil
}

func reconciliationPeriodEnd(periodKey string) (time.Time, error) {
	start, err := time.Parse("2006-01", periodKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid period key %q", periodKey)
	}
	return start.AddDate(0, 1, -1), nil
}

func previousReconciliationPeriod(periodKey string) (string, error) {
	start, err := time.Parse("2006-01", periodKey)
	if err != nil {
		return "", fmt.Errorf("invalid period key %q", periodKey)
	}
	return start.AddDate(0, -1, 0).Format("2006-01"), nil
}

func stateModelResult(state database.ShadowErpBankReconciliationState) ReconciliationStateResult {
	return ReconciliationStateResult{StateID: uuidString(state.ID), StateHash: state.StateHash, Status: state.Status, StateKind: state.StateKind, PeriodKey: state.PeriodKey, Revision: state.Revision, PreviousStateID: uuidString(state.PreviousStateID)}
}

func stateReplay(ctx context.Context, q *database.Queries, realmID string, bankID pgtype.UUID, idempotencyKey string, validate func(database.ShadowErpBankReconciliationState) bool) (ReconciliationStateResult, bool, error) {
	state, err := q.GetBankReconciliationStateByIdempotencyKey(ctx, database.GetBankReconciliationStateByIdempotencyKeyParams{RealmID: realmID, BankAccountID: bankID, IdempotencyKey: idempotencyKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReconciliationStateResult{}, false, nil
	}
	if err != nil {
		return ReconciliationStateResult{}, false, err
	}
	if !validate(state) {
		return ReconciliationStateResult{}, false, fmt.Errorf("reconciliation idempotency key was reused with different command content")
	}
	result := stateModelResult(state)
	result.IdempotentReplay = true
	return result, true, nil
}

func reconciliationRequestHash(header []string, refs []ReconciliationEvidenceRef) (string, error) {
	membershipKeys := make([]string, 0, len(refs))
	for _, ref := range refs {
		key, err := (StateMembershipRef{
			BankStatementLineID: ref.BankStatementLineID,
			JournalLineID:       ref.JournalLineID,
			Disposition:         ref.Disposition,
			CarryForward:        ref.CarryForward,
			MatchGroupID:        ref.MatchGroupID,
			ReviewReason:        ref.ReviewReason,
		}).CanonicalKey()
		if err != nil {
			return "", err
		}
		membershipKeys = append(membershipKeys, key)
	}
	return StateHash(header, membershipKeys), nil
}

func positionFromState(state database.ShadowErpBankReconciliationState) (ClosingPosition, error) {
	parse := func(value pgtype.Numeric) (ReconciliationMoney, error) {
		text, err := numericText(value)
		if err != nil {
			return ReconciliationMoney{}, err
		}
		return NewReconciliationMoney(text)
	}
	var result ClosingPosition
	var err error
	if result.StatementOpeningBalance, err = parse(state.StatementOpeningBalance); err != nil {
		return result, err
	}
	if result.BookOpeningBalance, err = parse(state.BookOpeningBalance); err != nil {
		return result, err
	}
	if result.StatementBalance, err = parse(state.BankStatementBalance); err != nil {
		return result, err
	}
	if result.BookBankBalance, err = parse(state.BookBankBalance); err != nil {
		return result, err
	}
	if result.OutstandingBookInflows, err = parse(state.OutstandingBookInflows); err != nil {
		return result, err
	}
	if result.OutstandingBookOutflows, err = parse(state.OutstandingBookOutflows); err != nil {
		return result, err
	}
	if result.OutstandingBankNet, err = parse(state.OutstandingBankNet); err != nil {
		return result, err
	}
	if result.ExpectedBankBalance, err = parse(state.ExpectedBankBalance); err != nil {
		return result, err
	}
	if result.Difference, err = parse(state.Difference); err != nil {
		return result, err
	}
	return result, nil
}

func membershipModelsToResolved(models []database.ShadowErpBankReconciliationStateMembership) []resolvedMembership {
	result := make([]resolvedMembership, 0, len(models))
	for _, model := range models {
		result = append(result, resolvedMembership{BankStatementLineID: model.BankStatementLineID, JournalLineID: model.JournalLineID, Disposition: model.Disposition, MatchGroupID: model.ReconciliationMatchGroupID, CarryForward: model.CarryForward, ReviewReason: model.ReviewReason})
	}
	return result
}

func membershipModelsForClose(models []database.ShadowErpBankReconciliationStateMembership) []resolvedMembership {
	result := membershipModelsToResolved(models)
	for index := range result {
		if result[index].Disposition != "RECONCILED" {
			result[index].Disposition = "CARRIED_FORWARD"
			result[index].CarryForward = true
		}
	}
	return result
}

func membershipModelsToCarryForward(models []database.ShadowErpBankReconciliationStateMembership) []ReconciliationEvidenceRef {
	result := make([]ReconciliationEvidenceRef, 0, len(models))
	for _, model := range models {
		if model.Disposition == "RECONCILED" {
			continue
		}
		result = append(result, ReconciliationEvidenceRef{BankStatementLineID: uuidString(model.BankStatementLineID), JournalLineID: uuidString(model.JournalLineID), Disposition: "CARRIED_FORWARD", MatchGroupID: uuidString(model.ReconciliationMatchGroupID), CarryForward: true, ReviewReason: model.ReviewReason.String})
	}
	return result
}

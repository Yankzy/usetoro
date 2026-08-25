package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrJournalPostingConflict = errors.New("journal posting idempotency conflict")

type JournalPostingService struct{ pool *pgxpool.Pool }

func NewJournalPostingService(pool *pgxpool.Pool) *JournalPostingService {
	return &JournalPostingService{pool: pool}
}

type PostApprovedProposalCommand struct {
	EntityID             string       `json:"entity_id"`
	ActorUserID          string       `json:"actor_user_id"`
	ExpectedProposalHash string       `json:"expected_proposal_hash"`
	Output               Stage2Output `json:"stage2_output"`
}

type JournalPostingResult struct {
	JournalEntryID   string   `json:"journal_entry_id"`
	JournalLineIDs   []string `json:"journal_line_ids"`
	PieceReference   string   `json:"piece_reference"`
	ProposalHash     string   `json:"proposal_hash"`
	IdempotentReplay bool     `json:"idempotent_replay"`
}

func (s *JournalPostingService) PostApprovedProposal(ctx context.Context, command PostApprovedProposalCommand) (JournalPostingResult, error) {
	if s == nil || s.pool == nil {
		return JournalPostingResult{}, fmt.Errorf("journal posting database is unavailable")
	}
	if err := command.Output.Validate(); err != nil {
		return JournalPostingResult{}, err
	}
	if command.Output.Outcome != Stage2ProposedTreatment {
		return JournalPostingResult{}, fmt.Errorf("only an accounting proposal can be posted")
	}
	if command.EntityID == "" || command.EntityID != command.Output.Context.EntityID {
		return JournalPostingResult{}, fmt.Errorf("posting entity does not match proposal context")
	}
	actualHash, err := command.Output.Hash()
	if err != nil {
		return JournalPostingResult{}, err
	}
	if command.ExpectedProposalHash == "" || actualHash != command.ExpectedProposalHash {
		return JournalPostingResult{}, fmt.Errorf("proposal hash changed after review")
	}
	actorID, err := postingUUID(command.ActorUserID)
	if err != nil {
		return JournalPostingResult{}, fmt.Errorf("actor user: %w", err)
	}
	entityID, err := postingUUID(command.EntityID)
	if err != nil {
		return JournalPostingResult{}, fmt.Errorf("entity: %w", err)
	}
	stagingID, err := postingUUID(command.Output.BankLine.StagingTransactionID)
	if err != nil {
		return JournalPostingResult{}, err
	}
	bankLineID, err := postingUUID(command.Output.BankLine.BankStatementLineID)
	if err != nil {
		return JournalPostingResult{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return JournalPostingResult{}, err
	}
	defer tx.Rollback(ctx)
	q := database.New(tx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", command.Output.BankLine.BankStatementLineID); err != nil {
		return JournalPostingResult{}, err
	}
	if _, err := q.GetActiveUserInEntity(ctx, database.GetActiveUserInEntityParams{ID: actorID, EntityID: entityID}); err != nil {
		return JournalPostingResult{}, fmt.Errorf("actor is not an active user in the proposal entity: %w", err)
	}
	var ownsRealm bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM toro_core.erp_connections WHERE entity_id = $1 AND realm_id = $2)", entityID, command.Output.Context.RealmID).Scan(&ownsRealm); err != nil || !ownsRealm {
		return JournalPostingResult{}, fmt.Errorf("proposal realm does not belong to the actor entity")
	}
	if existing, err := q.GetJournalEntryByBankStatementLine(ctx, bankLineID); err == nil {
		if !existing.SourceTreatmentHash.Valid || existing.SourceTreatmentHash.String != actualHash {
			return JournalPostingResult{}, ErrJournalPostingConflict
		}
		lines, listErr := q.ListJournalLinesByEntry(ctx, existing.ID)
		if listErr != nil {
			return JournalPostingResult{}, listErr
		}
		result := JournalPostingResult{JournalEntryID: uuidString(existing.ID), PieceReference: existing.PieceReference, ProposalHash: actualHash, IdempotentReplay: true}
		for _, line := range lines {
			result.JournalLineIDs = append(result.JournalLineIDs, uuidString(line.ID))
		}
		if err := tx.Commit(ctx); err != nil {
			return JournalPostingResult{}, err
		}
		return result, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return JournalPostingResult{}, err
	}

	encoded, err := json.Marshal(command.Output)
	if err != nil {
		return JournalPostingResult{}, err
	}
	if _, err := q.ApproveStage2Proposal(ctx, database.ApproveStage2ProposalParams{
		ApprovedPayload: encoded, ExpectedHash: actualHash, ActorUserID: actorID, StagingTransactionID: stagingID,
	}); err != nil {
		return JournalPostingResult{}, fmt.Errorf("proposal is stale, not reviewable, or already rejected: %w", err)
	}

	bankLine, err := q.GetBankStatementLineByStagingTransaction(ctx, stagingID)
	if err != nil || bankLine.ID != bankLineID || bankLine.RealmID != command.Output.Context.RealmID || uuidString(bankLine.BankAccountID) != command.Output.BankLine.BankAccountID {
		return JournalPostingResult{}, fmt.Errorf("canonical bank-line provenance does not match the approved proposal")
	}
	bankAmount, err := numericText(bankLine.Amount)
	if err != nil || MustReconciliationMoney(bankAmount).String() != MustReconciliationMoney(command.Output.BankLine.Amount).String() || bankLine.Direction != command.Output.BankLine.Direction || bankLine.Currency != command.Output.BankLine.Currency {
		return JournalPostingResult{}, fmt.Errorf("canonical bank-line financial evidence changed")
	}
	if !bankLine.OperationDate.Valid || bankLine.OperationDate.Time.Format("2006-01-02") != command.Output.Treatment.EntryDate {
		return JournalPostingResult{}, fmt.Errorf("journal date must be the canonical bank operation date")
	}

	journal, err := q.GetBankJournalForRealm(ctx, command.Output.Context.RealmID)
	if err != nil {
		return JournalPostingResult{}, fmt.Errorf("resolve bank journal: %w", err)
	}
	entryDate := pgtype.Date{Time: bankLine.OperationDate.Time, Valid: true}
	now := time.Now().UTC()
	pieceReference := canonicalBankPieceReference(bankLine.OperationDate.Time, command.Output.BankLine.BankStatementLineID)
	entry, err := q.CreateJournalEntry(ctx, database.CreateJournalEntryParams{
		RealmID: command.Output.Context.RealmID, JournalID: journal.ID, EntryDate: entryDate,
		Currency: command.Output.BankLine.Currency, PieceReference: pieceReference, Label: command.Output.Treatment.Label,
		SourceBankStatementLineID: bankLineID, SourceStagingTransactionID: stagingID,
		SourceTreatmentPayload: encoded, SourceTreatmentHash: pgtype.Text{String: actualHash, Valid: true},
		SourceWorkflowTraceID: optionalText(command.Output.Context.WorkflowTraceID), ApprovedBy: actorID,
		ApprovedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return JournalPostingResult{}, err
	}

	result := JournalPostingResult{JournalEntryID: uuidString(entry.ID), PieceReference: pieceReference, ProposalHash: actualHash}
	for _, proposed := range command.Output.Treatment.Lines {
		accountID, parseErr := postingUUID(proposed.AccountID)
		if parseErr != nil {
			return JournalPostingResult{}, parseErr
		}
		account, resolveErr := q.GetAccountByRealmAndCode(ctx, database.GetAccountByRealmAndCodeParams{
			RealmID: command.Output.Context.RealmID, AccountCode: pgtype.Text{String: proposed.AccountCode, Valid: true},
		})
		if resolveErr != nil || account.ID != accountID {
			return JournalPostingResult{}, fmt.Errorf("journal account %s/%s is not an active same-realm account", proposed.AccountID, proposed.AccountCode)
		}
		debit, parseErr := postingNumeric(proposed.Debit)
		if parseErr != nil {
			return JournalPostingResult{}, parseErr
		}
		credit, parseErr := postingNumeric(proposed.Credit)
		if parseErr != nil {
			return JournalPostingResult{}, parseErr
		}
		line, createErr := q.CreateJournalLine(ctx, database.CreateJournalLineParams{
			JournalEntryID: entry.ID, LineIndex: int32(proposed.LineIndex), AccountID: account.ID,
			AuxiliaryAccountCode: optionalText(proposed.AuxiliaryAccountCode), Label: proposed.Label,
			Debit: debit, Credit: credit,
		})
		if createErr != nil {
			return JournalPostingResult{}, createErr
		}
		result.JournalLineIDs = append(result.JournalLineIDs, uuidString(line.ID))
	}
	if err := tx.Commit(ctx); err != nil {
		return JournalPostingResult{}, fmt.Errorf("commit balanced journal: %w", err)
	}
	return result, nil
}

func canonicalBankPieceReference(date time.Time, bankLineID string) string {
	compact := strings.ReplaceAll(bankLineID, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	return fmt.Sprintf("BR-%s-%s", date.Format("20060102"), strings.ToUpper(compact))
}

func postingUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", value, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func uuidString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func postingNumeric(value string) (pgtype.Numeric, error) {
	money, err := NewReconciliationMoney(value)
	if err != nil {
		return pgtype.Numeric{}, err
	}
	var numeric pgtype.Numeric
	if err := numeric.Scan(money.String()); err != nil {
		return pgtype.Numeric{}, err
	}
	return numeric, nil
}

func numericText(value pgtype.Numeric) (string, error) {
	driverValue, err := value.Value()
	if err != nil {
		return "", err
	}
	text, ok := driverValue.(string)
	if !ok {
		return "", fmt.Errorf("unexpected numeric value %T", driverValue)
	}
	return text, nil
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

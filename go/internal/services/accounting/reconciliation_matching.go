package accounting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReconciliationMatchingService struct{ pool *pgxpool.Pool }

func NewReconciliationMatchingService(pool *pgxpool.Pool) *ReconciliationMatchingService {
	return &ReconciliationMatchingService{pool: pool}
}

type GenerateOneToOneCandidatesCommand struct {
	RealmID       string    `json:"realm_id"`
	BankAccountID string    `json:"bank_account_id"`
	AsOf          time.Time `json:"as_of"`
}

type CandidateGenerationResult struct {
	CandidateGroupIDs []string `json:"candidate_group_ids"`
	AmbiguousCount    int      `json:"ambiguous_count"`
	AgedReviewCount   int      `json:"aged_review_count"`
}

type CreateManualGroupedMatchCommand struct {
	ReconciliationActor
	RealmID              string   `json:"realm_id"`
	BankAccountID        string   `json:"bank_account_id"`
	Currency             string   `json:"currency"`
	ItemType             string   `json:"item_type"`
	BankStatementLineIDs []string `json:"bank_statement_line_ids"`
	JournalLineIDs       []string `json:"journal_line_ids"`
	IdempotencyKey       string   `json:"idempotency_key"`
}

type ConfirmMatchCandidateCommand struct {
	ReconciliationActor
	MatchGroupID string `json:"match_group_id"`
}

type ResolveReconciliationReviewCommand struct {
	ReconciliationActor
	ReviewQueueItemID string `json:"review_queue_item_id"`
	Resolution        string `json:"resolution"`
}

type MatchResult struct {
	MatchGroupID     string `json:"match_group_id"`
	Status           string `json:"status"`
	IdempotentReplay bool   `json:"idempotent_replay"`
}

type matchingPolicySnapshot struct {
	ItemType    string `json:"item_type"`
	MaxDays     int32  `json:"max_days"`
	AgeingDays  int32  `json:"ageing_days"`
	SourceRealm string `json:"source_realm"`
	Version     string `json:"version"`
}

type matchingBookMovement struct {
	ID, Currency, Direction, Label string
	Amount                         ReconciliationMoney
	Date                           time.Time
	SourceBankLineID               pgtype.UUID
}

func (s *ReconciliationMatchingService) GenerateOneToOneCandidates(ctx context.Context, command GenerateOneToOneCandidatesCommand) (CandidateGenerationResult, error) {
	if s == nil || s.pool == nil || command.RealmID == "" {
		return CandidateGenerationResult{}, fmt.Errorf("matching database and realm are required")
	}
	bankID, err := postingUUID(command.BankAccountID)
	if err != nil {
		return CandidateGenerationResult{}, err
	}
	if command.AsOf.IsZero() {
		command.AsOf = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CandidateGenerationResult{}, err
	}
	defer tx.Rollback(ctx)
	q := database.New(tx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1 || ':' || $2, 0))", command.RealmID, command.BankAccountID); err != nil {
		return CandidateGenerationResult{}, err
	}
	var ownsBank bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM shadow_erp.bank_accounts WHERE id = $1 AND realm_id = $2)", bankID, command.RealmID).Scan(&ownsBank); err != nil || !ownsBank {
		return CandidateGenerationResult{}, fmt.Errorf("matching bank account does not belong to realm")
	}
	bankLines, err := q.ListUnmatchedBankStatementLines(ctx, database.ListUnmatchedBankStatementLinesParams{RealmID: command.RealmID, BankAccountID: bankID})
	if err != nil {
		return CandidateGenerationResult{}, err
	}
	bookLines, err := q.ListUnmatchedBankJournalLines(ctx, database.ListUnmatchedBankJournalLinesParams{RealmID: command.RealmID, ID: bankID})
	if err != nil {
		return CandidateGenerationResult{}, err
	}
	books := make([]matchingBookMovement, 0, len(bookLines))
	for _, line := range bookLines {
		debitText, _ := numericText(line.Debit)
		creditText, _ := numericText(line.Credit)
		debit, credit := MustReconciliationMoney(debitText), MustReconciliationMoney(creditText)
		movement := matchingBookMovement{ID: uuidString(line.ID), Currency: line.Currency, Label: line.Label, Date: line.EntryDate.Time, SourceBankLineID: line.SourceBankStatementLineID}
		if !debit.IsZero() {
			movement.Direction, movement.Amount = "INFLOW", debit
		} else {
			movement.Direction, movement.Amount = "OUTFLOW", credit
		}
		books = append(books, movement)
	}

	result := CandidateGenerationResult{}
	type createdCandidate struct {
		key        string
		groupID    pgtype.UUID
		bankLineID pgtype.UUID
		policyJSON []byte
	}
	bookCandidates := make(map[string][]createdCandidate)
	for _, bank := range bankLines {
		itemType := inferTreasuryItemType(bank.Description)
		policyRow, err := q.GetReconciliationMatchWindow(ctx, database.GetReconciliationMatchWindowParams{RealmID: command.RealmID, ItemType: itemType})
		if err != nil {
			return result, err
		}
		policy := matchingPolicySnapshot{ItemType: itemType, MaxDays: policyRow.MaxDays, AgeingDays: policyRow.AgeingDays, SourceRealm: policyRow.RealmID, Version: policyRow.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)}
		policyJSON, _ := json.Marshal(policy)
		bankDate := bank.OperationDate.Time
		if !bank.OperationDate.Valid {
			bankDate = bank.ValueDate.Time
		}
		amountText, _ := numericText(bank.Amount)
		bankMovement := ReconciliationMovement{ID: uuidString(bank.ID), Amount: MustReconciliationMoney(amountText), Currency: bank.Currency, Direction: bank.Direction, Date: bankDate}
		type eligibleMatch struct {
			book  matchingBookMovement
			score int
		}
		eligible := make([]eligibleMatch, 0)
		for _, book := range books {
			if !IsEligibleOneToOneMatch(bankMovement, ReconciliationMovement{ID: book.ID, Amount: book.Amount, Currency: book.Currency, Direction: book.Direction, Date: book.Date}, int(policy.MaxDays)) {
				continue
			}
			eligible = append(eligible, eligibleMatch{book: book, score: rankMatchEvidence(bank, book)})
		}
		sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].score > eligible[j].score })
		for _, candidate := range eligible {
			key := stableMatchKey("auto-v1", uuidString(bank.ID), candidate.book.ID, string(policyJSON))
			group, replay, err := createCandidateGroup(ctx, q, database.CreateReconciliationMatchGroupParams{
				IdempotencyKey: key, RequestHash: key, RealmID: command.RealmID, BankAccountID: bankID, Currency: bank.Currency,
				ItemType: itemType, Direction: bank.Direction, Amount: bank.Amount,
				BankReferenceDate: pgtype.Date{Time: bankDate, Valid: true}, BookReferenceDate: pgtype.Date{Time: candidate.book.Date, Valid: true},
				PolicyMaxDays: policy.MaxDays, PolicySnapshot: policyJSON,
				EvidenceRanking: mustJSON(map[string]interface{}{"score": candidate.score, "ambiguous_candidate_count": len(eligible)}),
				Status:          "CANDIDATE", CreatedByKind: "SYSTEM",
			})
			if err != nil {
				return result, err
			}
			if !replay {
				if _, err := q.CreateReconciliationMatchMember(ctx, database.CreateReconciliationMatchMemberParams{MatchGroupID: group.ID, BankStatementLineID: bank.ID}); err != nil {
					return result, err
				}
				bookID, _ := postingUUID(candidate.book.ID)
				if _, err := q.CreateReconciliationMatchMember(ctx, database.CreateReconciliationMatchMemberParams{MatchGroupID: group.ID, JournalLineID: bookID}); err != nil {
					return result, err
				}
			}
			result.CandidateGroupIDs = append(result.CandidateGroupIDs, uuidString(group.ID))
			bookCandidates[candidate.book.ID] = append(bookCandidates[candidate.book.ID], createdCandidate{key: key, groupID: group.ID, bankLineID: bank.ID, policyJSON: policyJSON})
			if len(eligible) > 1 {
				result.AmbiguousCount++
				if _, err := q.CreateReconciliationReviewQueueItem(ctx, database.CreateReconciliationReviewQueueItemParams{DedupeKey: "ambiguous:" + key, RealmID: command.RealmID, BankAccountID: bankID, BankStatementLineID: bank.ID, MatchGroupID: group.ID, Reason: "AMBIGUOUS_MATCH", PolicySnapshot: policyJSON}); err != nil {
					return result, err
				}
			}
		}
		if len(eligible) == 0 && bankDate.AddDate(0, 0, int(policy.AgeingDays)).Before(command.AsOf) {
			if _, err := q.CreateReconciliationReviewQueueItem(ctx, database.CreateReconciliationReviewQueueItemParams{DedupeKey: stableMatchKey("aged-v1", uuidString(bank.ID), policy.Version), RealmID: command.RealmID, BankAccountID: bankID, BankStatementLineID: bank.ID, Reason: "AGED_UNMATCHED", PolicySnapshot: policyJSON}); err != nil {
				return result, err
			}
			result.AgedReviewCount++
		}
	}
	for _, candidates := range bookCandidates {
		if len(candidates) < 2 {
			continue
		}
		for _, candidate := range candidates {
			if _, err := q.CreateReconciliationReviewQueueItem(ctx, database.CreateReconciliationReviewQueueItemParams{DedupeKey: "ambiguous-book:" + candidate.key, RealmID: command.RealmID, BankAccountID: bankID, BankStatementLineID: candidate.bankLineID, MatchGroupID: candidate.groupID, Reason: "AMBIGUOUS_MATCH", PolicySnapshot: candidate.policyJSON}); err != nil {
				return result, err
			}
			result.AmbiguousCount++
		}
	}
	for _, book := range books {
		if len(bookCandidates[book.ID]) != 0 {
			continue
		}
		itemType := inferTreasuryItemType(book.Label)
		policyRow, err := q.GetReconciliationMatchWindow(ctx, database.GetReconciliationMatchWindowParams{RealmID: command.RealmID, ItemType: itemType})
		if err != nil {
			return result, err
		}
		if !book.Date.AddDate(0, 0, int(policyRow.AgeingDays)).Before(command.AsOf) {
			continue
		}
		policy := matchingPolicySnapshot{ItemType: itemType, MaxDays: policyRow.MaxDays, AgeingDays: policyRow.AgeingDays, SourceRealm: policyRow.RealmID, Version: policyRow.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)}
		policyJSON, _ := json.Marshal(policy)
		journalID, _ := postingUUID(book.ID)
		if _, err := q.CreateReconciliationReviewQueueItem(ctx, database.CreateReconciliationReviewQueueItemParams{DedupeKey: stableMatchKey("aged-book-v1", book.ID, policy.Version), RealmID: command.RealmID, BankAccountID: bankID, JournalLineID: journalID, Reason: "AGED_UNMATCHED", PolicySnapshot: policyJSON}); err != nil {
			return result, err
		}
		result.AgedReviewCount++
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (s *ReconciliationMatchingService) CreateManualGroupedMatch(ctx context.Context, command CreateManualGroupedMatchCommand) (MatchResult, error) {
	if len(command.BankStatementLineIDs) == 0 || len(command.JournalLineIDs) == 0 || command.IdempotencyKey == "" {
		return MatchResult{}, fmt.Errorf("manual grouped match needs both sides and an idempotency key")
	}
	requestHash := manualMatchRequestHash(command)
	return s.withMatchingActor(ctx, command.ReconciliationActor, func(ctx context.Context, tx pgx.Tx, q *database.Queries, actorID pgtype.UUID) (MatchResult, error) {
		bankID, err := postingUUID(command.BankAccountID)
		if err != nil {
			return MatchResult{}, err
		}
		entityID, _ := postingUUID(command.EntityID)
		var ownsRealm bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM toro_core.erp_connections WHERE entity_id = $1 AND realm_id = $2)", entityID, command.RealmID).Scan(&ownsRealm); err != nil || !ownsRealm {
			return MatchResult{}, fmt.Errorf("match realm does not belong to the actor entity")
		}
		if existing, err := q.GetReconciliationMatchGroupByIdempotencyKey(ctx, command.IdempotencyKey); err == nil {
			if existing.RequestHash != requestHash || existing.RealmID != command.RealmID || existing.BankAccountID != bankID {
				return MatchResult{}, fmt.Errorf("grouped-match idempotency conflict")
			}
			return MatchResult{MatchGroupID: uuidString(existing.ID), Status: existing.Status, IdempotentReplay: true}, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return MatchResult{}, err
		}
		policyRow, err := q.GetReconciliationMatchWindow(ctx, database.GetReconciliationMatchWindowParams{RealmID: command.RealmID, ItemType: command.ItemType})
		if err != nil {
			return MatchResult{}, err
		}
		policy := matchingPolicySnapshot{ItemType: command.ItemType, MaxDays: policyRow.MaxDays, AgeingDays: policyRow.AgeingDays, SourceRealm: policyRow.RealmID, Version: policyRow.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)}
		policyJSON, _ := json.Marshal(policy)
		bankTotal, bookTotal := MustReconciliationMoney("0"), MustReconciliationMoney("0")
		direction := ""
		var bankDate, bookDate time.Time
		bankIDs := make([]pgtype.UUID, 0, len(command.BankStatementLineIDs))
		journalIDs := make([]pgtype.UUID, 0, len(command.JournalLineIDs))
		for _, value := range command.BankStatementLineIDs {
			id, err := postingUUID(value)
			if err != nil {
				return MatchResult{}, err
			}
			var realm, currency, lineDirection string
			var accountID pgtype.UUID
			var amount pgtype.Numeric
			var opDate, valueDate pgtype.Date
			if err := tx.QueryRow(ctx, `SELECT realm_id, bank_account_id, currency, direction, amount, operation_date, value_date FROM shadow_erp.bank_statement_lines WHERE id = $1`, id).Scan(&realm, &accountID, &currency, &lineDirection, &amount, &opDate, &valueDate); err != nil {
				return MatchResult{}, err
			}
			if realm != command.RealmID || accountID != bankID || currency != command.Currency || (direction != "" && direction != lineDirection) {
				return MatchResult{}, fmt.Errorf("bank grouped-match members are outside one scope/direction")
			}
			direction = lineDirection
			amountText, _ := numericText(amount)
			bankTotal = bankTotal.Add(MustReconciliationMoney(amountText))
			date := opDate.Time
			if !opDate.Valid {
				date = valueDate.Time
			}
			if bankDate.IsZero() || date.Before(bankDate) {
				bankDate = date
			}
			bankIDs = append(bankIDs, id)
		}
		for _, value := range command.JournalLineIDs {
			id, err := postingUUID(value)
			if err != nil {
				return MatchResult{}, err
			}
			var realm, currency, accountCode, ledgerCode string
			var debit, credit pgtype.Numeric
			var entryDate pgtype.Date
			if err := tx.QueryRow(ctx, `SELECT je.realm_id, je.currency, a.account_code, ba.ledger_account_code, jl.debit, jl.credit, je.entry_date
				FROM shadow_erp.journal_lines jl JOIN shadow_erp.journal_entries je ON je.id = jl.journal_entry_id JOIN shadow_erp.accounts a ON a.id = jl.account_id JOIN shadow_erp.bank_accounts ba ON ba.id = $2 WHERE jl.id = $1`, id, bankID).Scan(&realm, &currency, &accountCode, &ledgerCode, &debit, &credit, &entryDate); err != nil {
				return MatchResult{}, err
			}
			if realm != command.RealmID || currency != command.Currency || accountCode != ledgerCode {
				return MatchResult{}, fmt.Errorf("journal grouped-match member is outside bank scope")
			}
			debitText, _ := numericText(debit)
			creditText, _ := numericText(credit)
			lineDirection, amount := "OUTFLOW", MustReconciliationMoney(creditText)
			if !MustReconciliationMoney(debitText).IsZero() {
				lineDirection, amount = "INFLOW", MustReconciliationMoney(debitText)
			}
			if lineDirection != direction {
				return MatchResult{}, fmt.Errorf("grouped-match directions are incompatible")
			}
			bookTotal = bookTotal.Add(amount)
			if bookDate.IsZero() || entryDate.Time.Before(bookDate) {
				bookDate = entryDate.Time
			}
			journalIDs = append(journalIDs, id)
		}
		if !bankTotal.Equal(bookTotal) {
			return MatchResult{}, fmt.Errorf("manual grouped-match totals differ: bank %s, book %s", bankTotal.String(), bookTotal.String())
		}
		if err := ensureMembersNotConfirmed(ctx, tx, bankIDs, journalIDs); err != nil {
			return MatchResult{}, err
		}
		amount, _ := postingNumeric(bankTotal.String())
		group, err := q.CreateReconciliationMatchGroup(ctx, database.CreateReconciliationMatchGroupParams{IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, RealmID: command.RealmID, BankAccountID: bankID, Currency: command.Currency, ItemType: command.ItemType, Direction: direction, Amount: amount, BankReferenceDate: pgtype.Date{Time: bankDate, Valid: true}, BookReferenceDate: pgtype.Date{Time: bookDate, Valid: true}, PolicyMaxDays: policy.MaxDays, PolicySnapshot: policyJSON, EvidenceRanking: mustJSON(map[string]interface{}{"manual": true}), Status: "CANDIDATE", CreatedBy: actorID, CreatedByKind: "USER"})
		if err != nil {
			return MatchResult{}, err
		}
		for _, id := range bankIDs {
			if _, err := q.CreateReconciliationMatchMember(ctx, database.CreateReconciliationMatchMemberParams{MatchGroupID: group.ID, BankStatementLineID: id}); err != nil {
				return MatchResult{}, err
			}
		}
		for _, id := range journalIDs {
			if _, err := q.CreateReconciliationMatchMember(ctx, database.CreateReconciliationMatchMemberParams{MatchGroupID: group.ID, JournalLineID: id}); err != nil {
				return MatchResult{}, err
			}
		}
		confirmed, err := q.ConfirmReconciliationMatchGroup(ctx, database.ConfirmReconciliationMatchGroupParams{ID: group.ID, ConfirmedBy: actorID})
		if err != nil {
			return MatchResult{}, err
		}
		return MatchResult{MatchGroupID: uuidString(confirmed.ID), Status: confirmed.Status}, nil
	})
}

func (s *ReconciliationMatchingService) ConfirmCandidate(ctx context.Context, command ConfirmMatchCandidateCommand) (MatchResult, error) {
	groupID, err := postingUUID(command.MatchGroupID)
	if err != nil {
		return MatchResult{}, err
	}
	return s.withMatchingActor(ctx, command.ReconciliationActor, func(ctx context.Context, tx pgx.Tx, q *database.Queries, actorID pgtype.UUID) (MatchResult, error) {
		var realm string
		var status string
		if err := tx.QueryRow(ctx, "SELECT realm_id, status FROM shadow_erp.reconciliation_match_groups WHERE id = $1 FOR UPDATE", groupID).Scan(&realm, &status); err != nil {
			return MatchResult{}, err
		}
		if status == "CONFIRMED" {
			return MatchResult{MatchGroupID: command.MatchGroupID, Status: status, IdempotentReplay: true}, nil
		}
		var ownsRealm bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM toro_core.erp_connections c
			JOIN toro_core.users u ON u.entity_id = c.entity_id
			WHERE c.realm_id = $1 AND u.id = $2 AND u.is_active = TRUE
		)`, realm, actorID).Scan(&ownsRealm); err != nil || !ownsRealm {
			return MatchResult{}, fmt.Errorf("match candidate belongs to another entity")
		}
		if err := ensureGroupMembersNotConfirmed(ctx, tx, groupID); err != nil {
			return MatchResult{}, err
		}
		group, err := q.ConfirmReconciliationMatchGroup(ctx, database.ConfirmReconciliationMatchGroupParams{ID: groupID, ConfirmedBy: actorID})
		if err != nil {
			return MatchResult{}, err
		}
		if _, err := q.ResolveReviewQueueForMatchGroup(ctx, database.ResolveReviewQueueForMatchGroupParams{MatchGroupID: group.ID, ResolvedBy: actorID}); err != nil {
			return MatchResult{}, err
		}
		return MatchResult{MatchGroupID: uuidString(group.ID), Status: group.Status}, nil
	})
}

func (s *ReconciliationMatchingService) ResolveReviewItem(ctx context.Context, command ResolveReconciliationReviewCommand) (MatchResult, error) {
	itemID, err := postingUUID(command.ReviewQueueItemID)
	if err != nil {
		return MatchResult{}, err
	}
	if command.Resolution != "RESOLVED" && command.Resolution != "DISMISSED" {
		return MatchResult{}, fmt.Errorf("review resolution must be RESOLVED or DISMISSED")
	}
	return s.withMatchingActor(ctx, command.ReconciliationActor, func(ctx context.Context, tx pgx.Tx, q *database.Queries, actorID pgtype.UUID) (MatchResult, error) {
		var realm string
		var status string
		var matchGroupID pgtype.UUID
		if err := tx.QueryRow(ctx, "SELECT realm_id, status, match_group_id FROM shadow_erp.reconciliation_review_queue WHERE id = $1 FOR UPDATE", itemID).Scan(&realm, &status, &matchGroupID); err != nil {
			return MatchResult{}, err
		}
		if status != "OPEN" {
			if status != command.Resolution {
				return MatchResult{}, fmt.Errorf("review item was already resolved as %s", status)
			}
			return MatchResult{MatchGroupID: uuidString(matchGroupID), Status: status, IdempotentReplay: true}, nil
		}
		entityID, _ := postingUUID(command.EntityID)
		var ownsRealm bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM toro_core.erp_connections WHERE entity_id = $1 AND realm_id = $2)", entityID, realm).Scan(&ownsRealm); err != nil || !ownsRealm {
			return MatchResult{}, fmt.Errorf("review item belongs to another entity")
		}
		item, err := q.ResolveReconciliationReviewQueueItem(ctx, database.ResolveReconciliationReviewQueueItemParams{ID: itemID, ResolvedBy: actorID, Status: command.Resolution})
		if err != nil {
			return MatchResult{}, err
		}
		return MatchResult{MatchGroupID: uuidString(item.MatchGroupID), Status: item.Status}, nil
	})
}

func (s *ReconciliationMatchingService) withMatchingActor(ctx context.Context, actor ReconciliationActor, fn func(context.Context, pgx.Tx, *database.Queries, pgtype.UUID) (MatchResult, error)) (MatchResult, error) {
	if s == nil || s.pool == nil {
		return MatchResult{}, fmt.Errorf("matching database is unavailable")
	}
	actorID, err := postingUUID(actor.ActorUserID)
	if err != nil {
		return MatchResult{}, err
	}
	entityID, err := postingUUID(actor.EntityID)
	if err != nil {
		return MatchResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return MatchResult{}, err
	}
	defer tx.Rollback(ctx)
	q := database.New(tx)
	if _, err := q.GetActiveUserInEntity(ctx, database.GetActiveUserInEntityParams{ID: actorID, EntityID: entityID}); err != nil {
		return MatchResult{}, fmt.Errorf("actor is not an active user in the entity: %w", err)
	}
	result, err := fn(ctx, tx, q, actorID)
	if err != nil {
		return MatchResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MatchResult{}, err
	}
	return result, nil
}

func createCandidateGroup(ctx context.Context, q *database.Queries, params database.CreateReconciliationMatchGroupParams) (database.ShadowErpReconciliationMatchGroup, bool, error) {
	if existing, err := q.GetReconciliationMatchGroupByIdempotencyKey(ctx, params.IdempotencyKey); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return database.ShadowErpReconciliationMatchGroup{}, false, err
	}
	group, err := q.CreateReconciliationMatchGroup(ctx, params)
	return group, false, err
}

func inferTreasuryItemType(description string) string {
	upper := strings.ToUpper(description)
	switch {
	case strings.Contains(upper, "VIR") || strings.Contains(upper, "TRANSFER") || strings.Contains(upper, "5115"):
		return "INTERNAL_TRANSFER"
	case strings.Contains(upper, "TPE") || strings.Contains(upper, "CARD") || strings.Contains(upper, "CARTE"):
		return "CARD_TPE"
	case strings.Contains(upper, "CHEQUE") || strings.Contains(upper, "CHQ") || strings.Contains(upper, "LCN") || strings.Contains(upper, "EFFET"):
		return "CHEQUE_BILL"
	default:
		return "GENERIC"
	}
}

func rankMatchEvidence(bank database.ShadowErpBankStatementLine, book matchingBookMovement) int {
	score := 0
	label := normalizeMatchText(book.Label)
	if bank.ExternalReference.Valid && strings.Contains(label, normalizeMatchText(bank.ExternalReference.String)) {
		score += 100
	}
	if bank.CounterpartyName.Valid && strings.Contains(label, normalizeMatchText(bank.CounterpartyName.String)) {
		score += 20
	}
	if bank.SourceStagingTransactionID.Valid && book.SourceBankLineID.Valid && bank.ID == book.SourceBankLineID {
		score += 1000
	}
	return score
}

func normalizeMatchText(value string) string {
	return strings.Join(strings.Fields(strings.ToUpper(value)), " ")
}

func stableMatchKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func manualMatchRequestHash(command CreateManualGroupedMatchCommand) string {
	bankIDs := append([]string(nil), command.BankStatementLineIDs...)
	journalIDs := append([]string(nil), command.JournalLineIDs...)
	sort.Strings(bankIDs)
	sort.Strings(journalIDs)
	return stableMatchKey(
		"manual-v1", command.RealmID, command.BankAccountID, command.Currency,
		command.ItemType, strings.Join(bankIDs, ","), strings.Join(journalIDs, ","),
	)
}

func mustJSON(value interface{}) []byte { encoded, _ := json.Marshal(value); return encoded }

func ensureMembersNotConfirmed(ctx context.Context, tx pgx.Tx, bankIDs, journalIDs []pgtype.UUID) error {
	for _, id := range bankIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shadow_erp.reconciliation_match_members m JOIN shadow_erp.reconciliation_match_groups g ON g.id=m.match_group_id WHERE m.bank_statement_line_id=$1 AND g.status='CONFIRMED')`, id).Scan(&exists); err != nil || exists {
			return fmt.Errorf("bank line is already confirmed in another match")
		}
	}
	for _, id := range journalIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shadow_erp.reconciliation_match_members m JOIN shadow_erp.reconciliation_match_groups g ON g.id=m.match_group_id WHERE m.journal_line_id=$1 AND g.status='CONFIRMED')`, id).Scan(&exists); err != nil || exists {
			return fmt.Errorf("journal line is already confirmed in another match")
		}
	}
	return nil
}

func ensureGroupMembersNotConfirmed(ctx context.Context, tx pgx.Tx, groupID pgtype.UUID) error {
	var conflict bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM shadow_erp.reconciliation_match_members selected
		JOIN shadow_erp.reconciliation_match_members other ON
		  (selected.bank_statement_line_id IS NOT NULL AND selected.bank_statement_line_id=other.bank_statement_line_id)
		  OR (selected.journal_line_id IS NOT NULL AND selected.journal_line_id=other.journal_line_id)
		JOIN shadow_erp.reconciliation_match_groups g ON g.id=other.match_group_id
		WHERE selected.match_group_id=$1 AND other.match_group_id<>$1 AND g.status='CONFIRMED')`, groupID).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return fmt.Errorf("a candidate member is already confirmed in another match")
	}
	return nil
}

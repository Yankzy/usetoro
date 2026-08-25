package workers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// UnmarshalJSON keeps OCR monetary values as json.Number. The default
// interface{} decoding path converts them to float64, which is not acceptable
// at the canonical reconciliation boundary.
func (e *PcmOCRExtraction) UnmarshalJSON(data []byte) error {
	var raw struct {
		DocType       string                       `json:"doc_type"`
		FileName      string                       `json:"file_name,omitempty"`
		Data          json.RawMessage              `json:"data"`
		ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
		Confidence    float64                      `json:"confidence"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	e.DocType = raw.DocType
	e.FileName = raw.FileName
	e.Confidence = raw.Confidence
	e.ColumnMapping = raw.ColumnMapping
	if len(raw.Data) > 0 && string(raw.Data) != "null" {
		dataDecoder := json.NewDecoder(bytes.NewReader(raw.Data))
		dataDecoder.UseNumber()
		if err := dataDecoder.Decode(&e.Data); err != nil {
			return err
		}
	}
	return nil
}

func decodeJSONMapUseNumber(data []byte, target *map[string]interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

type pcmBankAccountLookup interface {
	GetBankAccountsByRealm(context.Context, string) ([]database.ShadowErpBankAccount, error)
	ListBankAccountsByNormalizedIdentifiers(context.Context, database.ListBankAccountsByNormalizedIdentifiersParams) ([]database.ShadowErpBankAccount, error)
}

type pcmStatementLineStore interface {
	GetBankStatementLineByDocumentIndex(context.Context, database.GetBankStatementLineByDocumentIndexParams) (database.ShadowErpBankStatementLine, error)
	InsertBankStatementLine(context.Context, database.InsertBankStatementLineParams) (int64, error)
}

type statementAccountIdentifiers struct {
	AccountNumber string
	RIB           string
	IBAN          string
}

func (i statementAccountIdentifiers) empty() bool {
	return i.AccountNumber == "" && i.RIB == "" && i.IBAN == ""
}

func (i statementAccountIdentifiers) String() string {
	return fmt.Sprintf("account_number=%q rib=%q iban=%q", i.AccountNumber, i.RIB, i.IBAN)
}

func normalizeBankIdentifier(value string) string {
	var normalized strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func statementIntakeKey(realmID, bankAccountID string, documentIDs []string) string {
	ids := append([]string(nil), documentIDs...)
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(append([]string{realmID, bankAccountID}, ids...), "\n")))
	return fmt.Sprintf("%x", sum[:])
}

func identifiersFromStatement(document PcmParsedDocument) statementAccountIdentifiers {
	return statementAccountIdentifiers{
		AccountNumber: normalizeBankIdentifier(firstString(document.Data, "account_number", "account_no", "bank_account_number")),
		RIB:           normalizeBankIdentifier(firstString(document.Data, "rib", "bank_rib")),
		IBAN:          normalizeBankIdentifier(firstString(document.Data, "iban", "bank_iban")),
	}
}

func (w *PcmWorker) resolveBankAccountContext(ctx context.Context, realmID string, documents []PcmParsedDocument) (PcmBankAccountContext, error) {
	result := PcmBankAccountContext{}
	lookup := w.bankAccountLookup
	if lookup == nil {
		lookup = w.db
	}
	if lookup == nil {
		return result, fmt.Errorf("pcm: database unavailable while resolving bank account")
	}

	var selected database.ShadowErpBankAccount
	selectedSet := false
	for _, document := range documents {
		if !isBankStatementType(document.Type) {
			continue
		}
		if _, err := uuid.Parse(document.DocumentID); err != nil {
			return result, fmt.Errorf("HOLD_STATEMENT_METADATA: bank statement document_id %q is missing or invalid", document.DocumentID)
		}
		result.SourceDocumentIDs = append(result.SourceDocumentIDs, document.DocumentID)

		identifiers := identifiersFromStatement(document)
		var candidates []database.ShadowErpBankAccount
		var err error
		if identifiers.empty() {
			candidates, err = lookup.GetBankAccountsByRealm(ctx, realmID)
		} else {
			candidates, err = lookup.ListBankAccountsByNormalizedIdentifiers(ctx, database.ListBankAccountsByNormalizedIdentifiersParams{
				RealmID:                 realmID,
				NormalizedAccountNumber: identifiers.AccountNumber,
				NormalizedRib:           identifiers.RIB,
				NormalizedIban:          identifiers.IBAN,
			})
		}
		if err != nil {
			return result, fmt.Errorf("pcm: resolve bank account for document %q: %w", document.DocumentID, err)
		}
		if len(candidates) != 1 {
			explainCandidates := candidates
			if len(explainCandidates) == 0 && !identifiers.empty() {
				if configured, configuredErr := lookup.GetBankAccountsByRealm(ctx, realmID); configuredErr == nil {
					explainCandidates = configured
				}
			}
			return result, fmt.Errorf(
				"HOLD_BANK_ACCOUNT_RESOLUTION: document %q in realm %q resolved %d accounts from %s; candidates=%s",
				document.DocumentID,
				realmID,
				len(candidates),
				identifiers.String(),
				bankAccountCandidateIDs(explainCandidates),
			)
		}

		candidate := candidates[0]
		if !candidate.ID.Valid || strings.TrimSpace(candidate.LedgerAccountCode) == "" || !candidate.Currency.Valid || strings.TrimSpace(candidate.Currency.String) == "" {
			return result, fmt.Errorf("HOLD_BANK_ACCOUNT_CONFIGURATION: bank account resolved for document %q is incomplete", document.DocumentID)
		}
		if statementCurrency := strings.ToUpper(firstString(document.Data, "currency", "currency_code", "iso_currency_code")); statementCurrency != "" && statementCurrency != strings.ToUpper(candidate.Currency.String) {
			return result, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q currency %q does not match bank account currency %q", document.DocumentID, statementCurrency, candidate.Currency.String)
		}
		start, startErr := parseStatementDate(firstString(document.Data, "start_date", "period_start", "statement_start_date"))
		end, endErr := parseStatementDate(firstString(document.Data, "end_date", "period_end", "statement_end_date", "statement_date"))
		opening, openingErr := parseReconciliationAmount(firstValue(document.Data, "starting_balance", "opening_balance"), true)
		closing, closingErr := parseReconciliationAmount(firstValue(document.Data, "ending_balance", "closing_balance"), true)
		if startErr != nil || endErr != nil || openingErr != nil || closingErr != nil || end.Before(start) || start.Format("2006-01") != end.Format("2006-01") {
			return result, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q does not contain one reliable monthly period and opening/closing position", document.DocumentID)
		}
		periodKey := end.Format("2006-01")
		if result.StatementPeriodKey != "" && (result.StatementPeriodKey != periodKey || result.StatementOpeningBalance != opening.String() || result.StatementClosingBalance != closing.String()) {
			return result, fmt.Errorf("HOLD_STATEMENT_METADATA: intake contains conflicting statement periods or positions")
		}
		result.StatementPeriodKey = periodKey
		result.StatementOpeningBalance = opening.String()
		result.StatementClosingBalance = closing.String()
		if selectedSet && selected.ID.Bytes != candidate.ID.Bytes {
			return result, fmt.Errorf("HOLD_BANK_ACCOUNT_RESOLUTION: one PCM intake contains statements for multiple bank accounts (%s, %s)", uuid.UUID(selected.ID.Bytes), uuid.UUID(candidate.ID.Bytes))
		}
		selected = candidate
		selectedSet = true
	}

	if !selectedSet {
		return result, fmt.Errorf("HOLD_BANK_ACCOUNT_RESOLUTION: no bank statement document was available for realm %q", realmID)
	}
	result.BankAccountID = uuid.UUID(selected.ID.Bytes).String()
	result.LedgerAccountCode = selected.LedgerAccountCode
	result.Currency = strings.ToUpper(selected.Currency.String)
	return result, nil
}

func bankAccountCandidateIDs(accounts []database.ShadowErpBankAccount) string {
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.ID.Valid {
			ids = append(ids, uuid.UUID(account.ID.Bytes).String())
		}
	}
	sort.Strings(ids)
	return "[" + strings.Join(ids, ",") + "]"
}

type canonicalBankStatementLine struct {
	RealmID                    string
	BankAccountID              pgtype.UUID
	SourceDocumentID           pgtype.UUID
	LineIndex                  int32
	ExternalReference          pgtype.Text
	OperationDate              pgtype.Date
	ValueDate                  pgtype.Date
	Direction                  string
	Amount                     pgtype.Numeric
	Currency                   string
	Description                string
	CounterpartyName           pgtype.Text
	SourceStagingTransactionID pgtype.UUID
}

func prepareCanonicalBankStatementLines(realmID string, documents []PcmParsedDocument, bankContext PcmBankAccountContext) ([]canonicalBankStatementLine, error) {
	if strings.TrimSpace(realmID) == "" {
		return nil, fmt.Errorf("HOLD_STATEMENT_METADATA: realm_id is required")
	}
	bankAccountID, err := parsePGUUID(bankContext.BankAccountID)
	if err != nil {
		return nil, fmt.Errorf("HOLD_BANK_ACCOUNT_CONFIGURATION: invalid resolved bank account id: %w", err)
	}
	if strings.TrimSpace(bankContext.Currency) == "" {
		return nil, fmt.Errorf("HOLD_BANK_ACCOUNT_CONFIGURATION: resolved bank account currency is required")
	}

	var lines []canonicalBankStatementLine
	for _, document := range documents {
		if !isBankStatementType(document.Type) {
			continue
		}
		documentID, err := parsePGUUID(document.DocumentID)
		if err != nil {
			return nil, fmt.Errorf("HOLD_STATEMENT_METADATA: invalid document_id %q: %w", document.DocumentID, err)
		}
		if document.ColumnMapping != nil && document.ColumnMapping.IsAmbiguous {
			return nil, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has ambiguous statement columns: %s", document.DocumentID, document.ColumnMapping.AmbiguityReason)
		}
		if err := validateStatementCoverage(document); err != nil {
			return nil, err
		}

		transactions := extractTransactionMaps(document.Data["transactions"])
		if len(transactions) == 0 {
			return nil, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has no usable statement lines", document.DocumentID)
		}
		for lineIndex, transaction := range transactions {
			line, err := canonicalLineFromTransaction(realmID, document, transaction, int32(lineIndex), bankAccountID, documentID, bankContext.Currency)
			if err != nil {
				return nil, err
			}
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// findCanonicalStatementReplay detects a complete prior intake before any new
// staging rows are written. A partial or changed replay is held: extending or
// rewriting the canonical line set for an already-observed source document
// would make prior proposals and postings impossible to reproduce.
func (w *PcmWorker) findCanonicalStatementReplay(ctx context.Context, expected []canonicalBankStatementLine) (pgtype.UUID, []string, bool, error) {
	if w.db == nil || len(expected) == 0 {
		return pgtype.UUID{}, nil, false, nil
	}
	expectedByDocument := make(map[string]int)
	documentIDs := make(map[string]pgtype.UUID)
	for _, line := range expected {
		documentID := uuid.UUID(line.SourceDocumentID.Bytes).String()
		expectedByDocument[documentID]++
		documentIDs[documentID] = line.SourceDocumentID
	}
	existingByKey := make(map[string]database.ShadowErpBankStatementLine, len(expected))
	for documentID, pgID := range documentIDs {
		lines, err := w.db.ListBankStatementLinesByDocument(ctx, pgID)
		if err != nil {
			return pgtype.UUID{}, nil, false, fmt.Errorf("pcm: inspect canonical statement replay for document %s: %w", documentID, err)
		}
		if len(lines) != 0 && len(lines) != expectedByDocument[documentID] {
			return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: document %s already has %d canonical lines, replay supplied %d", documentID, len(lines), expectedByDocument[documentID])
		}
		for _, line := range lines {
			key := fmt.Sprintf("%s:%d", documentID, line.LineIndex)
			existingByKey[key] = line
		}
	}
	if len(existingByKey) == 0 {
		return pgtype.UUID{}, nil, false, nil
	}
	if len(existingByKey) != len(expected) {
		return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: statement replay is partial across source documents")
	}

	var sessionID pgtype.UUID
	lineIDs := make([]string, 0, len(expected))
	for rowIndex, line := range expected {
		documentID := uuid.UUID(line.SourceDocumentID.Bytes).String()
		existing, ok := existingByKey[fmt.Sprintf("%s:%d", documentID, line.LineIndex)]
		if !ok || !canonicalObservedLineMatches(existing, line) {
			return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: document %s line %d changed after canonical intake", documentID, line.LineIndex)
		}
		if !existing.SourceStagingTransactionID.Valid {
			return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: document %s line %d has no staging provenance", documentID, line.LineIndex)
		}
		staging, err := w.db.GetProposedTransactionByID(ctx, existing.SourceStagingTransactionID)
		if err != nil || !staging.SessionID.Valid || !staging.RowIndex.Valid || int(staging.RowIndex.Int32) != rowIndex {
			return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: document %s line %d has inconsistent staging provenance", documentID, line.LineIndex)
		}
		if !sessionID.Valid {
			sessionID = staging.SessionID
		} else if sessionID != staging.SessionID {
			return pgtype.UUID{}, nil, false, fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: one statement replay maps to multiple staging sessions")
		}
		lineIDs = append(lineIDs, uuid.UUID(existing.ID.Bytes).String())
	}
	return sessionID, lineIDs, true, nil
}

func validateStatementCoverage(document PcmParsedDocument) error {
	startRaw := firstString(document.Data, "start_date", "period_start", "statement_start_date")
	endRaw := firstString(document.Data, "end_date", "period_end", "statement_end_date", "statement_date")
	start, err := parseStatementDate(startRaw)
	if err != nil {
		return fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has invalid or missing start date %q", document.DocumentID, startRaw)
	}
	end, err := parseStatementDate(endRaw)
	if err != nil || end.Before(start) {
		return fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has invalid statement coverage %q to %q", document.DocumentID, startRaw, endRaw)
	}
	if _, err := parseReconciliationAmount(firstValue(document.Data, "starting_balance", "opening_balance"), true); err != nil {
		return fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has invalid or missing opening balance: %w", document.DocumentID, err)
	}
	if _, err := parseReconciliationAmount(firstValue(document.Data, "ending_balance", "closing_balance"), true); err != nil {
		return fmt.Errorf("HOLD_STATEMENT_METADATA: document %q has invalid or missing closing balance: %w", document.DocumentID, err)
	}
	return nil
}

func canonicalLineFromTransaction(realmID string, document PcmParsedDocument, transaction map[string]interface{}, lineIndex int32, bankAccountID, documentID pgtype.UUID, currency string) (canonicalBankStatementLine, error) {
	description := firstString(transaction, "description", "label", "libelle")
	if description == "" {
		return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d is missing a description", document.DocumentID, lineIndex)
	}
	direction, err := canonicalDirection(firstString(transaction, "type", "direction", "transaction_type"))
	if err != nil {
		return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d: %w", document.DocumentID, lineIndex, err)
	}
	amount, err := parseReconciliationAmount(transaction["amount"], false)
	if err != nil {
		return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d has invalid amount: %w", document.DocumentID, lineIndex, err)
	}
	if amount.IsZero() {
		return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d has a zero amount", document.DocumentID, lineIndex)
	}
	amountString := strings.TrimPrefix(amount.String(), "-")
	var numeric pgtype.Numeric
	if err := numeric.Scan(amountString); err != nil {
		return canonicalBankStatementLine{}, fmt.Errorf("pcm: encode canonical amount for document %q line %d: %w", document.DocumentID, lineIndex, err)
	}

	operationTime, err := parseStatementDate(firstString(transaction, "operation_date", "date"))
	if err != nil {
		return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d has invalid operation date", document.DocumentID, lineIndex)
	}
	valueDate := pgtype.Date{}
	if raw := firstString(transaction, "value_date"); raw != "" {
		parsed, err := parseStatementDate(raw)
		if err != nil {
			return canonicalBankStatementLine{}, fmt.Errorf("HOLD_STATEMENT_METADATA: document %q line %d has invalid value date", document.DocumentID, lineIndex)
		}
		valueDate = pgtype.Date{Time: parsed, Valid: true}
	}

	return canonicalBankStatementLine{
		RealmID:           realmID,
		BankAccountID:     bankAccountID,
		SourceDocumentID:  documentID,
		LineIndex:         lineIndex,
		ExternalReference: optionalText(firstString(transaction, "external_reference", "reference", "transaction_id")),
		OperationDate:     pgtype.Date{Time: operationTime, Valid: true},
		ValueDate:         valueDate,
		Direction:         direction,
		Amount:            numeric,
		Currency:          strings.ToUpper(currency),
		Description:       description,
		CounterpartyName:  optionalText(firstString(transaction, "counterparty_name", "counterparty", "beneficiary")),
	}, nil
}

func (w *PcmWorker) persistCanonicalBankStatementLine(ctx context.Context, line canonicalBankStatementLine) (string, error) {
	store := w.statementLineStore
	if store == nil {
		store = w.db
	}
	if store == nil {
		return "", fmt.Errorf("pcm: database unavailable while persisting canonical bank line")
	}
	line.RealmID = strings.TrimSpace(line.RealmID)
	if line.RealmID == "" {
		return "", fmt.Errorf("pcm: canonical bank line realm is required")
	}
	params := database.InsertBankStatementLineParams{
		RealmID:                    line.RealmID,
		BankAccountID:              line.BankAccountID,
		SourceDocumentID:           line.SourceDocumentID,
		LineIndex:                  line.LineIndex,
		ExternalReference:          line.ExternalReference,
		OperationDate:              line.OperationDate,
		ValueDate:                  line.ValueDate,
		Direction:                  line.Direction,
		Amount:                     line.Amount,
		Currency:                   line.Currency,
		Description:                line.Description,
		CounterpartyName:           line.CounterpartyName,
		SourceStagingTransactionID: line.SourceStagingTransactionID,
	}
	if _, err := store.InsertBankStatementLine(ctx, params); err != nil {
		return "", fmt.Errorf("pcm: insert canonical bank line for document %s index %d: %w", uuid.UUID(line.SourceDocumentID.Bytes), line.LineIndex, err)
	}
	existing, err := store.GetBankStatementLineByDocumentIndex(ctx, database.GetBankStatementLineByDocumentIndexParams{
		SourceDocumentID: line.SourceDocumentID,
		LineIndex:        line.LineIndex,
	})
	if err != nil {
		return "", fmt.Errorf("pcm: load canonical bank line for document %s index %d: %w", uuid.UUID(line.SourceDocumentID.Bytes), line.LineIndex, err)
	}
	if !canonicalLineMatches(existing, line) {
		return "", fmt.Errorf("HOLD_CANONICAL_BANK_LINE_CONFLICT: document %s line %d was already ingested with different canonical evidence", uuid.UUID(line.SourceDocumentID.Bytes), line.LineIndex)
	}
	return uuid.UUID(existing.ID.Bytes).String(), nil
}

func canonicalLineMatches(existing database.ShadowErpBankStatementLine, expected canonicalBankStatementLine) bool {
	return existing.RealmID == expected.RealmID &&
		existing.BankAccountID == expected.BankAccountID &&
		existing.SourceDocumentID == expected.SourceDocumentID &&
		existing.LineIndex == expected.LineIndex &&
		existing.ExternalReference == expected.ExternalReference &&
		existing.OperationDate == expected.OperationDate &&
		existing.ValueDate == expected.ValueDate &&
		existing.Direction == expected.Direction &&
		numericText(existing.Amount) == numericText(expected.Amount) &&
		existing.Currency == expected.Currency &&
		existing.Description == expected.Description &&
		existing.CounterpartyName == expected.CounterpartyName &&
		existing.SourceStagingTransactionID == expected.SourceStagingTransactionID
}

func canonicalObservedLineMatches(existing database.ShadowErpBankStatementLine, expected canonicalBankStatementLine) bool {
	expected.SourceStagingTransactionID = existing.SourceStagingTransactionID
	return canonicalLineMatches(existing, expected)
}

func numericText(value pgtype.Numeric) string {
	driverValue, err := value.Value()
	if err != nil || driverValue == nil {
		return ""
	}
	return fmt.Sprint(driverValue)
}

func canonicalDirection(value string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DEBIT", "OUTFLOW", "WITHDRAWAL":
		return "OUTFLOW", nil
	case "CREDIT", "INFLOW", "DEPOSIT":
		return "INFLOW", nil
	default:
		return "", fmt.Errorf("unsupported bank movement direction %q", value)
	}
}

func parseReconciliationAmount(value interface{}, signed bool) (accounting.ReconciliationMoney, error) {
	raw := strings.TrimSpace(interfaceString(value))
	if raw == "" {
		return accounting.ReconciliationMoney{}, fmt.Errorf("amount is required")
	}
	negativeParentheses := strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")")
	raw = strings.Trim(raw, "()")
	raw = strings.ReplaceAll(raw, "\u00a0", "")
	raw = strings.ReplaceAll(raw, " ", "")
	for _, token := range []string{"MAD", "DH", "DHS"} {
		raw = strings.ReplaceAll(strings.ToUpper(raw), token, "")
	}
	if strings.Contains(raw, ",") {
		raw = strings.ReplaceAll(raw, ".", "")
		raw = strings.ReplaceAll(raw, ",", ".")
	}
	if negativeParentheses {
		raw = "-" + strings.TrimPrefix(raw, "+")
	}
	if !signed {
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
	}
	return accounting.NewReconciliationMoney(raw)
}

func parseStatementDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006", "2/1/2006", "2-1-2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported statement date %q", value)
}

func firstString(values map[string]interface{}, keys ...string) string {
	return strings.TrimSpace(interfaceString(firstValue(values, keys...)))
}

func firstValue(values map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func interfaceString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func parsePGUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func legacyDocumentID(inbound *PcmInboundMessage, index int) string {
	if index >= 0 && index < len(inbound.DocumentIDs) && inbound.DocumentIDs[index] != "" {
		return inbound.DocumentIDs[index]
	}
	if inbound.DocumentID != "" && index == 0 {
		return inbound.DocumentID
	}
	if len(inbound.DocumentIDs) == 1 {
		return inbound.DocumentIDs[0]
	}
	return ""
}

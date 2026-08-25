package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type bankAccountLookupStub struct {
	all     []database.ShadowErpBankAccount
	matches []database.ShadowErpBankAccount
}

func (s bankAccountLookupStub) GetBankAccountsByRealm(context.Context, string) ([]database.ShadowErpBankAccount, error) {
	return s.all, nil
}

func (s bankAccountLookupStub) ListBankAccountsByNormalizedIdentifiers(_ context.Context, _ database.ListBankAccountsByNormalizedIdentifiersParams) ([]database.ShadowErpBankAccount, error) {
	return s.matches, nil
}

type statementLineStoreStub struct {
	rowsAffected int64
	existing     database.ShadowErpBankStatementLine
	inserted     []database.InsertBankStatementLineParams
}

func (s *statementLineStoreStub) InsertBankStatementLine(_ context.Context, params database.InsertBankStatementLineParams) (int64, error) {
	s.inserted = append(s.inserted, params)
	return s.rowsAffected, nil
}

func (s *statementLineStoreStub) GetBankStatementLineByDocumentIndex(_ context.Context, _ database.GetBankStatementLineByDocumentIndexParams) (database.ShadowErpBankStatementLine, error) {
	return s.existing, nil
}

func TestResolveBankAccountContextSingleAccountFallback(t *testing.T) {
	account := testPhysicalBankAccount("10000000-0000-0000-0000-000000000001", "514100", "MAD")
	worker := &PcmWorker{bankAccountLookup: bankAccountLookupStub{all: []database.ShadowErpBankAccount{account}}}
	document := testStatementDocument("20000000-0000-0000-0000-000000000001")
	delete(document.Data, "account_number")

	result, err := worker.resolveBankAccountContext(context.Background(), "realm-a", []PcmParsedDocument{document})
	if err != nil {
		t.Fatalf("expected single-account fallback to resolve: %v", err)
	}
	if result.BankAccountID != "10000000-0000-0000-0000-000000000001" {
		t.Fatalf("unexpected bank account id %q", result.BankAccountID)
	}
}

func TestResolveBankAccountContextZeroMatchesHolds(t *testing.T) {
	configured := testPhysicalBankAccount("10000000-0000-0000-0000-000000000001", "514100", "MAD")
	worker := &PcmWorker{bankAccountLookup: bankAccountLookupStub{all: []database.ShadowErpBankAccount{configured}, matches: nil}}
	_, err := worker.resolveBankAccountContext(context.Background(), "realm-a", []PcmParsedDocument{testStatementDocument("20000000-0000-0000-0000-000000000001")})
	if err == nil || !strings.Contains(err.Error(), "HOLD_BANK_ACCOUNT_RESOLUTION") || !strings.Contains(err.Error(), "resolved 0 accounts") || !strings.Contains(err.Error(), "10000000-0000-0000-0000-000000000001") {
		t.Fatalf("expected explainable zero-match hold, got %v", err)
	}
}

func TestResolveBankAccountContextMultiAccountRealmUsesStatementIdentifier(t *testing.T) {
	wanted := testPhysicalBankAccount("10000000-0000-0000-0000-000000000001", "514100", "MAD")
	other := testPhysicalBankAccount("10000000-0000-0000-0000-000000000002", "514200", "MAD")
	worker := &PcmWorker{bankAccountLookup: bankAccountLookupStub{
		all:     []database.ShadowErpBankAccount{wanted, other},
		matches: []database.ShadowErpBankAccount{wanted},
	}}

	result, err := worker.resolveBankAccountContext(context.Background(), "realm-a", []PcmParsedDocument{testStatementDocument("20000000-0000-0000-0000-000000000001")})
	if err != nil {
		t.Fatalf("expected identifier to resolve one account in multi-account realm: %v", err)
	}
	if result.LedgerAccountCode != "514100" {
		t.Fatalf("unexpected ledger account %q", result.LedgerAccountCode)
	}
}

func TestResolveBankAccountContextAmbiguousMatchesHoldsWithCandidates(t *testing.T) {
	first := testPhysicalBankAccount("10000000-0000-0000-0000-000000000001", "514100", "MAD")
	second := testPhysicalBankAccount("10000000-0000-0000-0000-000000000002", "514200", "MAD")
	worker := &PcmWorker{bankAccountLookup: bankAccountLookupStub{matches: []database.ShadowErpBankAccount{first, second}}}

	_, err := worker.resolveBankAccountContext(context.Background(), "realm-a", []PcmParsedDocument{testStatementDocument("20000000-0000-0000-0000-000000000001")})
	if err == nil || !strings.Contains(err.Error(), "resolved 2 accounts") || !strings.Contains(err.Error(), "10000000-0000-0000-0000-000000000002") {
		t.Fatalf("expected candidate-bearing ambiguity hold, got %v", err)
	}
}

func TestProductionOCRCallbackPreservesCanonicalDocumentIDAndExactNumbers(t *testing.T) {
	payload := []byte(`{
		"document_id":"20000000-0000-0000-0000-000000000001",
		"session_id":"session-1",
		"ocr_extraction":{
			"doc_type":"bank_statement",
			"confidence":0.99,
			"data":{
				"start_date":"01/07/2026",
				"end_date":"31/07/2026",
				"starting_balance":100.0000,
				"ending_balance":101.2345,
				"transactions":{"1":{"date":"02/07/2026","description":"Transfer","amount":1.2345,"type":"credit"}}
			}
		}
	}`)
	worker := &PcmWorker{logger: slog.Default()}
	_, inbound, err := worker.parseInboundMessage(&nats.Msg{Data: payload})
	if err != nil {
		t.Fatalf("parse production callback: %v", err)
	}
	documents, err := worker.extractOCRDocuments(&inbound)
	if err != nil {
		t.Fatalf("extract callback document: %v", err)
	}
	if len(documents) != 1 || documents[0].DocumentID != inbound.DocumentID {
		t.Fatalf("expected top-level document id to survive callback, got %#v", documents)
	}
	transaction := extractTransactionMaps(documents[0].Data["transactions"])[0]
	if _, ok := transaction["amount"].(json.Number); !ok {
		t.Fatalf("expected exact json.Number, got %T", transaction["amount"])
	}
}

func TestPrepareCanonicalBankStatementLines(t *testing.T) {
	document := testStatementDocument("20000000-0000-0000-0000-000000000001")
	bankContext := PcmBankAccountContext{BankAccountID: "10000000-0000-0000-0000-000000000001", Currency: "MAD"}

	lines, err := prepareCanonicalBankStatementLines("realm-a", []PcmParsedDocument{document}, bankContext)
	if err != nil {
		t.Fatalf("prepare canonical lines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	line := lines[0]
	if line.Direction != "OUTFLOW" || numericText(line.Amount) != "12000.2500" || line.LineIndex != 0 {
		t.Fatalf("unexpected canonical line: direction=%s amount=%s index=%d", line.Direction, numericText(line.Amount), line.LineIndex)
	}
	if !line.OperationDate.Valid || !line.ValueDate.Valid || line.ExternalReference.String != "REF-1" {
		t.Fatalf("canonical provenance fields were not retained: %#v", line)
	}
}

func TestPrepareCanonicalBankStatementLinesHoldsUnreliableMetadata(t *testing.T) {
	document := testStatementDocument("20000000-0000-0000-0000-000000000001")
	delete(document.Data, "ending_balance")
	bankContext := PcmBankAccountContext{BankAccountID: "10000000-0000-0000-0000-000000000001", Currency: "MAD"}

	_, err := prepareCanonicalBankStatementLines("realm-a", []PcmParsedDocument{document}, bankContext)
	if err == nil || !strings.Contains(err.Error(), "HOLD_STATEMENT_METADATA") || !strings.Contains(err.Error(), "closing balance") {
		t.Fatalf("expected statement metadata hold, got %v", err)
	}
}

func TestPersistCanonicalBankStatementLineIsIdempotent(t *testing.T) {
	document := testStatementDocument("20000000-0000-0000-0000-000000000001")
	bankContext := PcmBankAccountContext{BankAccountID: "10000000-0000-0000-0000-000000000001", Currency: "MAD"}
	lines, err := prepareCanonicalBankStatementLines("realm-a", []PcmParsedDocument{document}, bankContext)
	if err != nil {
		t.Fatal(err)
	}
	line := lines[0]
	line.SourceStagingTransactionID = testPGUUID("30000000-0000-0000-0000-000000000001")
	existing := database.ShadowErpBankStatementLine{
		ID: testPGUUID("40000000-0000-0000-0000-000000000001"), RealmID: line.RealmID,
		BankAccountID: line.BankAccountID, SourceDocumentID: line.SourceDocumentID,
		LineIndex: line.LineIndex, ExternalReference: line.ExternalReference,
		OperationDate: line.OperationDate, ValueDate: line.ValueDate, Direction: line.Direction,
		Amount: line.Amount, Currency: line.Currency, Description: line.Description,
		CounterpartyName: line.CounterpartyName,
		// A retry creates a new staging row but must retain the original canonical link.
		SourceStagingTransactionID: testPGUUID("30000000-0000-0000-0000-000000000099"),
	}
	store := &statementLineStoreStub{rowsAffected: 0, existing: existing}
	worker := &PcmWorker{statementLineStore: store}

	id, err := worker.persistCanonicalBankStatementLine(context.Background(), line)
	if err != nil {
		t.Fatalf("idempotent persistence failed: %v", err)
	}
	if id != "40000000-0000-0000-0000-000000000001" || len(store.inserted) != 1 {
		t.Fatalf("unexpected idempotent result id=%q inserts=%d", id, len(store.inserted))
	}
}

func TestPersistCanonicalBankStatementLineHoldsConflictingReplay(t *testing.T) {
	document := testStatementDocument("20000000-0000-0000-0000-000000000001")
	bankContext := PcmBankAccountContext{BankAccountID: "10000000-0000-0000-0000-000000000001", Currency: "MAD"}
	lines, err := prepareCanonicalBankStatementLines("realm-a", []PcmParsedDocument{document}, bankContext)
	if err != nil {
		t.Fatal(err)
	}
	line := lines[0]
	existing := database.ShadowErpBankStatementLine{
		ID: testPGUUID("40000000-0000-0000-0000-000000000001"), RealmID: line.RealmID,
		BankAccountID: line.BankAccountID, SourceDocumentID: line.SourceDocumentID,
		LineIndex: line.LineIndex, ExternalReference: line.ExternalReference,
		OperationDate: line.OperationDate, ValueDate: line.ValueDate, Direction: line.Direction,
		Amount: line.Amount, Currency: line.Currency, Description: "different evidence",
		CounterpartyName: line.CounterpartyName,
	}
	worker := &PcmWorker{statementLineStore: &statementLineStoreStub{existing: existing}}

	_, err = worker.persistCanonicalBankStatementLine(context.Background(), line)
	if err == nil || !strings.Contains(err.Error(), "HOLD_CANONICAL_BANK_LINE_CONFLICT") {
		t.Fatalf("expected conflicting replay hold, got %v", err)
	}
}

func testStatementDocument(documentID string) PcmParsedDocument {
	return PcmParsedDocument{
		DocumentID: documentID,
		Type:       "bank_statement",
		Data: map[string]interface{}{
			"account_number":   "0078 1000-0012 3456 7890 1234",
			"currency":         "MAD",
			"start_date":       "01/07/2026",
			"end_date":         "31/07/2026",
			"starting_balance": json.Number("100000.0000"),
			"ending_balance":   json.Number("88000.2500"),
			"transactions": map[string]interface{}{
				"1": map[string]interface{}{
					"date": "02/07/2026", "value_date": "03/07/2026",
					"description": "Supplier transfer", "amount": json.Number("12000.2500"),
					"type": "debit", "reference": "REF-1", "counterparty_name": "Supplier SARL",
				},
			},
		},
	}
}

func testPhysicalBankAccount(id, ledgerCode, currency string) database.ShadowErpBankAccount {
	return database.ShadowErpBankAccount{
		ID: testPGUUID(id), RealmID: "realm-a", BankName: "Bank", AccountNumber: "007810000012345678901234",
		LedgerAccountCode: ledgerCode, Currency: pgtype.Text{String: currency, Valid: currency != ""},
	}
}

func testPGUUID(value string) pgtype.UUID {
	parsed := uuid.MustParse(value)
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

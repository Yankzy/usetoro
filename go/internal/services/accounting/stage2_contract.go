package accounting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const Stage2SchemaVersion = "pcm.stage2.v1"

const (
	Stage2ProposedTreatment = "PROPOSED_ACCOUNTING_TREATMENT"
	Stage2HumanReview       = "HUMAN_REVIEW"
	Stage2HoldUnreliable    = "HOLD_UNRELIABLE_INPUT"
	Stage2HoldBankConfig    = "HOLD_BANK_ACCOUNT_CONFIGURATION"
	Stage2HoldAccountConfig = "HOLD_ACCOUNT_CONFIGURATION"
	Stage2HoldUnsupported   = "HOLD_UNSUPPORTED_TREATMENT"
)

// Stage2WorkflowContext is copied through the workflow so the proposal can be
// reviewed and posted without reconstructing tenant or source identity.
type Stage2WorkflowContext struct {
	EntityID                string   `json:"entity_id"`
	RealmID                 string   `json:"realm_id"`
	WorkflowID              string   `json:"workflow_id,omitempty"`
	WorkflowTraceID         string   `json:"workflow_trace_id,omitempty"`
	SessionID               string   `json:"session_id"`
	SourceDocumentIDs       []string `json:"source_document_ids"`
	StatementPeriodKey      string   `json:"statement_period_key"`
	StatementOpeningBalance string   `json:"statement_opening_balance"`
	StatementClosingBalance string   `json:"statement_closing_balance"`
}

type Stage2BankLine struct {
	StagingTransactionID  string `json:"staging_transaction_id"`
	BankStatementLineID   string `json:"bank_statement_line_id"`
	BankAccountID         string `json:"bank_account_id"`
	BankLedgerAccountCode string `json:"bank_ledger_account_code"`
	OperationDate         string `json:"operation_date"`
	Direction             string `json:"direction"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	Description           string `json:"description"`
}

type Stage2Classification struct {
	Intent     string  `json:"intent"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

type ProposedJournalLine struct {
	LineIndex            int    `json:"line_index"`
	AccountID            string `json:"account_id"`
	AccountCode          string `json:"account_code"`
	AuxiliaryAccountCode string `json:"auxiliary_account_code,omitempty"`
	Label                string `json:"label"`
	Debit                string `json:"debit"`
	Credit               string `json:"credit"`
}

type ProposedAccountingTreatment struct {
	EntryDate string                `json:"entry_date"`
	Currency  string                `json:"currency"`
	Label     string                `json:"label"`
	Lines     []ProposedJournalLine `json:"lines"`
}

// Stage2Output is the only supported terminal output from the PCM DAG. It is a
// proposal or hold, never a posting or reconciliation assertion.
type Stage2Output struct {
	SchemaVersion  string                       `json:"schema_version"`
	GeneratedAt    time.Time                    `json:"generated_at,omitempty"`
	Context        Stage2WorkflowContext        `json:"context"`
	BankLine       Stage2BankLine               `json:"bank_line"`
	Classification Stage2Classification         `json:"classification"`
	Outcome        string                       `json:"outcome"`
	ReviewReason   string                       `json:"review_reason,omitempty"`
	Treatment      *ProposedAccountingTreatment `json:"treatment,omitempty"`
}

func (o Stage2Output) Validate() error {
	if o.SchemaVersion != Stage2SchemaVersion {
		return fmt.Errorf("unsupported stage-2 schema %q", o.SchemaVersion)
	}
	if o.Context.EntityID == "" || o.Context.RealmID == "" || o.Context.SessionID == "" {
		return fmt.Errorf("stage-2 entity, realm, and session context are required")
	}
	if _, err := time.Parse("2006-01", o.Context.StatementPeriodKey); err != nil {
		return fmt.Errorf("valid statement period is required")
	}
	if _, err := NewReconciliationMoney(o.Context.StatementOpeningBalance); err != nil {
		return fmt.Errorf("valid statement opening balance is required")
	}
	if _, err := NewReconciliationMoney(o.Context.StatementClosingBalance); err != nil {
		return fmt.Errorf("valid statement closing balance is required")
	}
	if o.BankLine.StagingTransactionID == "" || o.BankLine.BankStatementLineID == "" || o.BankLine.BankAccountID == "" {
		return fmt.Errorf("stage-2 canonical bank-line identity is required")
	}
	if o.Classification.Intent == "" {
		return fmt.Errorf("stage-2 classification intent is required")
	}
	switch o.Outcome {
	case Stage2ProposedTreatment:
		if o.Treatment == nil {
			return fmt.Errorf("proposed treatment payload is required")
		}
		return validateProposedTreatment(o.BankLine, *o.Treatment)
	case Stage2HumanReview, Stage2HoldUnreliable, Stage2HoldBankConfig, Stage2HoldAccountConfig, Stage2HoldUnsupported:
		if o.Treatment != nil {
			return fmt.Errorf("%s cannot contain a proposed journal", o.Outcome)
		}
		if strings.TrimSpace(o.ReviewReason) == "" {
			return fmt.Errorf("%s requires a review reason", o.Outcome)
		}
		return nil
	default:
		return fmt.Errorf("unsupported stage-2 terminal outcome %q", o.Outcome)
	}
}

func validateProposedTreatment(bank Stage2BankLine, treatment ProposedAccountingTreatment) error {
	if bank.Direction != "INFLOW" && bank.Direction != "OUTFLOW" {
		return fmt.Errorf("invalid bank direction %q", bank.Direction)
	}
	if _, err := time.Parse("2006-01-02", bank.OperationDate); err != nil {
		return fmt.Errorf("invalid bank operation date: %w", err)
	}
	if treatment.EntryDate != bank.OperationDate {
		return fmt.Errorf("journal entry date must equal bank operation date")
	}
	if treatment.Currency == "" || treatment.Currency != bank.Currency {
		return fmt.Errorf("journal currency must equal bank-line currency")
	}
	bankAmount, err := NewReconciliationMoney(bank.Amount)
	if err != nil || bankAmount.value().Sign() <= 0 {
		return fmt.Errorf("invalid positive bank amount %q", bank.Amount)
	}
	if len(treatment.Lines) < 2 {
		return fmt.Errorf("a proposed journal requires at least two lines")
	}
	debits := MustReconciliationMoney("0")
	credits := MustReconciliationMoney("0")
	bankLines := 0
	for index, line := range treatment.Lines {
		if line.LineIndex != index || line.AccountID == "" || line.AccountCode == "" || strings.TrimSpace(line.Label) == "" {
			return fmt.Errorf("journal line %d has incomplete canonical account data", index)
		}
		debit, debitErr := NewReconciliationMoney(line.Debit)
		credit, creditErr := NewReconciliationMoney(line.Credit)
		if debitErr != nil || creditErr != nil || debit.value().Sign() < 0 || credit.value().Sign() < 0 || (debit.IsZero() == credit.IsZero()) {
			return fmt.Errorf("journal line %d must contain exact non-negative debit XOR credit", index)
		}
		debits = debits.Add(debit)
		credits = credits.Add(credit)
		if line.AccountCode == bank.BankLedgerAccountCode {
			bankLines++
			if bank.Direction == "INFLOW" && !debit.Equal(bankAmount) {
				return fmt.Errorf("inflow bank line must debit the exact bank amount")
			}
			if bank.Direction == "OUTFLOW" && !credit.Equal(bankAmount) {
				return fmt.Errorf("outflow bank line must credit the exact bank amount")
			}
		}
	}
	if bankLines != 1 {
		return fmt.Errorf("proposed journal must contain exactly one bank-ledger line")
	}
	if debits.IsZero() || !debits.Equal(credits) {
		return fmt.Errorf("proposed journal is not exactly balanced")
	}
	return nil
}

func (o Stage2Output) Hash() (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	canonical := o
	// Recording time belongs to the append-only proposal envelope in Postgres,
	// not the semantic content hash. Retries of identical evidence must match.
	canonical.GeneratedAt = time.Time{}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

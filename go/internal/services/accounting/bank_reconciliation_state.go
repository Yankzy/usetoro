package accounting

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

const reconciliationScale = 4

// ReconciliationMoney is a signed amount in 10^-4 currency units.
// It deliberately avoids float64 so equality checks are exact.
type ReconciliationMoney struct{ units *big.Int }

func NewReconciliationMoney(value string) (ReconciliationMoney, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ReconciliationMoney{}, fmt.Errorf("reconciliation amount is required")
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return ReconciliationMoney{}, fmt.Errorf("invalid reconciliation amount %q", value)
	}
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return ReconciliationMoney{}, fmt.Errorf("invalid reconciliation amount %q", value)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > reconciliationScale {
		return ReconciliationMoney{}, fmt.Errorf("amount %q exceeds 4 decimal places", value)
	}
	for len(fraction) < reconciliationScale {
		fraction += "0"
	}
	fractionUnits := big.NewInt(0)
	if fraction != "" {
		if _, ok := fractionUnits.SetString(fraction, 10); !ok {
			return ReconciliationMoney{}, fmt.Errorf("invalid reconciliation amount %q", value)
		}
	}
	units := whole.Mul(whole, big.NewInt(10000))
	units.Add(units, fractionUnits)
	if negative {
		units.Neg(units)
	}
	return ReconciliationMoney{units: units}, nil
}

func MustReconciliationMoney(value string) ReconciliationMoney {
	money, err := NewReconciliationMoney(value)
	if err != nil {
		panic(err)
	}
	return money
}

func (m ReconciliationMoney) String() string {
	if m.units == nil {
		return "0.0000"
	}
	negative := m.units.Sign() < 0
	units := new(big.Int).Abs(m.units)
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(units, big.NewInt(10000), fraction)
	value := fmt.Sprintf("%s.%04d", whole.String(), fraction.Int64())
	if negative {
		return "-" + value
	}
	return value
}

func (m ReconciliationMoney) Add(other ReconciliationMoney) ReconciliationMoney {
	return ReconciliationMoney{units: new(big.Int).Add(m.value(), other.value())}
}

func (m ReconciliationMoney) Sub(other ReconciliationMoney) ReconciliationMoney {
	return ReconciliationMoney{units: new(big.Int).Sub(m.value(), other.value())}
}

func (m ReconciliationMoney) Equal(other ReconciliationMoney) bool {
	return m.value().Cmp(other.value()) == 0
}
func (m ReconciliationMoney) IsZero() bool { return m.value().Sign() == 0 }
func (m ReconciliationMoney) value() *big.Int {
	if m.units == nil {
		return big.NewInt(0)
	}
	return m.units
}

// ClosingPosition contains the values that are snapshotted in a state header.
type ClosingPosition struct {
	StatementOpeningBalance ReconciliationMoney
	BookOpeningBalance      ReconciliationMoney
	BookBankBalance         ReconciliationMoney
	OutstandingBookInflows  ReconciliationMoney
	OutstandingBookOutflows ReconciliationMoney
	OutstandingBankNet      ReconciliationMoney
	ExpectedBankBalance     ReconciliationMoney
	StatementBalance        ReconciliationMoney
	Difference              ReconciliationMoney
}

// StateMembershipRef points to canonical evidence; it intentionally contains
// no copied financial attributes from the referenced record.
type StateMembershipRef struct {
	BankStatementLineID string
	JournalLineID       string
	Disposition         string
	CarryForward        bool
	MatchGroupID        string
	ReviewReason        string
}

func (m StateMembershipRef) CanonicalKey() (string, error) {
	if (m.BankStatementLineID == "") == (m.JournalLineID == "") {
		return "", fmt.Errorf("state membership must reference exactly one canonical bank or journal line")
	}
	if m.CarryForward && m.Disposition != "CARRIED_FORWARD" {
		return "", fmt.Errorf("carry-forward membership must have CARRIED_FORWARD disposition")
	}
	switch m.Disposition {
	case "RECONCILED", "UNRECONCILED", "CARRIED_FORWARD", "MISSING_EVIDENCE", "HUMAN_REVIEW":
	default:
		return "", fmt.Errorf("invalid reconciliation disposition %q", m.Disposition)
	}
	source := "bank:" + m.BankStatementLineID
	if m.JournalLineID != "" {
		source = "journal:" + m.JournalLineID
	}
	return strings.Join([]string{source, m.Disposition, fmt.Sprint(m.CarryForward), m.MatchGroupID, m.ReviewReason}, "|"), nil
}

// StateSnapshotInput is a normalized, persistence-independent state creation
// command. The database layer stores only the returned header and references.
type StateSnapshotInput struct {
	RealmID           string
	BankAccountID     string
	PeriodKey         string
	PreviousStateID   string
	SupersedesStateID string
	StateKind         string
	Revision          int32
	IdempotencyKey    string
	RequestHash       string
	Status            string
	Currency          string
	CreatedBy         string
	Position          ClosingPosition
	Memberships       []StateMembershipRef
}

type StateSnapshot struct {
	StateHash string
	Position  ClosingPosition
}

func BuildStateSnapshot(input StateSnapshotInput) (StateSnapshot, error) {
	if input.RealmID == "" || input.BankAccountID == "" || input.Currency == "" || input.CreatedBy == "" {
		return StateSnapshot{}, fmt.Errorf("realm, bank account, currency, and creator are required")
	}
	if len(input.PeriodKey) != len("2006-01") {
		return StateSnapshot{}, fmt.Errorf("period key must be YYYY-MM")
	}
	if _, err := time.Parse("2006-01", input.PeriodKey); err != nil {
		return StateSnapshot{}, fmt.Errorf("invalid period key %q: %w", input.PeriodKey, err)
	}
	if input.Status != "OPEN" && input.Status != "CLOSED" {
		return StateSnapshot{}, fmt.Errorf("invalid state status %q", input.Status)
	}
	if len(input.RequestHash) != 64 {
		return StateSnapshot{}, fmt.Errorf("state request hash must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(input.RequestHash); err != nil {
		return StateSnapshot{}, fmt.Errorf("invalid state request hash: %w", err)
	}
	if input.Status == "CLOSED" && !input.Position.Difference.IsZero() {
		return StateSnapshot{}, fmt.Errorf("cannot close reconciliation with difference %s", input.Position.Difference.String())
	}
	membershipKeys := make([]string, 0, len(input.Memberships))
	for _, membership := range input.Memberships {
		key, err := membership.CanonicalKey()
		if err != nil {
			return StateSnapshot{}, err
		}
		membershipKeys = append(membershipKeys, key)
	}
	header := []string{
		input.RealmID, input.BankAccountID, input.PeriodKey, fmt.Sprint(input.Revision),
		input.PreviousStateID, input.SupersedesStateID, input.StateKind, input.IdempotencyKey,
		input.RequestHash, input.Status, input.Currency, input.Position.StatementOpeningBalance.String(),
		input.Position.BookOpeningBalance.String(), input.Position.BookBankBalance.String(),
		input.Position.OutstandingBookInflows.String(), input.Position.OutstandingBookOutflows.String(),
		input.Position.OutstandingBankNet.String(),
		input.Position.ExpectedBankBalance.String(), input.Position.StatementBalance.String(),
		input.Position.Difference.String(),
	}
	return StateSnapshot{StateHash: StateHash(header, membershipKeys), Position: input.Position}, nil
}

func CalculateClosingPosition(bookBalance, bookInflows, bookOutflows, outstandingBankNet, statementBalance ReconciliationMoney) ClosingPosition {
	expected := bookBalance.Add(bookInflows).Sub(bookOutflows).Add(outstandingBankNet)
	return ClosingPosition{
		BookBankBalance:         bookBalance,
		OutstandingBookInflows:  bookInflows,
		OutstandingBookOutflows: bookOutflows,
		OutstandingBankNet:      outstandingBankNet,
		ExpectedBankBalance:     expected,
		StatementBalance:        statementBalance,
		Difference:              statementBalance.Sub(expected),
	}
}

// ReconciliationMovement is the minimum immutable evidence required for a
// deterministic V1 1:1 match.
type ReconciliationMovement struct {
	ID        string
	Amount    ReconciliationMoney
	Currency  string
	Direction string
	Date      time.Time
}

// IsEligibleOneToOneMatch compares canonical bank and book movements. Both
// sides use economic cash direction, so a bank credit and a debit to the bank
// ledger are represented as INFLOW.
func IsEligibleOneToOneMatch(bank, book ReconciliationMovement, windowDays int) bool {
	if bank.ID == "" || book.ID == "" || bank.Amount.IsZero() || book.Amount.IsZero() || windowDays < 0 {
		return false
	}
	if bank.Currency == "" || book.Currency == "" || bank.Currency != book.Currency {
		return false
	}
	if bank.Direction != book.Direction || (bank.Direction != "INFLOW" && bank.Direction != "OUTFLOW") {
		return false
	}
	if !bank.Amount.Equal(book.Amount) {
		return false
	}
	delta := bank.Date.Sub(book.Date)
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Duration(windowDays)*24*time.Hour
}

// StateHash makes state identity reproducible from normalized immutable values.
func StateHash(header []string, membershipIDs []string) string {
	values := append([]string(nil), header...)
	sort.Strings(membershipIDs)
	values = append(values, membershipIDs...)
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:])
}

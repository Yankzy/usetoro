package pcm_cash

import (
	"fmt"
	"strings"
	"time"
)

type InstrumentType string

const (
	InstrumentCheckReceived InstrumentType = "CHECK_RECEIVED" // Account 5111
	InstrumentCheckIssued   InstrumentType = "CHECK_ISSUED"   // Account 5143
	InstrumentBillReceivable InstrumentType = "BILL_RECEIVABLE" // Account 3425
	InstrumentBillPayable    InstrumentType = "BILL_PAYABLE"    // Account 4415
	InstrumentDiscountedBill InstrumentType = "DISCOUNTED_BILL" // Account 5520
)

type InstrumentStatus string

const (
	StatusPending InstrumentStatus = "PENDING"
	StatusCleared InstrumentStatus = "CLEARED"
	StatusBounced InstrumentStatus = "BOUNCED"
)

// PendingInstrument represents an uncleared check or trade bill.
type PendingInstrument struct {
	ID             string           `json:"id"`
	Type           InstrumentType   `json:"type"`
	ReferenceNum   string           `json:"reference_num"`
	Counterparty   string           `json:"counterparty"`
	Amount         float64          `json:"amount"`
	IssueDate      time.Time        `json:"issue_date"`
	ClearingDate   time.Time        `json:"clearing_date"`
	Status         InstrumentStatus `json:"status"`
	PendingAccount string           `json:"pending_account"`
	ClearedAccount string           `json:"cleared_account"`
}

// ProcessInstrumentBankClearing links a bank statement line to an uncleared check/bill ledger entry.
func ProcessInstrumentBankClearing(inst *PendingInstrument, bankTxDate time.Time, bankTxAmount float64) (*PendingInstrument, error) {
	if inst.Status == StatusCleared {
		return inst, fmt.Errorf("instrument ref %s is already cleared", inst.ReferenceNum)
	}

	if inst.Amount != bankTxAmount {
		return inst, fmt.Errorf("HOLD: INSTRUMENT_AMOUNT_MISMATCH: Bank amount (%.2f MAD) != Instrument face value (%.2f MAD)", bankTxAmount, inst.Amount)
	}

	inst.Status = StatusCleared
	inst.ClearingDate = bankTxDate
	return inst, nil
}

// DeterminePendingAccounts resolves statutory PCGM suspense account codes.
func DeterminePendingAccounts(instType InstrumentType) (pendingAcc string, clearedAcc string) {
	switch instType {
	case InstrumentCheckReceived:
		return "511100", "514100" // Chèques à encaisser -> Banque
	case InstrumentCheckIssued:
		return "514300", "514100" // Chèques à payer -> Banque
	case InstrumentBillReceivable:
		return "342500", "514100" // Effets à recevoir -> Banque
	case InstrumentBillPayable:
		return "441500", "514100" // Effets à payer -> Banque
	case InstrumentDiscountedBill:
		return "552000", "514100" // Crédit d'escompte -> Banque
	default:
		return "511100", "514100"
	}
}

// DetectInstrumentType identifies check or bill indicators in raw bank descriptions.
func DetectInstrumentType(desc string, isOutflow bool) (InstrumentType, bool) {
	upper := strings.ToUpper(desc)
	if strings.Contains(upper, "CHEQUE") || strings.Contains(upper, "CHQ") {
		if isOutflow {
			return InstrumentCheckIssued, true
		}
		return InstrumentCheckReceived, true
	}
	if strings.Contains(upper, "LCN") || strings.Contains(upper, "EFFET") || strings.Contains(upper, "LETTRE DE CHANGE") {
		if isOutflow {
			return InstrumentBillPayable, true
		}
		return InstrumentBillReceivable, true
	}
	return "", false
}

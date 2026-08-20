package enrichment

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type CashSupplierDayKey struct {
	RealmID      string
	SupplierName string
	DateStr      string // YYYY-MM-DD
}

type CashSupplierMonthKey struct {
	RealmID      string
	SupplierName string
	MonthStr     string // YYYY-MM
}

// CashGuardrailsEvaluator enforces statutory cash ceilings and caisse integrity.
type CashGuardrailsEvaluator struct {
	mu           sync.RWMutex
	dailyTotals  map[CashSupplierDayKey]float64
	monthlyTotals map[CashSupplierMonthKey]float64
	caisseBalance map[string]float64 // RealmID -> Balance in Compte 5161
}

// NewCashGuardrailsEvaluator creates a new evaluator.
func NewCashGuardrailsEvaluator() *CashGuardrailsEvaluator {
	return &CashGuardrailsEvaluator{
		dailyTotals:   make(map[CashSupplierDayKey]float64),
		monthlyTotals: make(map[CashSupplierMonthKey]float64),
		caisseBalance: make(map[string]float64),
	}
}

// SetCaisseBalance sets the current known balance for Petty Cash (Compte 5161).
func (cge *CashGuardrailsEvaluator) SetCaisseBalance(realmID string, balance float64) {
	cge.mu.Lock()
	defer cge.mu.Unlock()
	cge.caisseBalance[realmID] = balance
}

// EvaluateAndRecordCashPayment verifies compliance against statutory limits and updates running totals.
func (cge *CashGuardrailsEvaluator) EvaluateAndRecordCashPayment(
	ctx context.Context,
	realmID string,
	supplierName string,
	amountMAD float64,
	txnDate time.Time,
	isOutflow bool,
) StatutoryGuardrailsInfo {
	cge.mu.Lock()
	defer cge.mu.Unlock()

	dayStr := txnDate.Format("2006-01-02")
	monthStr := txnDate.Format("2006-01")

	dayKey := CashSupplierDayKey{
		RealmID:      realmID,
		SupplierName: supplierName,
		DateStr:      dayStr,
	}
	monthKey := CashSupplierMonthKey{
		RealmID:      realmID,
		SupplierName: supplierName,
		MonthStr:     monthStr,
	}

	curDaily := cge.dailyTotals[dayKey]
	curMonthly := cge.monthlyTotals[monthKey]
	curCaisse := cge.caisseBalance[realmID]

	info := StatutoryGuardrailsInfo{
		IsCashPayment:          true,
		DailyVendorCashTotal:   curDaily + amountMAD,
		MonthlyVendorCashTotal: curMonthly + amountMAD,
		GuardrailStatus:        GuardrailPass,
	}

	if isOutflow {
		// 1. Check Caisse Créditrice (Balance must not drop below 0 MAD)
		if curCaisse > 0 && (curCaisse-amountMAD) < 0 {
			info.GuardrailStatus = GuardrailBlockedCaisseCreditrice
			info.ViolationReason = fmt.Sprintf("Caisse Créditrice: Outflow of %.2f MAD exceeds current caisse balance of %.2f MAD", amountMAD, curCaisse)
			return info
		}

		// 2. Check Monthly Cap: 50,000 MAD TTC (CGI Art. 193/210)
		if (curMonthly + amountMAD) > 50000.00 {
			info.GuardrailStatus = GuardrailBlockedMonthlyLimit
			info.ViolationReason = fmt.Sprintf("CGI Art. 193/210 Violation: Monthly cash payments to '%s' (%.2f MAD) exceed 50,000 MAD TTC limit", supplierName, curMonthly+amountMAD)
			return info
		}

		// 3. Check Daily Cap: 5,000 MAD TTC (CGI Art. 193/210)
		if (curDaily + amountMAD) > 5000.00 {
			info.GuardrailStatus = GuardrailWarningDailyLimitExceeded
			info.ViolationReason = fmt.Sprintf("CGI Art. 193/210 Violation: Daily cash payments to '%s' (%.2f MAD) exceed 5,000 MAD TTC limit", supplierName, curDaily+amountMAD)
		}

		// Update running balances
		cge.dailyTotals[dayKey] = curDaily + amountMAD
		cge.monthlyTotals[monthKey] = curMonthly + amountMAD
		if curCaisse > 0 {
			cge.caisseBalance[realmID] = curCaisse - amountMAD
		}
	}

	return info
}

package pcm_cash

import (
	"errors"
	"fmt"
)

const (
	// MaxDailyCashPaymentPerSupplierTTC defines CGI Art. 193 daily cash payment limit (5,000 MAD TTC/day per supplier).
	MaxDailyCashPaymentPerSupplierTTC = 5000.00
	// MaxMonthlyCashPaymentPerSupplierTTC defines CGI Art. 193 monthly aggregate cash payment limit (50,000 MAD TTC/month per supplier).
	MaxMonthlyCashPaymentPerSupplierTTC = 50000.00
	PettyCashAccountCode                 = "5161"
)

var (
	ErrCaisseCreditricePrevented = errors.New("HOLD: CAISSE_CREDITRICE_PREVENTED: Petty cash balance (5161) cannot drop below 0 MAD")
	ErrDailyCashCapExceeded      = errors.New("WARN: DAILY_CASH_CAP_EXCEEDED: Supplier cash payments exceed 5,000 MAD TTC/day (CGI Art. 193)")
	ErrMonthlyCashCapExceeded    = errors.New("WARN: MONTHLY_CASH_CAP_EXCEEDED: Supplier aggregate cash payments exceed 50,000 MAD TTC/month (CGI Art. 193)")
)

// PettyCashGuardResult holds evaluation metrics for petty cash vouchers.
type PettyCashGuardResult struct {
	AccountCode           string  `json:"account_code"`
	CurrentBalance        float64 `json:"current_balance"`
	TransactionAmount     float64 `json:"transaction_amount"`
	ProjectedBalance      float64 `json:"projected_balance"`
	IsCaisseCreditrice    bool    `json:"is_caisse_creditrice"`
	CumulativeDailyCash   float64 `json:"cumulative_daily_cash"`
	CumulativeMonthlyCash float64 `json:"cumulative_monthly_cash"`
	IsCapExceeded         bool    `json:"is_cap_exceeded"`
	IsVATDeductible       bool    `json:"is_vat_deductible"`
	VatRestrictedAccount  string  `json:"vat_restricted_account"`
}

// EvaluatePettyCashVoucher checks running balance and CGI Art. 193 daily & monthly cash payment limits.
func EvaluatePettyCashVoucher(currentBalance float64, amountTTC float64, isOutflow bool, vendorDailyCashTotal float64, vendorMonthlyCashTotal ...float64) (*PettyCashGuardResult, error) {
	monthlyTotal := 0.0
	if len(vendorMonthlyCashTotal) > 0 {
		monthlyTotal = vendorMonthlyCashTotal[0]
	}

	res := &PettyCashGuardResult{
		AccountCode:           PettyCashAccountCode,
		CurrentBalance:        currentBalance,
		TransactionAmount:     amountTTC,
		CumulativeDailyCash:   vendorDailyCashTotal,
		CumulativeMonthlyCash: monthlyTotal,
		IsVATDeductible:       true,
	}

	if isOutflow {
		res.ProjectedBalance = currentBalance - amountTTC
		if res.ProjectedBalance < 0 {
			res.IsCaisseCreditrice = true
			return res, fmt.Errorf("%w (Current: %.2f MAD, Attempted Outflow: %.2f MAD, Projected: %.2f MAD)",
				ErrCaisseCreditricePrevented, currentBalance, amountTTC, res.ProjectedBalance)
		}

		newDailyTotal := vendorDailyCashTotal + amountTTC
		res.CumulativeDailyCash = newDailyTotal

		newMonthlyTotal := monthlyTotal + amountTTC
		res.CumulativeMonthlyCash = newMonthlyTotal

		if newDailyTotal > MaxDailyCashPaymentPerSupplierTTC || newMonthlyTotal > MaxMonthlyCashPaymentPerSupplierTTC {
			res.IsCapExceeded = true
			res.IsVATDeductible = false
			res.VatRestrictedAccount = "6181" // Non-deductible expense bucket
		}
	} else {
		// Cash Inflow replenishing petty cash box
		res.ProjectedBalance = currentBalance + amountTTC
	}

	return res, nil
}

package pcm_cash

import (
	"fmt"
	"math"
	"strings"
)

const (
	RASForeignServiceRate = 0.10 // 10% Foreign Services RAS (CGI Art. 15)
	RASCommercialRentRate = 0.05 // 5% Commercial Property Rent RAS (CGI Art. 160)
	RASTaxAccount         = "445800" // État, autres impôts, taxes et assimilés (RAS à payer)
)

type RASType string

const (
	RASTypeForeignService RASType = "FOREIGN_SERVICE" // 10%
	RASTypeCommercialRent RASType = "COMMERCIAL_RENT" // 5%
)

// RASTaxCalculationResult details the statutory withholding split triggered at cash outflow timestamp.
type RASTaxCalculationResult struct {
	RASType           RASType `json:"ras_type"`
	GrossExpense      float64 `json:"gross_expense"`
	RASRate           float64 `json:"ras_rate"`
	WithholdingAmount float64 `json:"withholding_amount"`
	NetBankOutflow    float64 `json:"net_bank_outflow"`
	ExpenseAccount    string  `json:"expense_account"`
	WithholdingAccount string `json:"withholding_account"`
	BankAccount       string  `json:"bank_account"`
	Currency          string  `json:"currency"`
}

// CalculateRASWithholding evaluates cash outflow transactions and computes 10% foreign service or 5% rent RAS.
func CalculateRASWithholding(grossAmount float64, rasType RASType, currency string) (*RASTaxCalculationResult, error) {
	if grossAmount <= 0 {
		return nil, fmt.Errorf("invalid gross amount for RAS calculation: %.2f", grossAmount)
	}

	rate := RASForeignServiceRate
	expenseAccount := "614000" // Charges externes non-résidentes

	if rasType == RASTypeCommercialRent {
		rate = RASCommercialRentRate
		expenseAccount = "613100" // Locations immobilières
	} else if rasType == RASTypeForeignService {
		rate = RASForeignServiceRate
		expenseAccount = "613600" // Remunérations d'intermédiaires et honoraires étrangers / SaaS
	}

	withholding := math.Round((grossAmount*rate)*100) / 100
	netBank := math.Round((grossAmount-withholding)*100) / 100

	return &RASTaxCalculationResult{
		RASType:            rasType,
		GrossExpense:       grossAmount,
		RASRate:            rate,
		WithholdingAmount:  withholding,
		NetBankOutflow:     netBank,
		ExpenseAccount:     expenseAccount,
		WithholdingAccount: RASTaxAccount,
		BankAccount:        "514100",
		Currency:           currency,
	}, nil
}

// IsForeignVendorDescription checks if vendor/description indicates foreign SaaS/services subject to 10% RAS.
func IsForeignVendorDescription(desc string) bool {
	upper := strings.ToUpper(desc)
	foreignKeywords := []string{
		"AWS", "AMAZON WEB SERVICES", "GOOGLE CLOUD", "GITHUB", "HEROKU",
		"MICROSOFT AZURE", "STRIPE", "VERCEL", "OPENAI", "SLACK",
		"NOTION", "ZOOM", "LINKEDIN", "META ADS", "GOOGLE ADS",
	}
	for _, kw := range foreignKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

package pcm_cash

import (
	"fmt"
	"math"
	"strings"
)

const StatutoryBankFeeVATRate = 0.10 // 10% statutory VAT under CGI Art. 89

// BankFeeSplitResult details the accounting breakdown for bank fees and agios.
type BankFeeSplitResult struct {
	AmountTTC      float64 `json:"amount_ttc"`
	AmountHT       float64 `json:"amount_ht"`
	AmountVAT      float64 `json:"amount_vat"`
	VATRate        float64 `json:"vat_rate"`
	ExpenseAccount string  `json:"expense_account"` // 6147 for services/commissions, 6311 for agios/interest
	VATAccount     string  `json:"vat_account"`     // 34552 (TVA récupérable sur charges)
	BankAccount    string  `json:"bank_account"`    // 5141 (Banque)
	IsAgios        bool    `json:"is_agios"`
}

var BankFeeKeywords = []string{
	"COMMISSION", "COMMISSIONS", "AGIO", "AGIOS",
	"FRAIS DE TENUE", "FRAIS TENUE", "FRAIS DOSSIER",
	"FRAIS D'EFFET", "FRAIS EFFET", "FRAIS VIREMENT",
	"COTISATION CARTE", "TENUE DE COMPTE",
}

// IsBankFeeDescription checks if a transaction line matches bank fee or agios keywords.
func IsBankFeeDescription(desc string) bool {
	upper := strings.ToUpper(desc)
	for _, kw := range BankFeeKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// SplitBankFee computes exact HT and 10% VAT splits for Moroccan bank fees and agios.
func SplitBankFee(amountTTC float64, desc string) (*BankFeeSplitResult, error) {
	if amountTTC <= 0 {
		return nil, fmt.Errorf("invalid bank fee amount: %.2f", amountTTC)
	}

	upper := strings.ToUpper(desc)
	isAgios := strings.Contains(upper, "AGIO") || strings.Contains(upper, "INTERET") || strings.Contains(upper, "INTÉRÊT")

	expenseAccount := "614700" // Services bancaires
	if isAgios {
		expenseAccount = "631100" // Intérêts des emprunts et dettes
	}

	amountHT := math.Round((amountTTC/1.10)*100) / 100
	amountVAT := math.Round((amountTTC-amountHT)*100) / 100

	return &BankFeeSplitResult{
		AmountTTC:      amountTTC,
		AmountHT:       amountHT,
		AmountVAT:      amountVAT,
		VATRate:        StatutoryBankFeeVATRate,
		ExpenseAccount: expenseAccount,
		VATAccount:     "345520",
		BankAccount:    "514100",
		IsAgios:        isAgios,
	}, nil
}

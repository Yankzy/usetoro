package pcm_cash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/xuri/excelize/v2"
)

// ExportRecord represents a normalized transaction record for Moroccan cash bookkeeping export.
type ExportRecord struct {
	ID                 string
	DateStr            string
	RawDescription     string
	Amount             float64
	Direction          string // "INFLOW" or "OUTFLOW"
	AccountCode        string
	AuxiliaryCode      string
	Counterparty       string
	StatementType      string // "BANK_STATEMENT" or "PETTY_CASH"
	MoroccanEnrichment []byte
	TaxRuleCode        string
	DocumentRequired   string
	Instruction        string
	ComplianceStatus   string
	HoldReason         string
}

// GenerateMoroccanBookkeepingWorkbook produces a 4-tab styled Excel workbook (.xlsx):
// Tab 1: "Journal des Écritures" (Double-entry PCGM with HT/TVA/RAS splits)
// Tab 2: "Pièces Manquantes & Actions" (Human-in-the-loop annotations)
// Tab 3: "SIMPL-TVA (DGI Ready)" (6 mandatory cash VAT fields)
// Tab 4: "Synthèse Fiscale" (CGI audit KPIs & compliance summary)
func GenerateMoroccanBookkeepingWorkbook(records []ExportRecord) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	// Styles
	styleJournalHeader, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11, Family: "Calibri"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1F4E78"}, Pattern: 1}, // Dark Blue
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	styleActionHeader, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11, Family: "Calibri"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"C0504D"}, Pattern: 1}, // Coral / Red
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	styleSIMPLHeader, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11, Family: "Calibri"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"385723"}, Pattern: 1}, // Forest Green
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	styleSummaryHeader, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11, Family: "Calibri"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"334155"}, Pattern: 1}, // Slate
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})

	styleNumber, _ := f.NewStyle(&excelize.Style{
		CustomNumFmt: &[]string{"#,##0.00;[Red]-#,##0.00;\"0.00\""}[0],
		Alignment:    &excelize.Alignment{Horizontal: "right"},
	})
	styleCenter, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	styleWarningRow, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Color: []string{"FEF3C7"}, Pattern: 1}, // Light Yellow
	})
	styleActionRow, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Color: []string{"FEE2E2"}, Pattern: 1}, // Light Red
	})
	styleGreenBadge, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "166534"},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	styleRedBadge, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "991B1B"},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})

	// ─── TAB 1: Journal des Écritures ─────────────────────────────────────────
	const sheetJournal = "Journal des Écritures"
	f.SetSheetName("Sheet1", sheetJournal)

	journalHeaders := []string{
		"N° Pièce", "Date", "Journal", "Compte Général", "Intitulé du Compte",
		"Compte Auxiliaire", "Libellé de l'Écriture", "Débit (MAD)", "Crédit (MAD)",
		"Montant HT", "TVA Déductible", "Tiers / Bénéficiaire", "Statut",
	}
	writeExcelHeader(f, sheetJournal, journalHeaders, styleJournalHeader)

	journalRowNum := 2
	var totalDebit, totalCredit float64
	var totalBankVAT, totalRAS, totalNonDeductible float64
	caisseRunningBalance := 0.0
	actionRequiredCount := 0

	for itemIdx, rec := range records {
		piece := fmt.Sprintf("BNK%03d", itemIdx+1)
		journalCode := "BQ"
		if rec.StatementType == "PETTY_CASH" {
			piece = fmt.Sprintf("CSH%03d", itemIdx+1)
			journalCode = "CA"
		}

		isOutflow := rec.Direction != "INFLOW"
		_ = isOutflow
		ann := AnnotationFromPayload(rec)
		if ann.ComplianceStatus != StatusCompliant {
			actionRequiredCount++
		}

		var env *enrichment.AnnotatedMoroccanTransactionEnvelope
		if len(rec.MoroccanEnrichment) > 0 && string(rec.MoroccanEnrichment) != "{}" {
			var parsed enrichment.AnnotatedMoroccanTransactionEnvelope
			if err := json.Unmarshal(rec.MoroccanEnrichment, &parsed); err == nil {
				env = &parsed
			}
		}

		// A. Bank Fee Auto-Split (10% VAT, CGI Art. 89)
		if IsBankFeeDescription(rec.RawDescription) && isOutflow {
			split, _ := SplitBankFee(rec.Amount, rec.RawDescription)
			if split == nil {
				split = &BankFeeSplitResult{
					AmountTTC: rec.Amount, AmountHT: math.Round((rec.Amount/1.10)*100) / 100,
					AmountVAT: math.Round((rec.Amount-(rec.Amount/1.10))*100) / 100,
					ExpenseAccount: "614700", VATAccount: "345520", BankAccount: "514100",
				}
			}
			totalBankVAT += split.AmountVAT

			// Line 1: Expense HT
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), split.ExpenseAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(split.ExpenseAccount))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription+" (Frais HT)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), split.AmountHT)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), split.AmountHT)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "AUTO-VENTILÉ (10% TVA)")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: VAT 10%
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), split.VATAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "TVA récupérable sur charges (10%)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "TVA 10% sur commissions bancaires")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), split.AmountVAT)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), split.AmountVAT)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "AUTO-VENTILÉ")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 3: Bank Credit TTC
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), split.BankAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "Banques (Solde crédité)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Débit bancaire commissions")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), split.AmountTTC)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RÈGLEMENT")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += split.AmountHT + split.AmountVAT
			totalCredit += split.AmountTTC
			continue
		}

		// B. Foreign SaaS / Services with 10% RAS (CGI Art. 15)
		if (IsForeignVendorDescription(rec.RawDescription) || (env != nil && env.Counterparty.IsForeignService)) && isOutflow {
			rasRes, _ := CalculateRASWithholding(rec.Amount, RASTypeForeignService, "MAD")
			if rasRes == nil {
				rasRes = &RASTaxCalculationResult{
					GrossExpense: rec.Amount, WithholdingAmount: math.Round(rec.Amount*0.10*100) / 100,
					NetBankOutflow: math.Round(rec.Amount*0.90*100) / 100,
					ExpenseAccount: "613600", WithholdingAccount: "445800", BankAccount: "514100",
				}
			}
			totalRAS += rasRes.WithholdingAmount

			// Line 1: Gross Expense
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.ExpenseAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(rasRes.ExpenseAccount))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription+" (Brut)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), rasRes.GrossExpense)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), rasRes.GrossExpense)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RAS 10% APPLIQUÉE")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: RAS 10% Withholding Liability Credit
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.WithholdingAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "État, RAS sur services étrangers à payer (10%)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Retenue à la source 10% CGI Art. 15")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rasRes.WithholdingAmount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RAS 10%")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 3: Net Bank Outflow
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.BankAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "Banques (Virement net)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Virement net fournisseur non-résident")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rasRes.NetBankOutflow)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RÈGLEMENT NET")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += rasRes.GrossExpense
			totalCredit += rasRes.WithholdingAmount + rasRes.NetBankOutflow
			continue
		}

		// C. Commercial Rent with 5% RAS (CGI Art. 160)
		if (strings.HasPrefix(rec.AccountCode, "6131") || strings.Contains(strings.ToUpper(rec.RawDescription), "LOYER")) && isOutflow {
			rasRes, _ := CalculateRASWithholding(rec.Amount, RASTypeCommercialRent, "MAD")
			if rasRes == nil {
				rasRes = &RASTaxCalculationResult{
					GrossExpense: rec.Amount, WithholdingAmount: math.Round(rec.Amount*0.05*100) / 100,
					NetBankOutflow: math.Round(rec.Amount*0.95*100) / 100,
					ExpenseAccount: "613100", WithholdingAccount: "445800", BankAccount: "514100",
				}
			}
			totalRAS += rasRes.WithholdingAmount

			// Line 1: Gross Rent
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.ExpenseAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(rasRes.ExpenseAccount))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription+" (Loyer Brut)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), rasRes.GrossExpense)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), rasRes.GrossExpense)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RAS 5% LOYER")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: RAS 5%
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.WithholdingAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "État, RAS sur loyers commerciaux (5%)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Retenue à la source 5% CGI Art. 160")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rasRes.WithholdingAmount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RAS 5%")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 3: Net Bank Outflow
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), rasRes.BankAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "Banques (Loyer net versé)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Règlement net propriétaire")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rasRes.NetBankOutflow)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RÈGLEMENT NET")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += rasRes.GrossExpense
			totalCredit += rasRes.WithholdingAmount + rasRes.NetBankOutflow
			continue
		}

		// D. Petty Cash Outflow Cap Violation (CGI Art. 193 > 5,000 DH)
		if (rec.StatementType == "PETTY_CASH" || strings.HasPrefix(rec.AccountCode, "5161")) && isOutflow && rec.Amount > MaxDailyCashPaymentPerSupplierTTC {
			totalNonDeductible += rec.Amount
			// Line 1: Non-deductible expense
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), "618100")
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "Charges non déductibles (Dépassement espèces CGI Art. 193)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription+" (Espèces > 5000 DH)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "NON DÉDUCTIBLE (CGI 193)")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: Petty Cash Credit
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), "516100")
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), "Caisse (Décaissement)")
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Sortie d'espèces caisse")
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "CAISSE")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += rec.Amount
			totalCredit += rec.Amount
			caisseRunningBalance -= rec.Amount
			continue
		}

		// E. Standard Operating Outflow (Debit Class 6, Credit 5141/5161)
		if isOutflow {
			account := rec.AccountCode
			if account == "" {
				account = "619000"
			}
			treasuryAccount := "514100"
			if rec.StatementType == "PETTY_CASH" {
				treasuryAccount = "516100"
				caisseRunningBalance -= rec.Amount
			}

			// Line 1: Charge
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), account)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(account))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription)
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), string(ann.ComplianceStatus))
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: Treasury Account Credit
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), treasuryAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(treasuryAccount))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Règlement "+rec.RawDescription)
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "RÈGLEMENT")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += rec.Amount
			totalCredit += rec.Amount
			continue
		}

		// F. Standard Operating Inflow (Debit 5141/5161, Credit 3421/Class 7)
		if !isOutflow {
			account := rec.AccountCode
			if account == "" || account == "471000" {
				account = "342100"
			}
			treasuryAccount := "514100"
			if rec.StatementType == "PETTY_CASH" {
				treasuryAccount = "516100"
				caisseRunningBalance += rec.Amount
			}

			// Line 1: Treasury Debit
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), treasuryAccount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(treasuryAccount))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), "")
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "Encaissement "+rec.RawDescription)
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), "ENCAISSEMENT")
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			// Line 2: Client Credit
			f.SetCellValue(sheetJournal, fmt.Sprintf("A%d", journalRowNum), piece)
			f.SetCellValue(sheetJournal, fmt.Sprintf("B%d", journalRowNum), rec.DateStr)
			f.SetCellValue(sheetJournal, fmt.Sprintf("C%d", journalRowNum), journalCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("D%d", journalRowNum), account)
			f.SetCellValue(sheetJournal, fmt.Sprintf("E%d", journalRowNum), getAccountLabel(account))
			f.SetCellValue(sheetJournal, fmt.Sprintf("F%d", journalRowNum), rec.AuxiliaryCode)
			f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), rec.RawDescription)
			f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), rec.Amount)
			f.SetCellValue(sheetJournal, fmt.Sprintf("J%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("K%d", journalRowNum), 0.00)
			f.SetCellValue(sheetJournal, fmt.Sprintf("L%d", journalRowNum), rec.Counterparty)
			f.SetCellValue(sheetJournal, fmt.Sprintf("M%d", journalRowNum), string(ann.ComplianceStatus))
			setJournalRowStyles(f, sheetJournal, journalRowNum, styleCenter, styleNumber)
			journalRowNum++

			totalDebit += rec.Amount
			totalCredit += rec.Amount
		}
	}

	// Journal Total Row
	f.SetCellValue(sheetJournal, fmt.Sprintf("G%d", journalRowNum), "TOTAL GÉNÉRAL JOURNAL (ÉQUILIBRÉ)")
	f.SetCellValue(sheetJournal, fmt.Sprintf("H%d", journalRowNum), totalDebit)
	f.SetCellValue(sheetJournal, fmt.Sprintf("I%d", journalRowNum), totalCredit)
	f.SetCellStyle(sheetJournal, fmt.Sprintf("G%d", journalRowNum), fmt.Sprintf("I%d", journalRowNum), styleJournalHeader)
	autoFitColumns(f, sheetJournal, len(journalHeaders))

	// ─── TAB 2: Pièces Manquantes & Actions ───────────────────────────────────
	const sheetAction = "Pièces Manquantes & Actions"
	f.NewSheet(sheetAction)

	actionHeaders := []string{
		"N° Réf", "Date", "Montant TTC (MAD)", "Bénéficiaire / Tiers", "Statut Conformité",
		"Pièce Justificative Requise", "Règle Fiscale (CGI / PCGM)",
		"Instruction pour le Comptable / Client", "Zone de Saisie / Note du Comptable",
	}
	writeExcelHeader(f, sheetAction, actionHeaders, styleActionHeader)

	actionRowNum := 2
	for itemIdx, rec := range records {
		ann := AnnotationFromPayload(rec)

		piece := fmt.Sprintf("REF-%03d", itemIdx+1)
		statusBadge := "✅ CONFORME"
		rowStyle := 0

		if ann.ComplianceStatus == StatusActionRequired {
			statusBadge = "⚠️ ACTION REQUISE"
			rowStyle = styleActionRow
		} else if ann.ComplianceStatus == StatusWarning {
			statusBadge = "⚠️ ALERTE FISCALE"
			rowStyle = styleWarningRow
		}

		f.SetCellValue(sheetAction, fmt.Sprintf("A%d", actionRowNum), piece)
		f.SetCellValue(sheetAction, fmt.Sprintf("B%d", actionRowNum), rec.DateStr)
		f.SetCellValue(sheetAction, fmt.Sprintf("C%d", actionRowNum), rec.Amount)
		f.SetCellValue(sheetAction, fmt.Sprintf("D%d", actionRowNum), rec.Counterparty)
		f.SetCellValue(sheetAction, fmt.Sprintf("E%d", actionRowNum), statusBadge)
		f.SetCellValue(sheetAction, fmt.Sprintf("F%d", actionRowNum), ann.DocumentRequired)
		f.SetCellValue(sheetAction, fmt.Sprintf("G%d", actionRowNum), ann.TaxRuleCode)
		f.SetCellValue(sheetAction, fmt.Sprintf("H%d", actionRowNum), ann.Instruction)
		f.SetCellValue(sheetAction, fmt.Sprintf("I%d", actionRowNum), "") // Editable blank for human bookkeeper

		if rowStyle != 0 {
			f.SetCellStyle(sheetAction, fmt.Sprintf("A%d", actionRowNum), fmt.Sprintf("I%d", actionRowNum), rowStyle)
		}
		f.SetCellStyle(sheetAction, fmt.Sprintf("C%d", actionRowNum), fmt.Sprintf("C%d", actionRowNum), styleNumber)
		f.SetCellStyle(sheetAction, fmt.Sprintf("A%d", actionRowNum), fmt.Sprintf("B%d", actionRowNum), styleCenter)
		f.SetCellStyle(sheetAction, fmt.Sprintf("E%d", actionRowNum), fmt.Sprintf("E%d", actionRowNum), styleCenter)

		actionRowNum++
	}
	autoFitColumns(f, sheetAction, len(actionHeaders))

	// ─── TAB 3: SIMPL-TVA (DGI Ready) ─────────────────────────────────────────
	const sheetSIMPL = "SIMPL-TVA (DGI Ready)"
	f.NewSheet(sheetSIMPL)

	simplHeaders := []string{
		"N° Ordre", "Date Règlement", "Mode de Règlement", "N° Facture / Pièce",
		"Nom Fournisseur", "ICE Fournisseur (15 chiffres)", "Montant HT (MAD)",
		"Taux TVA", "Montant TVA (MAD)", "Montant TTC (MAD)", "Validité Déclaration DGI",
	}
	writeExcelHeader(f, sheetSIMPL, simplHeaders, styleSIMPLHeader)

	simplRowNum := 2
	for itemIdx, rec := range records {
		ann := AnnotationFromPayload(rec)

		paymentMode := "VIREMENT"
		if rec.StatementType == "PETTY_CASH" {
			paymentMode = "ESPECES"
		} else if strings.Contains(strings.ToUpper(rec.RawDescription), "CHQ") || strings.Contains(strings.ToUpper(rec.RawDescription), "CHEQUE") {
			paymentMode = "CHEQUE"
		} else if strings.Contains(strings.ToUpper(rec.RawDescription), "CB") || strings.Contains(strings.ToUpper(rec.RawDescription), "CARTE") {
			paymentMode = "CARTE"
		}

		htAmount := math.Round((rec.Amount/1.20)*100) / 100
		tvaAmount := math.Round((rec.Amount-htAmount)*100) / 100
		tvaRate := 0.20

		if IsBankFeeDescription(rec.RawDescription) {
			htAmount = math.Round((rec.Amount/1.10)*100) / 100
			tvaAmount = math.Round((rec.Amount-htAmount)*100) / 100
			tvaRate = 0.10
		}

		dgiValidity := "VALIDE (Prêt pour XML)"
		if !ann.HasValidICE {
			dgiValidity = "INCOMPLET (ICE Manquant)"
		}

		f.SetCellValue(sheetSIMPL, fmt.Sprintf("A%d", simplRowNum), itemIdx+1)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("B%d", simplRowNum), rec.DateStr)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("C%d", simplRowNum), paymentMode)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("D%d", simplRowNum), fmt.Sprintf("FAC-%03d", itemIdx+1))
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("E%d", simplRowNum), rec.Counterparty)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("F%d", simplRowNum), ann.ExtractedICE)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("G%d", simplRowNum), htAmount)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("H%d", simplRowNum), fmt.Sprintf("%.0f%%", tvaRate*100))
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("I%d", simplRowNum), tvaAmount)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("J%d", simplRowNum), rec.Amount)
		f.SetCellValue(sheetSIMPL, fmt.Sprintf("K%d", simplRowNum), dgiValidity)

		f.SetCellStyle(sheetSIMPL, fmt.Sprintf("G%d", simplRowNum), fmt.Sprintf("G%d", simplRowNum), styleNumber)
		f.SetCellStyle(sheetSIMPL, fmt.Sprintf("I%d", simplRowNum), fmt.Sprintf("J%d", simplRowNum), styleNumber)
		f.SetCellStyle(sheetSIMPL, fmt.Sprintf("A%d", simplRowNum), fmt.Sprintf("C%d", simplRowNum), styleCenter)
		f.SetCellStyle(sheetSIMPL, fmt.Sprintf("F%d", simplRowNum), fmt.Sprintf("F%d", simplRowNum), styleCenter)
		f.SetCellStyle(sheetSIMPL, fmt.Sprintf("H%d", simplRowNum), fmt.Sprintf("H%d", simplRowNum), styleCenter)

		if ann.HasValidICE {
			f.SetCellStyle(sheetSIMPL, fmt.Sprintf("K%d", simplRowNum), fmt.Sprintf("K%d", simplRowNum), styleGreenBadge)
		} else {
			f.SetCellStyle(sheetSIMPL, fmt.Sprintf("K%d", simplRowNum), fmt.Sprintf("K%d", simplRowNum), styleRedBadge)
		}

		simplRowNum++
	}
	autoFitColumns(f, sheetSIMPL, len(simplHeaders))

	// ─── TAB 4: Synthèse Fiscale ──────────────────────────────────────────────
	const sheetSummary = "Synthèse Fiscale"
	f.NewSheet(sheetSummary)

	summaryHeaders := []string{"Indicateur de Contrôle Fiscal (CGI & PCGM)", "Valeur Constatée", "Statut / Règle Légale"}
	writeExcelHeader(f, sheetSummary, summaryHeaders, styleSummaryHeader)

	automationPct := 100.0
	if len(records) > 0 {
		automationPct = float64(len(records)-actionRequiredCount) / float64(len(records)) * 100
	}

	caisseStatus := "✅ Conforme (Solde Créditeur Évité)"
	if caisseRunningBalance < 0 {
		caisseStatus = "⚠️ ALERTE CAISSE CRÉDITRICE (CGI Art. 210/145)"
	}

	kpis := [][]string{
		{"Total des Lignes Traitées", strconv.Itoa(len(records)), "100% Ingestion"},
		{"Taux de Pré-Automatisation IA", fmt.Sprintf("%.1f%%", automationPct), "90% Travail Pré-Rempli"},
		{"Lignes Requérant Attention Comptable", strconv.Itoa(actionRequiredCount), "À compléter sur Tab 2"},
		{"Total Débit Journal (MAD)", fmt.Sprintf("%.2f DH", totalDebit), "Équilibre Débit == Crédit"},
		{"Total Crédit Journal (MAD)", fmt.Sprintf("%.2f DH", totalCredit), "Équilibre Débit == Crédit"},
		{"Total TVA 10% Commissions Bancaires (345520)", fmt.Sprintf("%.2f DH", totalBankVAT), "CGI Art. 89 (Auto-ventilée)"},
		{"Total Retenues à la Source à Déclarer (445800)", fmt.Sprintf("%.2f DH", totalRAS), "CGI Art. 15 (10%) & Art. 160 (5%)"},
		{"Total Charges Non Déductibles (618100)", fmt.Sprintf("%.2f DH", totalNonDeductible), "CGI Art. 193 (>5000 DH espèces)"},
		{"Variation Nette Caisse 5161 (MAD)", fmt.Sprintf("%.2f DH", caisseRunningBalance), caisseStatus},
		{"Généré le", time.Now().Format("02/01/2006 15:04"), "Moteur Comptable Toro PCM"},
	}

	for i, row := range kpis {
		f.SetCellValue(sheetSummary, fmt.Sprintf("A%d", i+2), row[0])
		f.SetCellValue(sheetSummary, fmt.Sprintf("B%d", i+2), row[1])
		f.SetCellValue(sheetSummary, fmt.Sprintf("C%d", i+2), row[2])
		f.SetCellStyle(sheetSummary, fmt.Sprintf("B%d", i+2), fmt.Sprintf("B%d", i+2), styleCenter)
	}
	autoFitColumns(f, sheetSummary, len(summaryHeaders))

	buf := new(bytes.Buffer)
	if err := f.Write(buf); err != nil {
		return nil, fmt.Errorf("failed to write excel buffer: %w", err)
	}

	return buf.Bytes(), nil
}

func writeExcelHeader(f *excelize.File, sheet string, headers []string, style int) {
	for i, h := range headers {
		cell := fmt.Sprintf("%s1", colLetter(i+1))
		f.SetCellValue(sheet, cell, h)
		if style != 0 {
			f.SetCellStyle(sheet, cell, cell, style)
		}
	}
}

func setJournalRowStyles(f *excelize.File, sheet string, rowNum int, centerStyle int, numberStyle int) {
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", rowNum), fmt.Sprintf("C%d", rowNum), centerStyle)
	f.SetCellStyle(sheet, fmt.Sprintf("D%d", rowNum), fmt.Sprintf("D%d", rowNum), centerStyle)
	f.SetCellStyle(sheet, fmt.Sprintf("H%d", rowNum), fmt.Sprintf("K%d", rowNum), numberStyle)
	f.SetCellStyle(sheet, fmt.Sprintf("M%d", rowNum), fmt.Sprintf("M%d", rowNum), centerStyle)
}

func colLetter(n int) string {
	name := ""
	for n > 0 {
		n--
		name = string(rune('A'+n%26)) + name
		n /= 26
	}
	return name
}

func autoFitColumns(f *excelize.File, sheet string, count int) {
	for i := 1; i <= count; i++ {
		col := colLetter(i)
		f.SetColWidth(sheet, col, col, 22)
	}
}

func getAccountLabel(accountCode string) string {
	switch {
	case strings.HasPrefix(accountCode, "6147"):
		return "Services bancaires (Commissions)"
	case strings.HasPrefix(accountCode, "6311"):
		return "Intérêts des emprunts et dettes (Agios)"
	case strings.HasPrefix(accountCode, "34552"):
		return "État - TVA récupérable sur charges"
	case strings.HasPrefix(accountCode, "34551"):
		return "État - TVA récupérable sur immobilisations"
	case strings.HasPrefix(accountCode, "4458"):
		return "État - Autres impôts, taxes et assimilés (RAS)"
	case strings.HasPrefix(accountCode, "6131"):
		return "Locations et charges locatives (Loyers)"
	case strings.HasPrefix(accountCode, "6136"):
		return "Rémunérations d'intermédiaires et honoraires étrangers"
	case strings.HasPrefix(accountCode, "6181"):
		return "Charges non déductibles (CGI Art. 193)"
	case strings.HasPrefix(accountCode, "5141"):
		return "Banques (Compte principal)"
	case strings.HasPrefix(accountCode, "5161"):
		return "Caisse centrale"
	case strings.HasPrefix(accountCode, "5115"):
		return "Virements de fonds (Compte de passage)"
	case strings.HasPrefix(accountCode, "3421"):
		return "Clients"
	case strings.HasPrefix(accountCode, "4411"):
		return "Fournisseurs"
	default:
		return "Charge / Produit d'exploitation"
	}
}

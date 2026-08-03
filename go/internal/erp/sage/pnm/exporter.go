package pnm

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// JournalEntryLine represents a single line item in a Sage accounting journal entry
type JournalEntryLine struct {
	JournalCode string    // e.g. "ACH", "BQ1"
	Date        time.Time // Entry Date
	GeneralAcc  string    // e.g. "61440000", "34552000", "44110000"
	AuxAcc      string    // e.g. "IAM001" or "44110042" (Sage CT_Num)
	PieceRef    string    // Facture or BL Number (RefPiece)
	Libelle     string    // Description
	Debit       float64   // Debit Amount
	Credit      float64   // Credit Amount
}

// TemplateConfig defines the formatting options for Sage export files (.PNM / .CSV)
type TemplateConfig struct {
	Delimiter  string   // Default ";"
	DateFormat string   // Default "020106" (DDMMYY)
	Columns    []string // Default column order
	HasHeader  bool     // Whether to output header line
}

// DefaultTemplate returns the standard Sage 100 Paramétrable (.PNM) format config
func DefaultTemplate() TemplateConfig {
	return TemplateConfig{
		Delimiter:  ";",
		DateFormat: "020106",
		Columns: []string{
			"journal_code",
			"date",
			"general_account",
			"auxiliary_account",
			"piece_ref",
			"libelle",
			"debit",
			"credit",
		},
		HasHeader: false,
	}
}

// FormatPNM converts balanced JournalEntryLine records into a Sage 100 import payload
func FormatPNM(lines []JournalEntryLine, cfg TemplateConfig) []byte {
	if cfg.Delimiter == "" {
		cfg.Delimiter = ";"
	}
	if cfg.DateFormat == "" {
		cfg.DateFormat = "020106"
	}
	if len(cfg.Columns) == 0 {
		cfg = DefaultTemplate()
	}

	var buf bytes.Buffer

	if cfg.HasHeader {
		buf.WriteString(strings.Join(cfg.Columns, cfg.Delimiter) + "\r\n")
	}

	for _, l := range lines {
		row := make([]string, len(cfg.Columns))
		for i, col := range cfg.Columns {
			switch strings.ToLower(col) {
			case "journal_code", "journal":
				row[i] = l.JournalCode
			case "date":
				row[i] = l.Date.Format(cfg.DateFormat)
			case "general_account", "compteg", "cg_num":
				row[i] = l.GeneralAcc
			case "auxiliary_account", "comptea", "ct_num":
				row[i] = l.AuxAcc
			case "piece_ref", "refpiece", "piece":
				row[i] = l.PieceRef
			case "libelle", "description":
				row[i] = l.Libelle
			case "debit":
				row[i] = fmt.Sprintf("%.2f", l.Debit)
			case "credit":
				row[i] = fmt.Sprintf("%.2f", l.Credit)
			default:
				row[i] = ""
			}
		}
		buf.WriteString(strings.Join(row, cfg.Delimiter) + "\r\n")
	}

	return buf.Bytes()
}

// BalanceCheck verifies that total Debits equal total Credits within 0.001 tolerance
func BalanceCheck(lines []JournalEntryLine) bool {
	var totalDebit, totalCredit float64
	for _, l := range lines {
		totalDebit += l.Debit
		totalCredit += l.Credit
	}
	diff := totalDebit - totalCredit
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.001
}

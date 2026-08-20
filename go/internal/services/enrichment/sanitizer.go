package enrichment

import (
	"regexp"
	"strings"
)

var (
	// Regex for transaction noise tokens
	noisePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bVIR\s+INST\b`),
		regexp.MustCompile(`(?i)\bVIR\s+EMIS\b`),
		regexp.MustCompile(`(?i)\bVIR\s+RECU\b`),
		regexp.MustCompile(`(?i)\bVIREMENT\b`),
		regexp.MustCompile(`(?i)\bPRLV\s+SEPA\b`),
		regexp.MustCompile(`(?i)\bPRLV\b`),
		regexp.MustCompile(`(?i)\bPRELEVEMENT\b`),
		regexp.MustCompile(`(?i)\bPAIEMENT\s+PAR\s+CARTE\b`),
		regexp.MustCompile(`(?i)\bPAIEMENT\s+CARTE\b`),
		regexp.MustCompile(`(?i)\bCARTE\b`),
		regexp.MustCompile(`(?i)\bRETRAIT\s+GAB\b`),
		regexp.MustCompile(`(?i)\bRETRAIT\s+DAB\b`),
		regexp.MustCompile(`(?i)\bBON\s+DE\s+CAISSE\b`),
		regexp.MustCompile(`(?i)\bFACTURE\b`),
		regexp.MustCompile(`(?i)\bFAC\b`),
		regexp.MustCompile(`(?i)\bREGLEMENT\b`),
		regexp.MustCompile(`(?i)\bAVOIR\b`),
		regexp.MustCompile(`\b\d{2}/\d{2}(?:/\d{2,4})?\b`), // Dates like 14/08 or 14/08/2026
		regexp.MustCompile(`\b\d{2}:\d{2}(?::\d{2})?\b`),   // Times like 09:30:00
		regexp.MustCompile(`\b\d{10,24}\b`),                // Long reference / account numbers
	}

	// Rail detection regexes
	virementInstRegex = regexp.MustCompile(`(?i)\bVIR(?:EMENT)?\s+INST\b`)
	virementRegex     = regexp.MustCompile(`(?i)\b(?:VIR|VIREMENT)\b`)
	prelevementRegex  = regexp.MustCompile(`(?i)\b(?:PRLV|PRELEVEMENT)\b`)
	chequeRegex       = regexp.MustCompile(`(?i)\b(?:CHQ|CHEQUE)\s*[:#]?\s*(\d+)\b`)
	gabRegex          = regexp.MustCompile(`(?i)\b(?:RETRAIT\s+GAB|RETRAIT\s+DAB|GAB|DAB)\b`)
	carteRegex        = regexp.MustCompile(`(?i)\b(?:CARTE|PAIEMENT\s+CARTE|TPE|POS)\b`)
	pettyCashRegex    = regexp.MustCompile(`(?i)\b(?:CAISSE|BON\s+DE\s+CAISSE|PETTY\s+CASH)\b`)

	// Bank fees detection regex
	agioRegex       = regexp.MustCompile(`(?i)\b(?:AGIOS|AGIO|INTERETS\s+DEBITEURS)\b`)
	bankCommRegex   = regexp.MustCompile(`(?i)\b(?:COMMISSION|FRAIS\s+TENUE|COTISATION|FRAIS\s+DOSSIER)\b`)
	intermGateways  = []string{"CMI", "FATOURATI", "BINGA", "PAYZONE", "WAFACASH", "CASH PLUS"}
)

// CleanDescription strips banking noise words, dates, and reference numbers.
func CleanDescription(raw string) string {
	cleaned := raw
	for _, p := range noisePatterns {
		cleaned = p.ReplaceAllString(cleaned, " ")
	}
	// Normalize spaces
	fields := strings.Fields(cleaned)
	return strings.Join(fields, " ")
}

// ClassifyPaymentRail determines the banking rail used for the transaction.
func ClassifyPaymentRail(raw string, accountCode string) PaymentRail {
	if strings.HasPrefix(accountCode, "5161") || pettyCashRegex.MatchString(raw) {
		return RailCashPetty
	}
	if virementInstRegex.MatchString(raw) {
		return RailVirementInstantane
	}
	if virementRegex.MatchString(raw) {
		return RailVirement
	}
	if prelevementRegex.MatchString(raw) {
		return RailPrelevement
	}
	if chequeRegex.MatchString(raw) {
		return RailCheque
	}
	if gabRegex.MatchString(raw) {
		return RailRetraitGAB
	}
	if carteRegex.MatchString(raw) {
		return RailPaiementCarte
	}
	return RailUnknown
}

// ExtractCheckNumber finds any check number in the description.
func ExtractCheckNumber(raw string) (string, bool) {
	m := chequeRegex.FindStringSubmatch(raw)
	if len(m) > 1 {
		return m[1], true
	}
	return "", false
}

// DetectBankFee checks if description represents bank commissions, agios, or account maintenance.
func DetectBankFee(raw string) (isFee bool, feeType string) {
	if agioRegex.MatchString(raw) {
		return true, "AGIOS"
	}
	if bankCommRegex.MatchString(raw) {
		return true, "COMMISSION"
	}
	return false, ""
}

// DetectIntermediary extracts intermediary payment gateways (CMI, Fatourati, Payzone, etc.).
func DetectIntermediary(raw string) (isIntermediated bool, intermediaryName *string) {
	upper := strings.ToUpper(raw)
	for _, gw := range intermGateways {
		if strings.Contains(upper, gw) {
			name := gw
			return true, &name
		}
	}
	return false, nil
}

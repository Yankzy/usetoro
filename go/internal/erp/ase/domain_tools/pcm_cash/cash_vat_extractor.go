package pcm_cash

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var iceRegex = regexp.MustCompile(`^\d{15}$`)

// SimplTVAFieldRecord represents the 6 mandatory fields for DGI SIMPL-TVA filing (CGI Art. 125).
type SimplTVAFieldRecord struct {
	PaymentDate      time.Time `json:"payment_date" xml:"date_reglement"`
	PaymentMode      string    `json:"payment_mode" xml:"mode_reglement"`
	ReferenceNum     string    `json:"reference_num" xml:"num_piece"`
	CounterpartyName string    `json:"counterparty_name" xml:"nom_tiers"`
	ICE              string    `json:"ice" xml:"ice"`
	AmountHT         float64   `json:"amount_ht" xml:"montant_ht"`
	AmountVAT        float64   `json:"amount_vat" xml:"montant_tva"`
	AmountTTC        float64   `json:"amount_ttc" xml:"montant_ttc"`
	VATRate          float64   `json:"vat_rate" xml:"taux_tva"`
	IsValid          bool      `json:"is_valid"`
	ValidationErrors []string  `json:"validation_errors"`
}

// ValidateICE checks if string is a valid 15-digit Moroccan Identifiant Commun de l'Entreprise.
func ValidateICE(ice string) bool {
	clean := strings.TrimSpace(ice)
	return iceRegex.MatchString(clean)
}

// ValidateSimplTVARecord inspects all 6 mandatory fields required by Direction Générale des Impôts (DGI).
func ValidateSimplTVARecord(rec *SimplTVAFieldRecord) []string {
	var errs []string

	if rec.PaymentDate.IsZero() {
		errs = append(errs, "MISSING_PAYMENT_DATE: Payment date is required for cash-basis VAT")
	}
	if strings.TrimSpace(rec.PaymentMode) == "" {
		errs = append(errs, "MISSING_PAYMENT_MODE: Payment mode (Virement/Chèque/Espèces/Carte/Effet) is required")
	}
	if strings.TrimSpace(rec.ReferenceNum) == "" {
		errs = append(errs, "MISSING_REFERENCE_NUM: Voucher or bank transaction reference number is required")
	}
	if strings.TrimSpace(rec.CounterpartyName) == "" {
		errs = append(errs, "MISSING_COUNTERPARTY: Supplier or Client name is required")
	}
	if !ValidateICE(rec.ICE) {
		errs = append(errs, fmt.Sprintf("INVALID_ICE_FORMAT: Supplier ICE '%s' must be exactly 15 digits", rec.ICE))
	}
	if rec.AmountTTC <= 0 {
		errs = append(errs, "INVALID_AMOUNT: Transaction amount TTC must be > 0")
	}

	rec.ValidationErrors = errs
	rec.IsValid = len(errs) == 0
	return errs
}

// DGIReleveDeductionsXML defines the XML schema required for DGI SIMPL-TVA submission.
type DGIReleveDeductionsXML struct {
	XMLName        xml.Name              `xml:"releve_deductions"`
	IdentifiantIF  string                `xml:"identifiant_fisc"`
	Annee          int                   `xml:"annee"`
	Periode        int                   `xml:"periode"`
	Regime         string                `xml:"regime"` // "1" for Encaissement
	DeductionItems []DGIReleveItemXML    `xml:"rd"`
}

type DGIReleveItemXML struct {
	NumOrdre       int     `xml:"ord"`
	NumFacture     string  `xml:"num_fact"`
	Designation    string  `xml:"des"`
	MontantHT      float64 `xml:"m_ht"`
	MontantTVA     float64 `xml:"m_tva"`
	MontantTTC     float64 `xml:"m_ttc"`
	NomFournisseur string  `xml:"fourn"`
	ICE            string  `xml:"ice"`
	TauxTVA        float64 `xml:"taux"`
	DateReglement  string  `xml:"dt_reg"`
	ModeReglement  string  `xml:"mode_reg"`
}

// GenerateSIMPLTVAXML compiles verified SIMPL records into official DGI XML schema.
func GenerateSIMPLTVAXML(ifNumber string, year int, month int, records []*SimplTVAFieldRecord) ([]byte, error) {
	xmlObj := DGIReleveDeductionsXML{
		IdentifiantIF: ifNumber,
		Annee:         year,
		Periode:       month,
		Regime:        "1", // Cash basis / Encaissement
	}

	for i, r := range records {
		if !r.IsValid {
			continue
		}
		item := DGIReleveItemXML{
			NumOrdre:       i + 1,
			NumFacture:     r.ReferenceNum,
			Designation:    r.CounterpartyName,
			MontantHT:      r.AmountHT,
			MontantTVA:     r.AmountVAT,
			MontantTTC:     r.AmountTTC,
			NomFournisseur: r.CounterpartyName,
			ICE:            r.ICE,
			TauxTVA:        r.VATRate * 100,
			DateReglement:  r.PaymentDate.Format("2006-01-02"),
			ModeReglement:  r.PaymentMode,
		}
		xmlObj.DeductionItems = append(xmlObj.DeductionItems, item)
	}

	return xml.MarshalIndent(xmlObj, "", "  ")
}

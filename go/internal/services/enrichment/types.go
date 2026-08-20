package enrichment

import (
	"time"

	"github.com/google/uuid"
)

// ScriptType designates the linguistic/script classification of a counterparty alias.
type ScriptType string

const (
	ScriptArabic               ScriptType = "ARABIC_SCRIPT"
	ScriptArabicTransliterated ScriptType = "ARABIC_TRANSLITERATED"
	ScriptFrenchLegal          ScriptType = "FRENCH_LEGAL"
	ScriptBankAbbreviation     ScriptType = "BANK_ABBREVIATION"
	ScriptAcronym              ScriptType = "ACRONYM"
)

// PaymentRail designates the Moroccan payment/clearing mechanism.
type PaymentRail string

const (
	RailVirement           PaymentRail = "VIREMENT"
	RailVirementInstantane PaymentRail = "VIREMENT_INSTANTANE"
	RailPrelevement        PaymentRail = "PRELEVEMENT"
	RailCheque             PaymentRail = "CHEQUE"
	RailRetraitGAB         PaymentRail = "RETRAIT_GAB"
	RailPaiementCarte      PaymentRail = "PAIEMENT_CARTE"
	RailCashPetty          PaymentRail = "CASH_PETTY"
	RailUnknown            PaymentRail = "UNKNOWN"
)

// GuardrailStatus designates statutory cash compliance under CGI Art. 193 & 210.
type GuardrailStatus string

const (
	GuardrailPass                      GuardrailStatus = "PASS"
	GuardrailWarningDailyLimitExceeded GuardrailStatus = "WARNING_DAILY_LIMIT_EXCEEDED"
	GuardrailBlockedDailyLimitExceeded GuardrailStatus = "BLOCKED_DAILY_LIMIT_EXCEEDED"
	GuardrailBlockedMonthlyLimit       GuardrailStatus = "BLOCKED_MONTHLY_LIMIT_EXCEEDED"
	GuardrailBlockedCaisseCreditrice   GuardrailStatus = "BLOCKED_CAISSE_CREDITRICE"
)

// ReceiptStatus designates document evidence correlation status.
type ReceiptStatus string

const (
	ReceiptMatchedHighConfidence ReceiptStatus = "MATCHED_HIGH_CONFIDENCE"
	ReceiptMatchedProbable       ReceiptStatus = "MATCHED_PROBABLE"
	ReceiptPendingAttachment     ReceiptStatus = "PENDING_ATTACHMENT"
	ReceiptNotApplicable         ReceiptStatus = "NOT_APPLICABLE"
)

// CashDirection indicates cash flow direction.
type CashDirection string

const (
	CashInflow  CashDirection = "INFLOW"
	CashOutflow CashDirection = "OUTFLOW"
)

// RawTransaction represents an un-enriched inbound Moroccan banking or cash record.
type RawTransaction struct {
	TransactionID   string        `json:"transaction_id"`
	RawDescription  string        `json:"raw_description"`
	Amount          float64       `json:"amount"`
	Currency        string        `json:"currency"`
	CashDirection   CashDirection `json:"cash_direction"`
	TransactionDate time.Time     `json:"transaction_date"`
	AccountCode     string        `json:"account_code,omitempty"`
}

// EnrichmentRequest is the top-level payload sent to CEE-MA.
type EnrichmentRequest struct {
	RequestID    string           `json:"request_id"`
	ClientID     string           `json:"client_id"`
	Transactions []RawTransaction `json:"transactions"`
}

// MatchedAliasInfo captures details about the matched multilingual alias.
type MatchedAliasInfo struct {
	RawAlias   string     `json:"raw_alias"`
	ScriptType ScriptType `json:"script_type"`
	Language   string     `json:"language"`
}

// TaxIdentifiers represents Moroccan & foreign statutory IDs.
type TaxIdentifiers struct {
	ICE          *string `json:"ice,omitempty"`
	IF           *string `json:"if,omitempty"`
	RC           *string `json:"rc,omitempty"`
	CNSS         *string `json:"cnss,omitempty"`
	ForeignTaxID *string `json:"foreign_tax_id,omitempty"`
}

// CounterpartyInfo represents the resolved vendor/beneficiary identity.
type CounterpartyInfo struct {
	MasterMerchantID uuid.UUID         `json:"master_merchant_id,omitempty"`
	NormalizedName   string            `json:"normalized_name"`
	MerchantCategory string            `json:"merchant_category"`
	Country          string            `json:"country"`
	IsForeignService bool              `json:"is_foreign_service"`
	MatchedAlias     *MatchedAliasInfo `json:"matched_alias,omitempty"`
	Identifiers      TaxIdentifiers    `json:"identifiers"`
	Domain           string            `json:"domain,omitempty"`
	LogoURL          string            `json:"logo_url,omitempty"`
}

// PCGMInfo represents Moroccan standard general ledger mapping and VAT split.
type PCGMInfo struct {
	SuggestedAccount string  `json:"suggested_account"`
	AccountLabel     string  `json:"account_label"`
	DefaultTVARate   float64 `json:"default_tva_rate"`
	TVAAccount       string  `json:"tva_account"`
	TVAAmount        float64 `json:"tva_amount"`
	NetHTAmount      float64 `json:"net_ht_amount"`
	IsDeductible     bool    `json:"is_deductible"`
}

// ForeignProviderRunningTotals tracks period-based cumulative tax obligations.
type ForeignProviderRunningTotals struct {
	FiscalYear                        int     `json:"fiscal_year"`
	FiscalMonth                       int     `json:"fiscal_month"`
	CurrentMonthCumulativeInvoicedMAD float64 `json:"current_month_cumulative_invoiced_mad"`
	CurrentMonthCumulativeRASMAD      float64 `json:"current_month_cumulative_ras_mad"`
	CurrentYearCumulativeInvoicedMAD  float64 `json:"current_year_cumulative_invoiced_mad"`
	CurrentYearCumulativeRASMAD       float64 `json:"current_year_cumulative_ras_mad"`
	DGIFilingDeadline                 string  `json:"dgi_filing_deadline"`
}

// StatutoryGuardrailsInfo holds cash limit and caisse compliance metrics.
type StatutoryGuardrailsInfo struct {
	IsCashPayment          bool            `json:"is_cash_payment"`
	DailyVendorCashTotal   float64         `json:"daily_vendor_cash_total,omitempty"`
	MonthlyVendorCashTotal float64         `json:"monthly_vendor_cash_total,omitempty"`
	GuardrailStatus        GuardrailStatus `json:"guardrail_status"`
	ViolationReason        string          `json:"violation_reason,omitempty"`
}

// TaxComplianceInfo captures RAS withholding, running totals, and cash limits.
type TaxComplianceInfo struct {
	RASApplicable                bool                          `json:"ras_applicable"`
	RASRate                      float64                       `json:"ras_rate"`
	RASAccount                   *string                       `json:"ras_account,omitempty"`
	RASAmountMAD                 float64                       `json:"ras_amount_mad,omitempty"`
	NetTransferredMAD            float64                       `json:"net_transferred_mad,omitempty"`
	ForeignProviderRunningTotals *ForeignProviderRunningTotals `json:"foreign_provider_running_totals,omitempty"`
	StatutoryGuardrails          StatutoryGuardrailsInfo       `json:"statutory_guardrails"`
}

// BankingInstrumentInfo captures extracted payment rail, check number, and fee info.
type BankingInstrumentInfo struct {
	PaymentRail        PaymentRail `json:"payment_rail"`
	ExtractedReference string      `json:"extracted_reference,omitempty"`
	CheckNumber        string      `json:"check_number,omitempty"`
	IsIntermediated    bool        `json:"is_intermediated"`
	IntermediaryName   *string     `json:"intermediary_name,omitempty"`
	IsBankFee          bool        `json:"is_bank_fee"`
	BaseFeeAmount      float64     `json:"base_fee_amount,omitempty"`
	BankVATAmount      float64     `json:"bank_vat_amount,omitempty"`
}

// DocumentEvidenceInfo links transaction to supporting OCR receipts in toro_core.documents.
type DocumentEvidenceInfo struct {
	HasReceipt             bool          `json:"has_receipt"`
	MatchedDocumentID      *string       `json:"matched_document_id,omitempty"`
	ReceiptStatus          ReceiptStatus `json:"receipt_status"`
	ExtractedInvoiceNumber string        `json:"extracted_invoice_number,omitempty"`
	ReceiptURL             string        `json:"receipt_url,omitempty"`
	ConfidenceScore        float64       `json:"confidence_score,omitempty"`
}

// AnnotatedMoroccanTransactionEnvelope represents the complete 5-dimension enriched transaction.
type AnnotatedMoroccanTransactionEnvelope struct {
	TransactionID        string                `json:"transaction_id"`
	RawDescription       string                `json:"raw_description"`
	SanitizedStem        string                `json:"sanitized_stem"`
	Counterparty         CounterpartyInfo      `json:"counterparty"`
	PCGMAccounting       PCGMInfo              `json:"pcgm_accounting"`
	TaxAndCompliance     TaxComplianceInfo     `json:"tax_and_compliance"`
	BankingInstrument    BankingInstrumentInfo `json:"banking_instrument"`
	DocumentEvidence     DocumentEvidenceInfo  `json:"document_evidence"`
	ConfidenceScore      float64               `json:"confidence_score"`
	EnrichmentSource     string                `json:"enrichment_source"`
	ProcessingDurationMS float64               `json:"processing_duration_ms"`
}

// EnrichmentResponse is the top-level response envelope returned by CEE-MA.
type EnrichmentResponse struct {
	RequestID            string                                 `json:"request_id"`
	EnrichedTransactions []AnnotatedMoroccanTransactionEnvelope `json:"enriched_transactions"`
	TotalDurationMS      float64                                `json:"total_duration_ms"`
}

// MasterMerchant represents the golden domain record.
type MasterMerchant struct {
	ID                 uuid.UUID
	NormalizedName     string
	LegalName          string
	CountryCode        string
	MerchantCategory   string
	ICE                string
	IdentifiantFiscal  string
	RegistreCommerce   string
	CNSSNumber         string
	PrimaryDomain      string
	LogoURL            string
	DefaultPCGMAccount string
	DefaultTVARate     float64
	DefaultTVAAccount  string
	IsTVADeductible    bool
	IsForeignService   bool
	RASApplicable      bool
	RASRate            float64
	ConfidenceWeight   float64
}

// MerchantAliasRecord represents an alias variant in toro_core.moroccan_merchant_multilingual_aliases.
type MerchantAliasRecord struct {
	ID               uuid.UUID
	MasterMerchantID uuid.UUID
	AliasVariant     string
	ScriptType       ScriptType
	LanguageCode     string
	IsPrimary        bool
	ConfidenceScore  float64
	Merchant         *MasterMerchant
}

// NonResidentForeignProvider represents a row in toro_core.non_resident_foreign_providers.
type NonResidentForeignProvider struct {
	ID                     uuid.UUID
	MasterMerchantID       uuid.UUID
	ProviderName           string
	HeadquartersCountry    string
	TaxResidencyStatus     string
	VATWithholdingRate     float64
	ServiceType            string
	PCGMExpenseAccount     string
	PCGMWithholdingAccount string
	IsActive               bool
	StatutoryLegalBasis    string
}

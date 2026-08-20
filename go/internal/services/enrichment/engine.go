package enrichment

import (
	"context"
	"time"
)

// MoroccanEnrichmentEngine is the central coordinator for Moroccan cognitive enrichment.
type MoroccanEnrichmentEngine struct {
	aliasResolver    *AliasResolver
	foreignTracker   *ForeignProviderTracker
	cashGuardrails   *CashGuardrailsEvaluator
	docCorrelator    *DocumentCorrelator
}

// NewMoroccanEnrichmentEngine initializes all sub-components.
func NewMoroccanEnrichmentEngine() *MoroccanEnrichmentEngine {
	return &MoroccanEnrichmentEngine{
		aliasResolver:  NewAliasResolver(),
		foreignTracker: NewForeignProviderTracker(),
		cashGuardrails: NewCashGuardrailsEvaluator(),
		docCorrelator:  NewDocumentCorrelator(),
	}
}

// GetAliasResolver exposes the internal alias resolver for registration or querying.
func (e *MoroccanEnrichmentEngine) GetAliasResolver() *AliasResolver {
	return e.aliasResolver
}

// GetForeignTracker exposes the foreign provider tracker.
func (e *MoroccanEnrichmentEngine) GetForeignTracker() *ForeignProviderTracker {
	return e.foreignTracker
}

// GetCashGuardrails exposes the cash guardrail evaluator.
func (e *MoroccanEnrichmentEngine) GetCashGuardrails() *CashGuardrailsEvaluator {
	return e.cashGuardrails
}

// GetDocCorrelator exposes the document correlator.
func (e *MoroccanEnrichmentEngine) GetDocCorrelator() *DocumentCorrelator {
	return e.docCorrelator
}

// EnrichTransaction processes a single Moroccan raw transaction across all 5 dimensions.
func (e *MoroccanEnrichmentEngine) EnrichTransaction(
	ctx context.Context,
	realmID string,
	rawTxn RawTransaction,
) AnnotatedMoroccanTransactionEnvelope {
	startTime := time.Now()

	// Dimension 4: Banking Rails & Payment Intermediary Extraction
	rail := ClassifyPaymentRail(rawTxn.RawDescription, rawTxn.AccountCode)
	checkNum, _ := ExtractCheckNumber(rawTxn.RawDescription)
	isFee, feeType := DetectBankFee(rawTxn.RawDescription)
	isIntermediated, intermediaryName := DetectIntermediary(rawTxn.RawDescription)

	// Clean description and extract statutory IDs
	sanitizedStem := CleanDescription(rawTxn.RawDescription)
	extractedIDs := ExtractTaxIdentifiers(rawTxn.RawDescription)

	// Dimension 1: Counterparty & Multilingual Alias Resolution
	var matchedMerchant *MasterMerchant
	var matchedAlias *MatchedAliasInfo
	source := "GENERIC_FALLBACK"

	// 1.1 Try matching by extracted 15-digit ICE
	if extractedIDs.ICE != nil {
		if m, ok := e.aliasResolver.ResolveByICE(*extractedIDs.ICE); ok {
			matchedMerchant = m
			source = "STATUTORY_ICE_REGISTRY"
		}
	}

	// 1.2 Try matching by Multilingual Alias / Stem
	if matchedMerchant == nil {
		if m, aliasInfo, ok := e.aliasResolver.ResolveMerchant(ctx, sanitizedStem); ok {
			matchedMerchant = m
			matchedAlias = aliasInfo
			source = "GLOBAL_MULTILINGUAL_ALIAS_NETWORK"
		}
	}

	// Build Counterparty Info
	counterparty := CounterpartyInfo{
		NormalizedName:   sanitizedStem,
		MerchantCategory: "GENERAL_EXPENSE",
		Country:          "MA",
		IsForeignService: false,
		Identifiers:      extractedIDs,
	}

	if matchedMerchant != nil {
		counterparty.MasterMerchantID = matchedMerchant.ID
		counterparty.NormalizedName = matchedMerchant.NormalizedName
		counterparty.MerchantCategory = matchedMerchant.MerchantCategory
		counterparty.Country = matchedMerchant.CountryCode
		counterparty.IsForeignService = matchedMerchant.IsForeignService
		counterparty.Domain = matchedMerchant.PrimaryDomain
		counterparty.LogoURL = matchedMerchant.LogoURL
		counterparty.MatchedAlias = matchedAlias

		if matchedMerchant.ICE != "" && counterparty.Identifiers.ICE == nil {
			ice := matchedMerchant.ICE
			counterparty.Identifiers.ICE = &ice
		}
		if matchedMerchant.IdentifiantFiscal != "" && counterparty.Identifiers.IF == nil {
			iff := matchedMerchant.IdentifiantFiscal
			counterparty.Identifiers.IF = &iff
		}
		if matchedMerchant.RegistreCommerce != "" && counterparty.Identifiers.RC == nil {
			rc := matchedMerchant.RegistreCommerce
			counterparty.Identifiers.RC = &rc
		}
		if matchedMerchant.CNSSNumber != "" && counterparty.Identifiers.CNSS == nil {
			cnss := matchedMerchant.CNSSNumber
			counterparty.Identifiers.CNSS = &cnss
		}
		if matchedMerchant.IsForeignService {
			source = "NON_RESIDENT_FOREIGN_REGISTRY"
		}
	}

	// Dimension 2: Automated PCGM Chart of Accounts & VAT Split
	suggestedAccount := "611100"
	tvaRate := 0.20
	tvaAccount := "345510"
	isDeductible := true

	if matchedMerchant != nil {
		suggestedAccount = matchedMerchant.DefaultPCGMAccount
		tvaRate = matchedMerchant.DefaultTVARate
		tvaAccount = matchedMerchant.DefaultTVAAccount
		isDeductible = matchedMerchant.IsTVADeductible
	} else if isFee {
		if feeType == "AGIOS" {
			suggestedAccount = "631100"
		} else {
			suggestedAccount = "614700"
		}
		tvaRate = 0.10
		tvaAccount = "345520"
	}

	netHT, tvaAmount := CalculateMoroccanVAT(rawTxn.Amount, tvaRate)
	pcgmInfo := PCGMInfo{
		SuggestedAccount: suggestedAccount,
		AccountLabel:     GetPCGMLabel(suggestedAccount),
		DefaultTVARate:   tvaRate,
		TVAAccount:       tvaAccount,
		TVAAmount:        tvaAmount,
		NetHTAmount:      netHT,
		IsDeductible:     isDeductible,
	}

	// Dimension 3: Statutory Tax Withholding (RAS) & Cumulative Tracking
	isRent := (suggestedAccount == "613100")
	rasApp, rasRate, rasAcc, rasAmountMAD, netTransferredMAD := EvaluateRAS(matchedMerchant, rawTxn.Amount, isRent)

	var runningTotals *ForeignProviderRunningTotals
	if counterparty.IsForeignService && matchedMerchant != nil {
		// Record and increment foreign provider running totals
		rTotals, err := e.foreignTracker.RecordForeignTransaction(
			ctx,
			realmID,
			matchedMerchant.ID,
			rawTxn.Amount,
			rasAmountMAD,
			netTransferredMAD,
			rawTxn.TransactionDate,
		)
		if err == nil {
			runningTotals = rTotals
		}
	}

	// Dimension 3.2: Statutory Cash Ceilings & Caisse Integrity
	var guardrailsInfo StatutoryGuardrailsInfo
	if rail == RailCashPetty {
		isOutflow := (rawTxn.CashDirection == CashOutflow)
		guardrailsInfo = e.cashGuardrails.EvaluateAndRecordCashPayment(
			ctx,
			realmID,
			counterparty.NormalizedName,
			rawTxn.Amount,
			rawTxn.TransactionDate,
			isOutflow,
		)
	} else {
		guardrailsInfo = StatutoryGuardrailsInfo{
			IsCashPayment:   false,
			GuardrailStatus: GuardrailPass,
		}
	}

	taxCompliance := TaxComplianceInfo{
		RASApplicable:                rasApp,
		RASRate:                      rasRate,
		RASAccount:                   rasAcc,
		RASAmountMAD:                 rasAmountMAD,
		NetTransferredMAD:            netTransferredMAD,
		ForeignProviderRunningTotals: runningTotals,
		StatutoryGuardrails:          guardrailsInfo,
	}

	// Dimension 4: Banking Instrument Info
	var baseFee float64
	var bankVAT float64
	if isFee {
		baseFee = netHT
		bankVAT = tvaAmount
	}

	bankingInstrument := BankingInstrumentInfo{
		PaymentRail:        rail,
		ExtractedReference: sanitizedStem,
		CheckNumber:        checkNum,
		IsIntermediated:    isIntermediated,
		IntermediaryName:   intermediaryName,
		IsBankFee:          isFee,
		BaseFeeAmount:      baseFee,
		BankVATAmount:      bankVAT,
	}

	// Dimension 5: Supporting Document & OCR Receipt Correlator
	var supplierICE string
	if counterparty.Identifiers.ICE != nil {
		supplierICE = *counterparty.Identifiers.ICE
	}
	docEvidence := e.docCorrelator.Correlate(
		ctx,
		realmID,
		rawTxn.Amount,
		rawTxn.TransactionDate,
		supplierICE,
		counterparty.NormalizedName,
	)

	// Calculate confidence score
	confidence := 0.60
	if matchedMerchant != nil {
		confidence = 0.95
		if docEvidence.HasReceipt {
			confidence = 1.00
		}
	}

	durationMS := float64(time.Since(startTime).Microseconds()) / 1000.0

	return AnnotatedMoroccanTransactionEnvelope{
		TransactionID:        rawTxn.TransactionID,
		RawDescription:       rawTxn.RawDescription,
		SanitizedStem:        sanitizedStem,
		Counterparty:         counterparty,
		PCGMAccounting:       pcgmInfo,
		TaxAndCompliance:     taxCompliance,
		BankingInstrument:    bankingInstrument,
		DocumentEvidence:     docEvidence,
		ConfidenceScore:      confidence,
		EnrichmentSource:     source,
		ProcessingDurationMS: durationMS,
	}
}

// EnrichBatch processes a batch of raw transactions.
func (e *MoroccanEnrichmentEngine) EnrichBatch(
	ctx context.Context,
	req EnrichmentRequest,
) EnrichmentResponse {
	start := time.Now()

	envelopes := make([]AnnotatedMoroccanTransactionEnvelope, len(req.Transactions))
	for i, raw := range req.Transactions {
		envelopes[i] = e.EnrichTransaction(ctx, req.ClientID, raw)
	}

	totalDurationMS := float64(time.Since(start).Microseconds()) / 1000.0

	return EnrichmentResponse{
		RequestID:            req.RequestID,
		EnrichedTransactions: envelopes,
		TotalDurationMS:      totalDurationMS,
	}
}

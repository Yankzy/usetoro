package enrichment

// EvaluateRAS calculates statutory withholding tax under the Moroccan Tax Code (CGI).
func EvaluateRAS(merchant *MasterMerchant, amount float64, isRent bool) (rasApplicable bool, rasRate float64, rasAccount *string, rasAmountMAD float64, netTransferredMAD float64) {
	if amount <= 0 {
		return false, 0.0, nil, 0.0, amount
	}

	acc := "445800"

	// 1. Foreign Non-Resident Service Providers (CGI Art. 157) -> 10% Withholding
	if merchant != nil && (merchant.IsForeignService || merchant.RASApplicable) {
		rate := 0.10
		if merchant.RASRate > 0 {
			rate = merchant.RASRate
		}
		rasMAD := round2Decimals(amount * rate)
		netMAD := round2Decimals(amount - rasMAD)
		return true, rate, &acc, rasMAD, netMAD
	}

	// 2. Commercial Real Estate Rent from non-corporate landlord (CGI Art. 160) -> 5% Withholding
	if isRent {
		rate := 0.05
		rasMAD := round2Decimals(amount * rate)
		netMAD := round2Decimals(amount - rasMAD)
		return true, rate, &acc, rasMAD, netMAD
	}

	return false, 0.0, nil, 0.0, amount
}

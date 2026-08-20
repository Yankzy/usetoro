package enrichment

import (
	"math"
)

var pcgmLabels = map[string]string{
	"614400": "Achats d'eau et d'électricité",
	"614510": "Frais postaux et de télécommunications",
	"612540": "Carburants et lubrifiants",
	"613100": "Locations et charges locatives",
	"613670": "Redevances pour logiciels et SaaS étrangers",
	"614700": "Services bancaires et frais de tenue de compte",
	"631100": "Intérêts des emprunts et dettes (Agios)",
	"614300": "Transports du personnel et voyages",
	"614350": "Péages et frais d'autoroute",
	"614410": "Publicité, publications et relations publiques",
	"611100": "Achats de marchandises",
	"612200": "Achats de matières et fournitures consommables",
}

// GetPCGMLabel returns the official French accounting label for a PCGM account code.
func GetPCGMLabel(accountCode string) string {
	if label, ok := pcgmLabels[accountCode]; ok {
		return label
	}
	return "Charges d'exploitation"
}

// CalculateMoroccanVAT computes the exact Net HT and statutory TVA breakdown from a TTC amount.
func CalculateMoroccanVAT(ttcAmount float64, tvaRate float64) (netHT float64, tvaAmount float64) {
	if ttcAmount <= 0 {
		return 0.0, 0.0
	}
	if tvaRate <= 0 {
		return ttcAmount, 0.0
	}

	divisor := 1.0 + tvaRate
	rawHT := ttcAmount / divisor
	netHT = round2Decimals(rawHT)
	tvaAmount = round2Decimals(ttcAmount - netHT)
	return netHT, tvaAmount
}

// round2Decimals performs bankers/half-up 2 decimal place rounding.
func round2Decimals(val float64) float64 {
	return math.Round(val*100.0) / 100.0
}

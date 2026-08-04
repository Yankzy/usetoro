package ase

import (
	"math"
)

// CalculateEntropy computes the Shannon entropy of a set of classification
// candidates based on their confidence scores.
//
// H = -Σ(p_i * log₂(p_i))
//
// Confidence scores are normalized to sum to 1.0. If no candidates are provided,
// returns 1.0 (maximum entropy / complete uncertainty).
// If only one candidate exists, returns 0.0 (perfect certainty).
func CalculateEntropy(candidates []ProbabilityCandidate) float64 {
	if len(candidates) == 0 {
		return 1.0
	}
	if len(candidates) == 1 {
		return 0.0
	}

	// Sum all confidence scores.
	var total float64
	for _, c := range candidates {
		total += c.Confidence
	}
	if total == 0 {
		return 1.0
	}

	// Normalize and compute Shannon entropy.
	var entropy float64
	for _, c := range candidates {
		p := c.Confidence / total
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}

	// Scale entropy based on the number of options (max entropy is log2(N))
	// This ensures that 5 options properly scales between 0 and 1 instead of overflowing.
	maxEntropy := math.Log2(float64(len(candidates)))
	if maxEntropy > 0 {
		entropy = entropy / maxEntropy
	}

	// Clamp to [0, 1] as a final safety measure.
	if entropy > 1.0 {
		entropy = 1.0
	}
	if entropy < 0.0 {
		entropy = 0.0
	}

	return entropy
}

// CalculateUnifiedConfidence calculates the unified confidence score using the Shannon entropy formula:
// C = 1 - (Sum(H(k)) / Total Properties).
func CalculateUnifiedConfidence(propertyEntropies map[string]float64, candidates map[string][]ProbabilityCandidate, expectedProperties int) (float64, float64) {
	var sumEntropy float64
	for _, h := range propertyEntropies {
		sumEntropy += h
	}

	var sumConfidence float64
	for _, cands := range candidates {
		if len(cands) > 0 {
			best := cands[0]
			for i := 1; i < len(cands); i++ {
				if cands[i].Confidence > best.Confidence {
					best = cands[i]
				}
			}
			sumConfidence += best.Confidence
		}
	}

	totalProperties := len(candidates)
	if expectedProperties > 0 && totalProperties < expectedProperties {
		totalProperties = expectedProperties
	} else if expectedProperties == 0 && totalProperties < 4 {
		// Fallback for backward compatibility if ExpectedProperties is not configured
		totalProperties = 4
	}

	unifiedConfidence := 0.0
	if totalProperties > 0 {
		unifiedConfidence = sumConfidence / float64(totalProperties)
	}

	return sumEntropy, unifiedConfidence
}

// CalculateExpectedValue computes EV = (P(S|a) * ExpectedIG(a)) - C(a)
func CalculateExpectedValue(probSuccess, expectedIG, cost float64) float64 {
	return (probSuccess * expectedIG) - cost
}

package enrichment

import (
	"regexp"
)

var (
	iceRegex  = regexp.MustCompile(`\b(00\d{13})\b`)
	ifRegex   = regexp.MustCompile(`(?i)\bIF\s*[:#]?\s*(\d{7,9})\b`)
	rcRegex   = regexp.MustCompile(`(?i)\bRC\s*[:#]?\s*(\d+[_A-Z]+)\b`)
	cnssRegex = regexp.MustCompile(`(?i)\bCNSS\s*[:#]?\s*(\d{7,10})\b`)
)

// ExtractICE retrieves a 15-digit Moroccan ICE from the string if present.
func ExtractICE(text string) (string, bool) {
	m := iceRegex.FindStringSubmatch(text)
	if len(m) > 1 {
		return m[1], true
	}
	return "", false
}

// ExtractTaxIdentifiers searches for all Moroccan statutory IDs in the string.
func ExtractTaxIdentifiers(text string) TaxIdentifiers {
	var ids TaxIdentifiers

	if ice, ok := ExtractICE(text); ok {
		ids.ICE = &ice
	}

	if m := ifRegex.FindStringSubmatch(text); len(m) > 1 {
		val := m[1]
		ids.IF = &val
	}

	if m := rcRegex.FindStringSubmatch(text); len(m) > 1 {
		val := m[1]
		ids.RC = &val
	}

	if m := cnssRegex.FindStringSubmatch(text); len(m) > 1 {
		val := m[1]
		ids.CNSS = &val
	}

	return ids
}

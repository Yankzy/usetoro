package cleanup

import (
	"math"
	"time"
)

// EnrichedRow is an in-memory representation used during enrichment and dedup.
type EnrichedRow struct {
	ID                   string
	SessionID            string
	RealmID              string
	RawDescription       string
	RawAmount            float64
	RawDate              time.Time
	RawVendorName        string
	RawCustomerName      string
	PredictedVendorID    string // empty = unresolved
	PredictedCustomerID  string // empty = unresolved
	PredictedAccountID   string // empty = unresolved
	PredictedAccountName string
	NormalizedVendor     string
	NormalizedCustomer   string
	MerchantName         string
	Category             string
	ConfidenceScore      float64
	AIReasoning          string
	IsRecurring          bool
	SplitSuggestion      []SplitLine
	DuplicateOf          string
	Status               string
}

// SplitLine represents one line of a split transaction suggestion.
type SplitLine struct {
	AccountID string  `json:"account_id"`
	Amount    float64 `json:"amount"`
}

// Deduplicator detects duplicates and recurring patterns within a session's rows.
type Deduplicator struct{}

// NewDeduplicator constructs a Deduplicator.
func NewDeduplicator() *Deduplicator { return &Deduplicator{} }

func vendorKey(row *EnrichedRow) string {
	vendor := coalesce(row.PredictedVendorID, row.PredictedCustomerID, row.NormalizedVendor, row.NormalizedCustomer, row.MerchantName, row.RawVendorName, row.RawCustomerName)
	if vendor == "" {
		vendor = row.RawDescription
	}
	return vendor
}

// AnnotateDuplicates mutates rows in-place, setting DuplicateOf on rows that
// appear to be duplicates. The canonical (first-seen) row keeps DuplicateOf="".
//
// Duplicate heuristic: same (resolved vendor OR raw vendor name) + same rounded
// amount + transaction date within ±1 day.
func (d *Deduplicator) AnnotateDuplicates(rows []EnrichedRow) {
	// key → ID of the first (canonical) row we saw with this signature
	seen := make(map[string]string, len(rows))

	for i := range rows {
		row := &rows[i]
		if row.DuplicateOf != "" {
			continue // already marked
		}

		// Build multiple candidate keys at ±1 day window.
		for _, date := range dateWindow(row.RawDate) {
			key := dupKey(row, date)
			if canonicalID, exists := seen[key]; exists && canonicalID != row.ID {
				row.DuplicateOf = canonicalID
				break
			}
		}

		if row.DuplicateOf == "" {
			// Register this row as the canonical for its signature (date exact).
			key := dupKey(row, row.RawDate)
			if _, exists := seen[key]; !exists {
				seen[key] = row.ID
			}
		}
	}
}

// AnnotateRecurring marks rows whose (vendor, rounded_amount) combination appears
// 3 or more times across the session. This signals a potential subscription/recurring charge.
func (d *Deduplicator) AnnotateRecurring(rows []EnrichedRow) {
	type sig struct {
		vendor string
		amount int64 // cents, rounded
	}

	counts := make(map[sig]int, len(rows))
	for _, row := range rows {
		if row.DuplicateOf != "" {
			continue
		}
		s := sig{
			vendor: vendorKey(&row),
			amount: roundCents(row.RawAmount),
		}
		counts[s]++
	}

	for i := range rows {
		if rows[i].DuplicateOf != "" {
			continue
		}
		s := sig{
			vendor: vendorKey(&rows[i]),
			amount: roundCents(rows[i].RawAmount),
		}
		if counts[s] >= 3 {
			rows[i].IsRecurring = true
		}
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func dupKey(row *EnrichedRow, date time.Time) string {
	vendor := vendorKey(row)
	cents := roundCents(row.RawAmount)
	dateStr := ""
	if !date.IsZero() {
		dateStr = date.Format("2006-01-02")
	}
	return vendor + "|" + i64str(cents) + "|" + dateStr
}

// dateWindow returns the exact date plus ±1 day to catch near-duplicate dates.
func dateWindow(t time.Time) []time.Time {
	if t.IsZero() {
		return []time.Time{t}
	}
	return []time.Time{
		t.AddDate(0, 0, -1),
		t,
		t.AddDate(0, 0, 1),
	}
}

// roundCents rounds an amount to the nearest cent as an integer (avoids float precision issues).
func roundCents(amount float64) int64 {
	return int64(math.Round(amount * 100))
}

// coalesce returns the first non-empty string.
func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func i64str(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

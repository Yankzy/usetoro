package cleanup

import (
	"testing"
	"time"
)

func TestDeduplicator_AnnotateDuplicates(t *testing.T) {
	dedup := NewDeduplicator()
	now := time.Now()

	rows := []EnrichedRow{
		{
			ID:                 "1",
			RawAmount:          100.50,
			RawDate:            now,
			PredictedVendorID:  "v1",
		},
		{
			ID:                 "2",
			RawAmount:          100.50,
			RawDate:            now, // Exact match on date, amount, vendor
			PredictedVendorID:  "v1",
		},
		{
			ID:                  "3",
			RawAmount:          100.50,
			RawDate:             now,
			PredictedCustomerID: "c1", // Different entity
		},
		{
			ID:                  "4",
			RawAmount:          100.50,
			RawDate:             now,
			PredictedCustomerID: "c1", // Duplicate of c1
		},
	}

	dedup.AnnotateDuplicates(rows)

	if rows[0].DuplicateOf != "" {
		t.Errorf("expected row 1 to be canonical, got duplicate of %s", rows[0].DuplicateOf)
	}
	if rows[1].DuplicateOf != "1" {
		t.Errorf("expected row 2 to be duplicate of 1, got %s", rows[1].DuplicateOf)
	}
	if rows[2].DuplicateOf != "" {
		t.Errorf("expected row 3 to be canonical, got duplicate of %s", rows[2].DuplicateOf)
	}
	if rows[3].DuplicateOf != "3" {
		t.Errorf("expected row 4 to be duplicate of 3, got %s", rows[3].DuplicateOf)
	}
}

func TestDeduplicator_AnnotateRecurring(t *testing.T) {
	dedup := NewDeduplicator()

	rows := []EnrichedRow{
		{ID: "1", RawAmount: 50.00, NormalizedCustomer: "Cust1"},
		{ID: "2", RawAmount: 50.00, NormalizedCustomer: "Cust1"},
		{ID: "3", RawAmount: 50.00, NormalizedCustomer: "Cust1"}, // 3rd occurrence -> all should be marked recurring
		{ID: "4", RawAmount: 20.00, NormalizedVendor: "Vend1"},
		{ID: "5", RawAmount: 20.00, NormalizedVendor: "Vend1"},
	}

	dedup.AnnotateRecurring(rows)

	if !rows[0].IsRecurring || !rows[1].IsRecurring || !rows[2].IsRecurring {
		t.Errorf("expected Cust1 rows to be marked recurring")
	}
	if rows[3].IsRecurring || rows[4].IsRecurring {
		t.Errorf("expected Vend1 rows to NOT be marked recurring since only 2 occur")
	}
}

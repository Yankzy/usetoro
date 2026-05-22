package workers

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseCSVDate(t *testing.T) {
	tests := []struct {
		input     string
		expected  time.Time
		expectErr bool
	}{
		// ISO standard formats
		{input: "2026-05-18", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "2026-05-18T15:04:05Z", expected: time.Date(2026, 5, 18, 15, 4, 5, 0, time.UTC), expectErr: false},

		// Slash formats (US/EU)
		{input: "05/18/2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false}, // US MM/DD/YYYY
		{input: "18/05/2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false}, // EU DD/MM/YYYY
		{input: "2026/05/18", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false}, // YYYY/MM/DD
		
		// 2-digit years
		{input: "05/18/26", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false}, // MM/DD/YY
		{input: "18/05/26", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false}, // DD/MM/YY

		// Single-digit month/day
		{input: "5/18/2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "18/5/2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "5/18/26", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "18/5/26", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},

		// Dot-separated (European banks)
		{input: "18.05.2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},

		// Dash-separated numeric
		{input: "05-18-2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},

		// Month names
		{input: "18-May-2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "18-May-26", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "May 18, 2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "18 May 2026", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},
		{input: "January 18, 2026", expected: time.Date(2026, 1, 18, 0, 0, 0, 0, time.UTC), expectErr: false},

		// White space handling
		{input: "   2026-05-18   ", expected: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), expectErr: false},

		// === Invisible Unicode characters (the root cause of random NULL parsed_dates) ===
		// BOM prefix (common in CSV files saved from Excel)
		{input: "\uFEFF2026-03-13", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Zero-width space embedded
		{input: "2026\u200B-03-13", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Trailing non-breaking space
		{input: "03/13/2026\u00A0", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Windows carriage return (from \r\n CSV line endings)
		{input: "2026-03-13\r", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Soft hyphen in place of regular hyphen
		{input: "2026\u00AD03-13", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// En-dash instead of hyphen (copy-paste from formatted documents)
		{input: "2026\u201303\u201313", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Multiple invisible chars combined
		{input: "\uFEFF2026-03-13\u200B\r", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},
		// Direction mark prefix
		{input: "\u200E03/13/2026", expected: time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC), expectErr: false},

		// Error cases
		{input: "", expectErr: true},
		{input: "invalid-date", expectErr: true},
		{input: "2026-13-45", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			res, err := parseCSVDate(tc.input)
			if tc.expectErr {
				if err == nil {
					t.Errorf("expected error for input %q, got nil", tc.input)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for input %q: %v", tc.input, err)
				}
				if res.Year() != tc.expected.Year() || res.Month() != tc.expected.Month() || res.Day() != tc.expected.Day() {
					t.Errorf("expected date %v, got %v for input %q", tc.expected, res, tc.input)
				}
			}
		})
	}
}

func TestPgtypeDateIntegration(t *testing.T) {
	// Verify that parsing a date and loading it into pgtype.Date conforms to the exact QBO expected "YYYY-MM-DD" format
	input := "May 24, 2025"
	parsed, err := parseCSVDate(input)
	if err != nil {
		t.Fatalf("failed to parse date: %v", err)
	}

	dDate := pgtype.Date{Time: parsed, Valid: true}
	
	// Value returns driver.Value, which for pgtype.Date is time.Time
	val, err := dDate.Value()
	if err != nil {
		t.Fatalf("failed to get driver value: %v", err)
	}

	tTime, ok := val.(time.Time)
	if !ok {
		t.Fatalf("expected driver value to be time.Time, got %T", val)
	}

	// Format as YYYY-MM-DD (format expected by QBO US: 2025-11-24 / 2025-05-24)
	formatted := tTime.Format("2006-01-02")
	expectedFormatted := "2025-05-24"
	if formatted != expectedFormatted {
		t.Errorf("expected formatted date string to be %q, got %q", expectedFormatted, formatted)
	}
}

package api

import (
	"strconv"
	"testing"
)

// ptrInt is a helper to take address of literal ints.
func ptrInt(i int) *int {
	return &i
}

func TestMapRowUsingLLM(t *testing.T) {
	tests := []struct {
		name     string
		row      []string
		mapping  ColumnMapping
		expected rawRow
	}{
		{
			name: "Single Amount - Expense Positive (e.g. Plaid)",
			row:  []string{"2026-03-01", "AMAZON", "125.00", "some-other-col"},
			mapping: ColumnMapping{
				DateColIdx:        ptrInt(0),
				VendorColIdx:      ptrInt(1),
				AmountColIdx:      ptrInt(2),
				IsSplitAmount:     false,
				IsExpensePositive: true,
			},
			expected: rawRow{Date: "2026-03-01", Vendor: "AMAZON", Amount: "-125.00"},
		},
		{
			name: "Single Amount - Revenue Negative (e.g. Plaid)",
			row:  []string{"2026-03-02", "STRIPE", "-3000.00", "some-other-col"},
			mapping: ColumnMapping{
				DateColIdx:        ptrInt(0),
				VendorColIdx:      ptrInt(1),
				AmountColIdx:      ptrInt(2),
				IsSplitAmount:     false,
				IsExpensePositive: true,
			},
			expected: rawRow{Date: "2026-03-02", Vendor: "STRIPE", Amount: "3000.00"},
		},
		{
			name: "Single Amount - Expense Negative (Standard App)",
			row:  []string{"03/05/2026", "GUSTO PAYROLL", "4500.00", "-850.50"}, // amount is col 3
			mapping: ColumnMapping{
				DateColIdx:        ptrInt(0),
				DescriptionColIdx: ptrInt(1),
				AmountColIdx:      ptrInt(3),
				IsSplitAmount:     false,
				IsExpensePositive: false,
			},
			expected: rawRow{Date: "03/05/2026", Description: "GUSTO PAYROLL", Amount: "-850.50"},
		},
		{
			name: "Split Amount - Expense Positive",
			row:  []string{"Jan 5", "PG&E", "MoneyOut", "150.00", "MoneyIn", ""},
			mapping: ColumnMapping{
				DateColIdx:        ptrInt(0),
				VendorColIdx:      ptrInt(1),
				DebitColIdx:       ptrInt(3),
				CreditColIdx:      ptrInt(5),
				IsSplitAmount:     true,
				IsExpensePositive: true,
			},
			expected: rawRow{Date: "Jan 5", Vendor: "PG&E", Amount: "-150.00"}, // Expense becomes negative
		},
		{
			name: "Split Amount - Revenue Positive",
			row:  []string{"Jan 6", "CLIENT PAY", "", "MoneyIn", "2500.00"},
			mapping: ColumnMapping{
				DateColIdx:        ptrInt(0),
				VendorColIdx:      ptrInt(1),
				DebitColIdx:       ptrInt(2),
				CreditColIdx:      ptrInt(4),
				IsSplitAmount:     true,
				IsExpensePositive: true,
			},
			expected: rawRow{Date: "Jan 6", Vendor: "CLIENT PAY", Amount: "2500.00"}, // Revenue becomes positive
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapRowUsingLLM(tc.row, tc.mapping)

			if got.Date != tc.expected.Date {
				t.Errorf("Date mismatch: got %q, want %q", got.Date, tc.expected.Date)
			}
			if got.Description != tc.expected.Description {
				t.Errorf("Description mismatch: got %q, want %q", got.Description, tc.expected.Description)
			}
			if got.Vendor != tc.expected.Vendor {
				t.Errorf("Vendor mismatch: got %q, want %q", got.Vendor, tc.expected.Vendor)
			}
			
			gotAmt, _ := strconv.ParseFloat(got.Amount, 64)
			expAmt, _ := strconv.ParseFloat(tc.expected.Amount, 64)

			if gotAmt != expAmt {
				t.Errorf("Amount mismatch: got %q, want %q", got.Amount, tc.expected.Amount)
			}
		})
	}
}

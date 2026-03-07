package quickbooks

import (
	"testing"
	"time"
)

// TestGetMaxTime is moved to connectors package since it's a connector helper function
// See internal/connectors/cdc_test.go

// Test CDC response structure
func TestCDCResponseStructure(t *testing.T) {
	// Test that the CDC response types are properly defined
	response := CDCResponse{
		CDCResponse: []CDCEntity{
			{
				QueryResponse: []QueryResponseItem{
					{
						Account: []Account{
							{Id: "1", Name: "Test Account", AccountType: "Bank"},
						},
					},
				},
			},
		},
		Time: time.Now().Format(time.RFC3339),
	}

	if len(response.CDCResponse) != 1 {
		t.Errorf("Expected 1 CDC entity, got %d", len(response.CDCResponse))
	}

	if len(response.CDCResponse[0].QueryResponse) != 1 {
		t.Errorf("Expected 1 query response, got %d", len(response.CDCResponse[0].QueryResponse))
	}

	if len(response.CDCResponse[0].QueryResponse[0].Account) != 1 {
		t.Errorf("Expected 1 account, got %d", len(response.CDCResponse[0].QueryResponse[0].Account))
	}

	account := response.CDCResponse[0].QueryResponse[0].Account[0]
	if account.Id != "1" {
		t.Errorf("Expected account Id '1', got '%s'", account.Id)
	}
}

// TestQueryResponseItemStructure tests that all entity types can be in QueryResponseItem
func TestQueryResponseItemStructure(t *testing.T) {
	item := QueryResponseItem{
		Account:  []Account{{Id: "1", Name: "Test"}},
		Vendor:   []Vendor{{Id: "2", DisplayName: "Vendor"}},
		Customer: []Customer{{Id: "3", DisplayName: "Customer"}},
		Invoice:  []Invoice{{Id: "4"}},
		Bill:     []Bill{{Id: "5"}},
	}

	if len(item.Account) != 1 {
		t.Errorf("Expected 1 account, got %d", len(item.Account))
	}

	if len(item.Vendor) != 1 {
		t.Errorf("Expected 1 vendor, got %d", len(item.Vendor))
	}

	if len(item.Customer) != 1 {
		t.Errorf("Expected 1 customer, got %d", len(item.Customer))
	}

	if len(item.Invoice) != 1 {
		t.Errorf("Expected 1 invoice, got %d", len(item.Invoice))
	}

	if len(item.Bill) != 1 {
		t.Errorf("Expected 1 bill, got %d", len(item.Bill))
	}
}

// TestCDCDateFormmatting tests the expected date format for CDC queries
func TestCDCDateFormatting(t *testing.T) {
	testCases := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "UTC time",
			input:    time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			expected: "2024-01-15T10:30:00+00:00",
		},
		{
			name:     "PST time",
			input:    time.Date(2024, 2, 20, 14, 45, 0, 0, time.FixedZone("PST", -8*3600)),
			expected: "2024-02-20T14:45:00-08:00",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := tc.input.Format("2006-01-02T15:04:05-07:00")
			if formatted != tc.expected {
				t.Errorf("Date format mismatch: expected %s, got %s", tc.expected, formatted)
			}
		})
	}
}

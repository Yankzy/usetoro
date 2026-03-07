package erp

import (
	"time"
)

// Transaction represents a financial transaction synced from an ERP system.
// This is a common denominator for Purchases, Bills, Journal Entries, etc.
type Transaction struct {
	ToroID      string
	ExternalID  string // ID in the ERP system
	Amount      float64
	VendorName  string
	VendorID    string
	AccountID   string
	Date        time.Time
	Description string
	Memo        string
	SourceType  string // e.g., "Purchase", "Bill", "JournalEntry"
}

// Account represents a general ledger account from an ERP system.
type Account struct {
	ToroID             string
	RealmID            string
	ExternalID         string
	Name               string
	AccountType        string
	AccountSubType     string
	Classification     string // Asset, Liability, Equity, Revenue, Expense
	FullyQualifiedName string
	Active             bool
	CurrentBalance     float64
	SyncToken          string
	Currency           string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Vendor represents a payee or vendor from an ERP system.
type Vendor struct {
	ToroID      string
	RealmID     string
	ExternalID  string
	DisplayName string
	Active      bool
	SyncToken   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Customer represents a customer or payer from an ERP system.
type Customer struct {
	ToroID      string
	RealmID     string
	ExternalID  string
	DisplayName string
	Active      bool
	SyncToken   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// JournalEntry represents a Journal Entry for pushing corrections back to the ERP.
type JournalEntry struct {
	ExternalID string
	Date       time.Time
	Lines      []JournalEntryLine
	Memo       string
}

type JournalEntryLine struct {
	AccountID   string
	Amount      float64
	PostingType string // "Debit" or "Credit"
	Description string
}

// CDCSyncResult represents the result of a generic Change Data Capture sync.
type CDCSyncResult struct {
	LatestSyncToken time.Time
	Accounts        []Account
	Vendors         []Vendor
	Customers       []Customer
	Transactions    []Transaction
}

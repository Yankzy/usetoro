package erp

import (
	"context"
	"io"
	"time"
)

// ExpenseInput represents the data needed to post an expense to an ERP.
type ExpenseInput struct {
	RealmID     string
	Description string
	Amount      float64
	Paid        bool      // true → immediate/paid (Purchase); false → payable (Bill)
	TxnDate     time.Time // defaults to today if zero
	AccountHint string    // ERP account ID (skips CoAMapper)
	VendorHint  string    // ERP vendor ID (skips EntityResolver)
	Vendor      string    // human-readable vendor name
	Customer    string
	Memo        string
	MCC         string // Merchant Category Code
	InvoiceText string // extracted invoice/receipt text
}

// PostedExpense is returned after successfully posting the expense to the ERP.
type PostedExpense struct {
	ERPEntityID  string // ID of the created entity in the ERP
	EntityType   string // e.g., "Purchase" or "Bill"
	AccountERPID string // The matched account ID in the ERP
	VendorERPID  string // The matched vendor ID in the ERP
	Amount       float64
}

// UploadReceiptInput contains everything needed to upload a receipt and link it
// to an existing ERP transaction.
type UploadReceiptInput struct {
	RealmID     string
	FileName    string
	ContentType string // e.g. "application/pdf"
	Data        io.Reader
	EntityID    string // ERP transaction ID to link the receipt to
	EntityType  string // "Purchase" or "Bill"
	Note        string // optional description shown on the attachment in ERP
}

// UploadedReceipt is returned after a successful receipt upload.
type UploadedReceipt struct {
	AttachmentID   string
	FileAccessURI  string // direct ERP storage URL
	LinkedEntityID string
}

// Provider represents a generic ERP integration.
// It abstracts away the specific implementation details of QuickBooks, NetSuite, etc.
type Provider struct {
	// SyncCDC fetches incrementally updated entities since the specified timestamp.
	SyncCDC func(ctx context.Context, since time.Time) (*CDCSyncResult, error)

	// FetchTransactions gets transactions for a given date range.
	FetchTransactions func(ctx context.Context, since time.Time) ([]Transaction, error)

	// FetchAccounts gets all active accounts from the ERP.
	FetchAccounts func(ctx context.Context) ([]Account, error)

	// PostExpense creates a paid expense or bill in the ERP.
	PostExpense func(ctx context.Context, input ExpenseInput) (*PostedExpense, error)

	// UpdateExpenseCategory modifies the mapped account/vendor for an existing expense.
	UpdateExpenseCategory func(ctx context.Context, erpID string, entityType string, newAccountID string, newVendorID string) error

	// UploadReceipt uploads a file and attaches it to an existing ERP entity.
	UploadReceipt func(ctx context.Context, input UploadReceiptInput) (*UploadedReceipt, error)
}

// ProviderFactory is responsible for returning the correct ERP Provider for a given tenant.
type ProviderFactory interface {
	GetProviderForTenant(ctx context.Context, tenantID string) (*Provider, error)
	GetProviderForRealm(ctx context.Context, erpSystem, realmID string) (*Provider, error)
}

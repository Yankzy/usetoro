package quickbooks

import (
	"strconv"
	"time"

	"github.com/Yankzy/usetoro/internal/erp"
	sdk "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

// MapAccount translates a QBO Account to a Toro ERP Account.
func MapAccount(qboAcct sdk.Account) erp.Account {
	var currentBalance float64
	if string(qboAcct.CurrentBalance) != "" {
		currentBalance, _ = strconv.ParseFloat(string(qboAcct.CurrentBalance), 64)
	}

	return erp.Account{
		ExternalID:         qboAcct.Id,
		Name:               qboAcct.Name,
		AccountType:        qboAcct.AccountType,
		AccountSubType:     qboAcct.AccountSubType,
		Classification:     qboAcct.Classification,
		FullyQualifiedName: qboAcct.FullyQualifiedName,
		Active:             qboAcct.Active,
		CurrentBalance:     currentBalance,
		Currency:           qboAcct.CurrencyRef.Value,
		UpdatedAt:          parseQboDate(qboAcct.MetaData.LastUpdatedTime),
	}
}

// MapVendor translates a QBO Vendor to a Toro ERP Vendor.
func MapVendor(qboVendor sdk.Vendor) erp.Vendor {
	return erp.Vendor{
		ExternalID:  qboVendor.Id,
		DisplayName: qboVendor.DisplayName,
		Active:      qboVendor.Active,
		UpdatedAt:   qboVendor.MetaData.LastUpdatedTime.Time,
	}
}

// MapCustomer translates a QBO Customer to a Toro ERP Customer.
func MapCustomer(qboCustomer sdk.Customer) erp.Customer {
	return erp.Customer{
		ExternalID:  qboCustomer.Id,
		DisplayName: qboCustomer.DisplayName,
		Active:      qboCustomer.Active,
		UpdatedAt:   qboCustomer.MetaData.LastUpdatedTime.Time,
	}
}

// MapPurchaseToTransaction translates a QBO Purchase (e.g. cash, check, credit card charge) to a Toro ERP Transaction.
func MapPurchaseToTransaction(p sdk.Purchase) erp.Transaction {
	amount, _ := strconv.ParseFloat(string(p.TotalAmt), 64)

	// Try to get account from the first line item
	var accountID string
	var description string
	if len(p.Line) > 0 {
		accountID = p.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value
		description = p.Line[0].Description
	}

	if description == "" {
		description = p.PrivateNote
	}

	return erp.Transaction{
		ExternalID:  p.Id,
		Amount:      amount,
		VendorID:    p.EntityRef.Value,
		VendorName:  p.EntityRef.Name,
		AccountID:   accountID,
		Date:        p.TxnDate.Time,
		Description: description,
		Memo:        p.PrivateNote,
		SourceType:  "Purchase",
	}
}

// MapBillToTransaction translates a QBO Bill (Accounts Payable) to a Toro ERP Transaction.
func MapBillToTransaction(b sdk.Bill) erp.Transaction {
	amount, _ := strconv.ParseFloat(string(b.TotalAmt), 64)

	var accountID string
	var description string
	if len(b.Line) > 0 {
		accountID = b.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value
		description = b.Line[0].Description
	}

	if description == "" {
		description = b.PrivateNote
	}

	return erp.Transaction{
		ExternalID:  b.Id,
		Amount:      amount,
		VendorID:    b.VendorRef.Value,
		VendorName:  b.VendorRef.Name,
		AccountID:   accountID,
		Date:        b.TxnDate.Time,
		Description: description,
		Memo:        b.PrivateNote,
		SourceType:  "Bill",
	}
}

// MapJournalEntryToTransaction translates a QBO Journal Entry to a Toro ERP Transaction.
func MapJournalEntryToTransaction(j sdk.JournalEntry) erp.Transaction {
	var amount float64
	var description string
	var accountID string

	// For a simple view, we just grab the first line's details
	if len(j.Line) > 0 {
		amount, _ = strconv.ParseFloat(string(j.Line[0].Amount), 64)
		description = j.Line[0].Description
		accountID = j.Line[0].JournalEntryLineDetail.AccountRef.Value
	}

	return erp.Transaction{
		ExternalID:  j.Id,
		Amount:      amount,
		AccountID:   accountID,
		Date:        j.TxnDate.Time,
		Description: description,
		Memo:        j.PrivateNote,
		SourceType:  "JournalEntry",
	}
}

// parseQboDate parses Intuit's YYYY-MM-DD date format.
func parseQboDate(d sdk.Date) time.Time {
	return d.Time
}

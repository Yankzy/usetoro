package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/erp"
	sdk "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

// Adapter implements the erp.Provider interface for QuickBooks Online.
type Adapter struct {
	client *sdk.Client
}

// NewAdapter creates a new QuickBooks adapter.
func NewAdapter(client *sdk.Client) *Adapter {
	return &Adapter{
		client: client,
	}
}

// SyncCDC implementation for QBO
func (a *Adapter) SyncCDC(ctx context.Context, since time.Time) (*erp.CDCSyncResult, error) {
	// 1. We ask QBO for changes to these specific entities since the last sync time
	entitiesToSync := "Account,Vendor,Customer,Purchase,JournalEntry,Bill"
	qboResult, err := a.client.QueryCDC(entitiesToSync, since)
	if err != nil {
		return nil, WrapError(err)
	}

	result := &erp.CDCSyncResult{
		LatestSyncToken: time.Now(), // Record the time we made the request
	}

	// 2. Parse the QBO CDC response (which is a deeply nested mess) into clean Toro models
	for _, entityList := range qboResult.CDCResponse {
		for _, qboQuery := range entityList.QueryResponse {

			for _, qboAcct := range qboQuery.Account {
				result.Accounts = append(result.Accounts, MapAccount(qboAcct))
			}

			for _, qboVendor := range qboQuery.Vendor {
				result.Vendors = append(result.Vendors, MapVendor(qboVendor))
			}

			for _, qboCustomer := range qboQuery.Customer {
				result.Customers = append(result.Customers, MapCustomer(qboCustomer))
			}

			// Map various QBO transaction types into Toro's generic Transaction struct
			for _, qboPurchase := range qboQuery.Purchase {
				result.Transactions = append(result.Transactions, MapPurchaseToTransaction(qboPurchase))
			}

			for _, qboBill := range qboQuery.Bill {
				result.Transactions = append(result.Transactions, MapBillToTransaction(qboBill))
			}

			for _, qboJE := range qboQuery.JournalEntry {
				result.Transactions = append(result.Transactions, MapJournalEntryToTransaction(qboJE))
			}
		}
	}

	return result, nil
}

// FetchTransactions implementation for QBO
func (a *Adapter) FetchTransactions(ctx context.Context, since time.Time) ([]erp.Transaction, error) {
	// A simpler version of CDC that only cares about transactions.
	// We'll reuse the CDC endpoint but ask for less data.
	qboResult, err := a.client.QueryCDC("Purchase,JournalEntry,Bill", since)
	if err != nil {
		return nil, WrapError(err)
	}

	var transactions []erp.Transaction

	for _, entityList := range qboResult.CDCResponse {
		for _, qboQuery := range entityList.QueryResponse {
			for _, qboPurchase := range qboQuery.Purchase {
				transactions = append(transactions, MapPurchaseToTransaction(qboPurchase))
			}
			for _, qboBill := range qboQuery.Bill {
				transactions = append(transactions, MapBillToTransaction(qboBill))
			}
			for _, qboJE := range qboQuery.JournalEntry {
				transactions = append(transactions, MapJournalEntryToTransaction(qboJE))
			}
		}
	}

	return transactions, nil
}

// FetchAccounts gets all active accounts from QBO
func (a *Adapter) FetchAccounts(ctx context.Context) ([]erp.Account, error) {
	// Let's implement this properly using the generic query method.
	query := "SELECT * FROM Account WHERE Active = true MAXRESULTS 1000"

	type QboQueryResponse struct {
		QueryResponse struct {
			Account []sdk.Account `json:"Account"`
		} `json:"QueryResponse"`
	}

	var res QboQueryResponse
	err := a.client.Query(query, &res)
	if err != nil {
		return nil, WrapError(err)
	}

	var accounts []erp.Account
	for _, acct := range res.QueryResponse.Account {
		accounts = append(accounts, MapAccount(acct))
	}

	return accounts, nil
}

// PostExpense creates a Purchase (paid) or Bill (unpaid) in QBO
func (a *Adapter) PostExpense(ctx context.Context, input erp.ExpenseInput) (*erp.PostedExpense, error) {
	amountStr := fmt.Sprintf("%.2f", input.Amount)

	if input.Paid {
		purchase := &sdk.Purchase{
			PaymentType: "Cash",
			AccountRef:  sdk.ReferenceType{Value: input.AccountHint},
			TxnDate:     sdk.Date{Time: input.TxnDate},
			PrivateNote: input.Description,
			Line: []sdk.Line{
				{
					Amount:      json.Number(amountStr),
					DetailType:  "AccountBasedExpenseLineDetail",
					Description: input.Description,
					AccountBasedExpenseLineDetail: sdk.AccountBasedExpenseLineDetail{
						AccountRef: sdk.ReferenceType{Value: input.AccountHint},
					},
				},
			},
		}

		if input.VendorHint != "" {
			purchase.EntityRef = sdk.ReferenceType{Value: input.VendorHint, Type: "Vendor"}
		}

		created, err := a.client.CreatePurchase(purchase)
		if err != nil {
			return nil, WrapError(err)
		}

		return &erp.PostedExpense{
			ERPEntityID:  created.Id,
			EntityType:   "Purchase",
			AccountERPID: input.AccountHint,
			VendorERPID:  input.VendorHint,
			Amount:       input.Amount,
		}, nil

	} else {
		if input.VendorHint == "" {
			return nil, erp.ErrBadRequest
		}

		bill := &sdk.Bill{
			VendorRef:   sdk.ReferenceType{Value: input.VendorHint},
			TxnDate:     sdk.Date{Time: input.TxnDate},
			PrivateNote: input.Description,
			Line: []sdk.Line{
				{
					Amount:      json.Number(amountStr),
					DetailType:  "AccountBasedExpenseLineDetail",
					Description: input.Description,
					AccountBasedExpenseLineDetail: sdk.AccountBasedExpenseLineDetail{
						AccountRef: sdk.ReferenceType{Value: input.AccountHint},
					},
				},
			},
		}

		created, err := a.client.CreateBill(bill)
		if err != nil {
			return nil, WrapError(err)
		}

		return &erp.PostedExpense{
			ERPEntityID:  created.Id,
			EntityType:   "Bill",
			AccountERPID: input.AccountHint,
			VendorERPID:  input.VendorHint,
			Amount:       input.Amount,
		}, nil
	}
}

// UploadReceipt uploads a file and attaches it to an existing QBO entity.
func (a *Adapter) UploadReceipt(ctx context.Context, input erp.UploadReceiptInput) (*erp.UploadedReceipt, error) {
	// Build the Attachable metadata
	attachable := &sdk.Attachable{
		FileName:    input.FileName,
		ContentType: sdk.ContentType(input.ContentType),
		Note:        input.Note,
		AttachableRef: []sdk.AttachableRef{
			{
				EntityRef: sdk.ReferenceType{
					Value: input.EntityID,
					Type:  input.EntityType,
				},
			},
		},
	}

	created, err := a.client.UploadAttachable(attachable, input.Data)
	if err != nil {
		return nil, WrapError(err)
	}

	return &erp.UploadedReceipt{
		AttachmentID:   created.Id,
		FileAccessURI:  created.FileAccessUri,
		LinkedEntityID: input.EntityID,
	}, nil
}

// WrapError translates QBO SDK errors into generic Toro ERP errors.
func WrapError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check if the old client is returning specific types
	var objNotFound sdk.ObjectNotFoundError
	if errors.As(err, &objNotFound) {
		return fmt.Errorf("%w: %v", erp.ErrNotFound, err)
	}

	// Or string matching for common HTTP/API errors from Intuit
	if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") {
		return fmt.Errorf("%w: %v", erp.ErrRateLimited, err)
	}

	if strings.Contains(errStr, "auth") || strings.Contains(errStr, "401") || strings.Contains(errStr, "token") {
		return fmt.Errorf("%w: %v", erp.ErrAuthFailed, err)
	}

	// Default fallback wrap
	return fmt.Errorf("qbo adapter error: %w", err)
}

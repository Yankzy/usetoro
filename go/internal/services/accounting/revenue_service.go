package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

// QBOClientFn returns an authenticated QBO API client for a specific tenant (Realm).
// Temporarily retained for services that are not yet generic.
type QBOClientFn func(ctx context.Context, realmID string) (*quickbooks.Client, error)

type invoiceCreator interface {
	CreateInvoice(*quickbooks.Invoice) (*quickbooks.Invoice, error)
}

type paymentCreator interface {
	CreatePayment(*quickbooks.Payment) (*quickbooks.Payment, error)
}

type depositCreator interface {
	CreateDeposit(*quickbooks.Deposit) (*quickbooks.Deposit, error)
}

// RevenueService posts AR-side transactions to QBO: Invoices, Payments, Deposits.
// It intentionally mirrors TransactionService's "service layer" shape without
// coupling to proposed_transactions until the repo schema supports customer-centric idempotency.
type RevenueService struct {
	logger   *slog.Logger
	clientFn QBOClientFn
}

func NewRevenueService(logger *slog.Logger, clientFn QBOClientFn) *RevenueService {
	return &RevenueService{
		logger:   logger,
		clientFn: clientFn,
	}
}

type InvoiceInput struct {
	RealmID string

	CustomerHint string // QBO Customer ID (required)
	ItemHint     string // QBO Item ID (required)

	Description string
	Amount      float64

	TxnDate time.Time
	DueDate time.Time
}

type PostedInvoice struct {
	QBOInvoiceID  string
	CustomerERPID string
	ItemERPID     string
	Amount        float64
}

func (s *RevenueService) PostInvoice(ctx context.Context, input InvoiceInput) (*PostedInvoice, error) {
	return s.postInvoice(ctx, input, nil)
}

func (s *RevenueService) postInvoice(ctx context.Context, input InvoiceInput, icOverride invoiceCreator) (*PostedInvoice, error) {
	if input.RealmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}
	if input.CustomerHint == "" {
		return nil, fmt.Errorf("CustomerHint is required")
	}
	if input.ItemHint == "" {
		return nil, fmt.Errorf("ItemHint is required")
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("amount must be > 0")
	}

	txnDate := input.TxnDate
	if txnDate.IsZero() {
		txnDate = time.Now()
	}

	var ic invoiceCreator = icOverride
	if ic == nil {
		client, err := s.clientFn(ctx, input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO client: %w", err)
		}
		ic = client
	}

	amountStr := json.Number(formatMoney2(input.Amount))
	inv := &quickbooks.Invoice{
		CustomerRef: quickbooks.ReferenceType{Value: input.CustomerHint},
		TxnDate:     quickbooks.Date{Time: txnDate},
		PrivateNote: input.Description,
		Line: []quickbooks.Line{
			{
				Amount:      amountStr,
				DetailType:  "SalesItemLineDetail",
				Description: input.Description,
				SalesItemLineDetail: quickbooks.SalesItemLineDetail{
					ItemRef: quickbooks.ReferenceType{Value: input.ItemHint},
					Qty:     1,
				},
			},
		},
	}
	if !input.DueDate.IsZero() {
		inv.DueDate = quickbooks.Date{Time: input.DueDate}
	}

	created, err := ic.CreateInvoice(inv)
	if err != nil {
		return nil, fmt.Errorf("failed to create Invoice in QBO: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("✅ Created QBO Invoice",
			"realm_id", input.RealmID,
			"invoice_id", created.Id,
			"amount", input.Amount,
			"customer_id", input.CustomerHint,
			"item_id", input.ItemHint,
		)
	}

	return &PostedInvoice{
		QBOInvoiceID:  created.Id,
		CustomerERPID: input.CustomerHint,
		ItemERPID:     input.ItemHint,
		Amount:        input.Amount,
	}, nil
}

type PaymentInput struct {
	RealmID string

	CustomerHint         string // QBO Customer ID (required)
	DepositToAccountHint string // optional; if empty QBO uses Undeposited Funds

	Amount  float64
	TxnDate time.Time

	// Optional linkage to apply the payment.
	LinkedTxnID   string // e.g. Invoice ID
	LinkedTxnType string // e.g. "Invoice"
}

type PostedPayment struct {
	QBOPaymentID  string
	CustomerERPID string
	Amount        float64
}

func (s *RevenueService) PostPayment(ctx context.Context, input PaymentInput) (*PostedPayment, error) {
	return s.postPayment(ctx, input, nil)
}

func (s *RevenueService) postPayment(ctx context.Context, input PaymentInput, pcOverride paymentCreator) (*PostedPayment, error) {
	if input.RealmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}
	if input.CustomerHint == "" {
		return nil, fmt.Errorf("CustomerHint is required")
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("amount must be > 0")
	}

	txnDate := input.TxnDate
	if txnDate.IsZero() {
		txnDate = time.Now()
	}

	var pc paymentCreator = pcOverride
	if pc == nil {
		client, err := s.clientFn(ctx, input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO client: %w", err)
		}
		pc = client
	}

	p := &quickbooks.Payment{
		CustomerRef: quickbooks.ReferenceType{Value: input.CustomerHint},
		TxnDate:     quickbooks.Date{Time: txnDate},
		TotalAmt:    input.Amount,
	}
	if input.DepositToAccountHint != "" {
		p.DepositToAccountRef = quickbooks.ReferenceType{Value: input.DepositToAccountHint}
	}
	if input.LinkedTxnID != "" && input.LinkedTxnType != "" {
		p.Line = []quickbooks.PaymentLine{
			{
				Amount: input.Amount,
				LinkedTxn: []quickbooks.LinkedTxn{
					{TxnID: input.LinkedTxnID, TxnType: input.LinkedTxnType},
				},
			},
		}
	}

	created, err := pc.CreatePayment(p)
	if err != nil {
		return nil, fmt.Errorf("failed to create Payment in QBO: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("✅ Created QBO Payment",
			"realm_id", input.RealmID,
			"payment_id", created.Id,
			"amount", input.Amount,
			"customer_id", input.CustomerHint,
		)
	}

	return &PostedPayment{
		QBOPaymentID:  created.Id,
		CustomerERPID: input.CustomerHint,
		Amount:        input.Amount,
	}, nil
}

type DepositInput struct {
	RealmID string

	DepositToAccountHint string // QBO Account ID (required)
	FromAccountHint      string // QBO Account ID (required)

	Amount  float64
	TxnDate time.Time

	// Optional linkage (e.g., deposit a payment)
	LinkedTxnID   string // e.g. Payment ID
	LinkedTxnType string // e.g. "Payment"
}

type PostedDeposit struct {
	QBODepositID string
	Amount       float64
}

func (s *RevenueService) PostDeposit(ctx context.Context, input DepositInput) (*PostedDeposit, error) {
	return s.postDeposit(ctx, input, nil)
}

func (s *RevenueService) postDeposit(ctx context.Context, input DepositInput, dcOverride depositCreator) (*PostedDeposit, error) {
	if input.RealmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}
	if input.DepositToAccountHint == "" {
		return nil, fmt.Errorf("DepositToAccountHint is required")
	}
	if input.FromAccountHint == "" {
		return nil, fmt.Errorf("FromAccountHint is required")
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("amount must be > 0")
	}

	txnDate := input.TxnDate
	if txnDate.IsZero() {
		txnDate = time.Now()
	}

	var dc depositCreator = dcOverride
	if dc == nil {
		client, err := s.clientFn(ctx, input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO client: %w", err)
		}
		dc = client
	}

	line := quickbooks.DepositLine{
		Amount:     json.Number(formatMoney2(input.Amount)),
		DetailType: "DepositLineDetail",
		DepositLineDetail: quickbooks.DepositLineDetail{
			AccountRef: quickbooks.ReferenceType{Value: input.FromAccountHint},
		},
	}
	if input.LinkedTxnID != "" && input.LinkedTxnType != "" {
		line.LinkedTxn = []quickbooks.LinkedTxn{
			{TxnID: input.LinkedTxnID, TxnType: input.LinkedTxnType},
		}
	}

	dep := &quickbooks.Deposit{
		DepositToAccountRef: quickbooks.ReferenceType{Value: input.DepositToAccountHint},
		TxnDate:             quickbooks.Date{Time: txnDate},
		TotalAmt:            json.Number(formatMoney2(input.Amount)),
		Line:                []quickbooks.DepositLine{line},
	}

	created, err := dc.CreateDeposit(dep)
	if err != nil {
		return nil, fmt.Errorf("failed to create Deposit in QBO: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("✅ Created QBO Deposit",
			"realm_id", input.RealmID,
			"deposit_id", created.Id,
			"amount", input.Amount,
			"to_account", input.DepositToAccountHint,
		)
	}

	return &PostedDeposit{
		QBODepositID: created.Id,
		Amount:       input.Amount,
	}, nil
}

func formatMoney2(v float64) string {
	// 2dp fixed-point for QBO payloads.
	// strconv is allocation-light and avoids fmt overhead in hot paths.
	return strconv.FormatFloat(v, 'f', 2, 64)
}

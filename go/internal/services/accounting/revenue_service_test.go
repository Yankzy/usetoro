package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failClientFn always returns an error from the QBO client resolver.
func failClientFn(msg string) QBOClientFn {
	return func(ctx context.Context, realmID string) (*quickbooks.Client, error) {
		return nil, errors.New(msg)
	}
}

type mockInvoiceCreator struct {
	result *quickbooks.Invoice
	err    error
	called bool
	got    *quickbooks.Invoice
}

func (m *mockInvoiceCreator) CreateInvoice(i *quickbooks.Invoice) (*quickbooks.Invoice, error) {
	m.called = true
	m.got = i
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &quickbooks.Invoice{Id: "inv-default"}, nil
}

type mockPaymentCreator struct {
	result *quickbooks.Payment
	err    error
	called bool
	got    *quickbooks.Payment
}

func (m *mockPaymentCreator) CreatePayment(p *quickbooks.Payment) (*quickbooks.Payment, error) {
	m.called = true
	m.got = p
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &quickbooks.Payment{Id: "pay-default"}, nil
}

type mockDepositCreator struct {
	result *quickbooks.Deposit
	err    error
	called bool
	got    *quickbooks.Deposit
}

func (m *mockDepositCreator) CreateDeposit(d *quickbooks.Deposit) (*quickbooks.Deposit, error) {
	m.called = true
	m.got = d
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &quickbooks.Deposit{Id: "dep-default"}, nil
}

func TestPostInvoice_Validation(t *testing.T) {
	svc := NewRevenueService(logger(), failClientFn("unused"))

	_, err := svc.postInvoice(context.Background(), InvoiceInput{}, &mockInvoiceCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realmID is required")

	_, err = svc.postInvoice(context.Background(), InvoiceInput{RealmID: "r1"}, &mockInvoiceCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CustomerHint is required")

	_, err = svc.postInvoice(context.Background(), InvoiceInput{RealmID: "r1", CustomerHint: "c1"}, &mockInvoiceCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ItemHint is required")

	_, err = svc.postInvoice(context.Background(), InvoiceInput{RealmID: "r1", CustomerHint: "c1", ItemHint: "it1", Amount: 0}, &mockInvoiceCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount must be > 0")
}

func TestPostInvoice_BuildsSingleLineSalesItemInvoice(t *testing.T) {
	ic := &mockInvoiceCreator{result: &quickbooks.Invoice{Id: "inv-001"}}
	svc := NewRevenueService(logger(), failClientFn("unused"))
	due := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	txn := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	out, err := svc.postInvoice(context.Background(), InvoiceInput{
		RealmID:      "realm-1",
		CustomerHint: "cust-1",
		ItemHint:     "item-1",
		Description:  "January retainer",
		Amount:       1234.5,
		TxnDate:      txn,
		DueDate:      due,
	}, ic)

	require.NoError(t, err)
	require.True(t, ic.called)
	require.NotNil(t, ic.got)
	assert.Equal(t, "cust-1", ic.got.CustomerRef.Value)
	assert.Equal(t, txn, ic.got.TxnDate.Time)
	assert.Equal(t, due, ic.got.DueDate.Time)
	require.Len(t, ic.got.Line, 1)
	assert.Equal(t, "SalesItemLineDetail", ic.got.Line[0].DetailType)
	assert.Equal(t, "January retainer", ic.got.Line[0].Description)
	assert.Equal(t, "1234.50", ic.got.Line[0].Amount.String())
	assert.Equal(t, "item-1", ic.got.Line[0].SalesItemLineDetail.ItemRef.Value)
	assert.Equal(t, float32(1), ic.got.Line[0].SalesItemLineDetail.Qty)

	assert.Equal(t, "inv-001", out.QBOInvoiceID)
	assert.Equal(t, "cust-1", out.CustomerERPID)
	assert.Equal(t, "item-1", out.ItemERPID)
	assert.InDelta(t, 1234.5, out.Amount, 1e-9)
}

func TestPostPayment_BuildsPaymentWithOptionalLinkage(t *testing.T) {
	pc := &mockPaymentCreator{result: &quickbooks.Payment{Id: "pay-001"}}
	svc := NewRevenueService(logger(), failClientFn("unused"))
	txn := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	out, err := svc.postPayment(context.Background(), PaymentInput{
		RealmID:              "r1",
		CustomerHint:         "cust-1",
		DepositToAccountHint: "bank-1",
		Amount:               200,
		TxnDate:              txn,
		LinkedTxnID:          "inv-123",
		LinkedTxnType:        "Invoice",
	}, pc)

	require.NoError(t, err)
	require.True(t, pc.called)
	require.NotNil(t, pc.got)
	assert.Equal(t, "cust-1", pc.got.CustomerRef.Value)
	assert.Equal(t, "bank-1", pc.got.DepositToAccountRef.Value)
	assert.Equal(t, txn, pc.got.TxnDate.Time)
	assert.InDelta(t, 200, pc.got.TotalAmt, 1e-9)
	require.Len(t, pc.got.Line, 1)
	assert.InDelta(t, 200, pc.got.Line[0].Amount, 1e-9)
	require.Len(t, pc.got.Line[0].LinkedTxn, 1)
	assert.Equal(t, "inv-123", pc.got.Line[0].LinkedTxn[0].TxnID)
	assert.Equal(t, "Invoice", pc.got.Line[0].LinkedTxn[0].TxnType)

	assert.Equal(t, "pay-001", out.QBOPaymentID)
}

func TestPostDeposit_BuildsDepositWithLineDetail(t *testing.T) {
	dc := &mockDepositCreator{result: &quickbooks.Deposit{Id: "dep-001"}}
	svc := NewRevenueService(logger(), failClientFn("unused"))
	txn := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)

	out, err := svc.postDeposit(context.Background(), DepositInput{
		RealmID:              "r1",
		DepositToAccountHint: "bank-1",
		FromAccountHint:      "income-1",
		Amount:               50,
		TxnDate:              txn,
		LinkedTxnID:          "pay-123",
		LinkedTxnType:        "Payment",
	}, dc)

	require.NoError(t, err)
	require.True(t, dc.called)
	require.NotNil(t, dc.got)
	assert.Equal(t, "bank-1", dc.got.DepositToAccountRef.Value)
	assert.Equal(t, txn, dc.got.TxnDate.Time)
	assert.Equal(t, "50.00", dc.got.TotalAmt.String())
	require.Len(t, dc.got.Line, 1)
	assert.Equal(t, "DepositLineDetail", dc.got.Line[0].DetailType)
	assert.Equal(t, "50.00", dc.got.Line[0].Amount.String())
	assert.Equal(t, "income-1", dc.got.Line[0].DepositLineDetail.AccountRef.Value)
	require.Len(t, dc.got.Line[0].LinkedTxn, 1)
	assert.Equal(t, "pay-123", dc.got.Line[0].LinkedTxn[0].TxnID)
	assert.Equal(t, "Payment", dc.got.Line[0].LinkedTxn[0].TxnType)

	assert.Equal(t, "dep-001", out.QBODepositID)
}

func TestPostInvoice_QBOErrorBubbles(t *testing.T) {
	ic := &mockInvoiceCreator{err: errors.New("qbo down")}
	svc := NewRevenueService(logger(), failClientFn("unused"))

	_, err := svc.postInvoice(context.Background(), InvoiceInput{
		RealmID:      "r1",
		CustomerHint: "c1",
		ItemHint:     "i1",
		Amount:       10,
	}, ic)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Invoice")
}

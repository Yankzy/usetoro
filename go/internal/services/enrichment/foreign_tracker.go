package enrichment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ForeignServiceRunningTotalKey uniquely identifies a period running total ledger entry.
type ForeignServiceRunningTotalKey struct {
	RealmID     string
	ProviderID  uuid.UUID
	FiscalYear  int
	FiscalMonth int
}

// ForeignServiceRunningTotal represents a period ledger entry.
type ForeignServiceRunningTotal struct {
	ID                         uuid.UUID
	RealmID                    string
	ProviderID                 uuid.UUID
	FiscalYear                 int
	FiscalMonth                int
	CumulativeGrossInvoicedMAD float64
	CumulativeRASWithheldMAD   float64
	CumulativeNetPaidMAD       float64
	TransactionCount           int
	DGIDeclarationStatus       string
	LastTransactionAt          time.Time
}

// ForeignProviderTracker handles real-time accumulation of foreign service spend and 10% RAS.
type ForeignProviderTracker struct {
	mu      sync.RWMutex
	totals  map[ForeignServiceRunningTotalKey]*ForeignServiceRunningTotal
}

// NewForeignProviderTracker initializes the tracker.
func NewForeignProviderTracker() *ForeignProviderTracker {
	return &ForeignProviderTracker{
		totals: make(map[ForeignServiceRunningTotalKey]*ForeignServiceRunningTotal),
	}
}

// CalculateDGIFilingDeadline returns the statutory Moroccan filing deadline (20th of following month).
func CalculateDGIFilingDeadline(fiscalYear int, fiscalMonth int) string {
	nextMonth := fiscalMonth + 1
	nextYear := fiscalYear
	if nextMonth > 12 {
		nextMonth = 1
		nextYear++
	}
	return fmt.Sprintf("%04d-%02d-20", nextYear, nextMonth)
}

// RecordForeignTransaction increments running totals and returns updated period and annual metrics.
func (fpt *ForeignProviderTracker) RecordForeignTransaction(
	ctx context.Context,
	realmID string,
	providerID uuid.UUID,
	grossMAD float64,
	rasMAD float64,
	netMAD float64,
	txnDate time.Time,
) (*ForeignProviderRunningTotals, error) {
	fpt.mu.Lock()
	defer fpt.mu.Unlock()

	year := txnDate.Year()
	month := int(txnDate.Month())

	key := ForeignServiceRunningTotalKey{
		RealmID:     realmID,
		ProviderID:  providerID,
		FiscalYear:  year,
		FiscalMonth: month,
	}

	total, ok := fpt.totals[key]
	if !ok {
		total = &ForeignServiceRunningTotal{
			ID:                         uuid.New(),
			RealmID:                    realmID,
			ProviderID:                 providerID,
			FiscalYear:                 year,
			FiscalMonth:                month,
			CumulativeGrossInvoicedMAD: 0.0,
			CumulativeRASWithheldMAD:   0.0,
			CumulativeNetPaidMAD:       0.0,
			TransactionCount:           0,
			DGIDeclarationStatus:       "PENDING",
		}
		fpt.totals[key] = total
	}

	total.CumulativeGrossInvoicedMAD = round2Decimals(total.CumulativeGrossInvoicedMAD + grossMAD)
	total.CumulativeRASWithheldMAD = round2Decimals(total.CumulativeRASWithheldMAD + rasMAD)
	total.CumulativeNetPaidMAD = round2Decimals(total.CumulativeNetPaidMAD + netMAD)
	total.TransactionCount++
	total.LastTransactionAt = txnDate

	// Compute annual totals for this realm & provider
	annualGross := 0.0
	annualRAS := 0.0
	for k, v := range fpt.totals {
		if k.RealmID == realmID && k.ProviderID == providerID && k.FiscalYear == year {
			annualGross += v.CumulativeGrossInvoicedMAD
			annualRAS += v.CumulativeRASWithheldMAD
		}
	}

	return &ForeignProviderRunningTotals{
		FiscalYear:                        year,
		FiscalMonth:                       month,
		CurrentMonthCumulativeInvoicedMAD: total.CumulativeGrossInvoicedMAD,
		CurrentMonthCumulativeRASMAD:      total.CumulativeRASWithheldMAD,
		CurrentYearCumulativeInvoicedMAD:  round2Decimals(annualGross),
		CurrentYearCumulativeRASMAD:       round2Decimals(annualRAS),
		DGIFilingDeadline:                 CalculateDGIFilingDeadline(year, month),
	}, nil
}

// GetRunningTotals retrieves the current month and annual totals without recording a new transaction.
func (fpt *ForeignProviderTracker) GetRunningTotals(
	ctx context.Context,
	realmID string,
	providerID uuid.UUID,
	txnDate time.Time,
) *ForeignProviderRunningTotals {
	fpt.mu.RLock()
	defer fpt.mu.RUnlock()

	year := txnDate.Year()
	month := int(txnDate.Month())

	key := ForeignServiceRunningTotalKey{
		RealmID:     realmID,
		ProviderID:  providerID,
		FiscalYear:  year,
		FiscalMonth: month,
	}

	var curMonthGross, curMonthRAS float64
	if total, ok := fpt.totals[key]; ok {
		curMonthGross = total.CumulativeGrossInvoicedMAD
		curMonthRAS = total.CumulativeRASWithheldMAD
	}

	annualGross := 0.0
	annualRAS := 0.0
	for k, v := range fpt.totals {
		if k.RealmID == realmID && k.ProviderID == providerID && k.FiscalYear == year {
			annualGross += v.CumulativeGrossInvoicedMAD
			annualRAS += v.CumulativeRASWithheldMAD
		}
	}

	return &ForeignProviderRunningTotals{
		FiscalYear:                        year,
		FiscalMonth:                       month,
		CurrentMonthCumulativeInvoicedMAD: curMonthGross,
		CurrentMonthCumulativeRASMAD:      curMonthRAS,
		CurrentYearCumulativeInvoicedMAD:  round2Decimals(annualGross),
		CurrentYearCumulativeRASMAD:       round2Decimals(annualRAS),
		DGIFilingDeadline:                 CalculateDGIFilingDeadline(year, month),
	}
}

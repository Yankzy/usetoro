package accounting

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/qbo"
)

// reconcileEpsilon is the half-cent tolerance used when comparing QBO report
// amounts to shadow-DB balances, absorbing floating-point and rounding drift.
const reconcileEpsilon = 0.005

// Discrepancy represents a mismatch between Toro's Shadow DB and QBO Reports
type Discrepancy struct {
	Type        string
	Entity      string
	Description string
	QboAmount   string
	LocalAmount string
}

// DiscrepancyReport holds the results of a month-end reconciliation
type DiscrepancyReport struct {
	RealmID       string
	Discrepancies []Discrepancy
	TotalChecked  int
	IsReconciled  bool
}

// ReconciliationService handles the "Final Exam" logic of comparing QBO Reports
// (BalanceSheet, ProfitAndLoss) with the local Shadow DB.
type ReconciliationService struct {
	logger   *slog.Logger
	q        *database.Queries
	clientFn QBOClientFn
}

// NewReconciliationService creates a new ReconciliationService
func NewReconciliationService(logger *slog.Logger, q *database.Queries, clientFn QBOClientFn) *ReconciliationService {
	return &ReconciliationService{
		logger:   logger,
		q:        q,
		clientFn: clientFn,
	}
}

// ReconcileMonth runs the Month-End Reconciliation loop for a specific CPA client.
// startDate and endDate (YYYY-MM-DD) constrain the QBO report period; pass empty
// strings to use QBO's default (current month).
func (s *ReconciliationService) ReconcileMonth(ctx context.Context, realmID, startDate, endDate string) (*DiscrepancyReport, error) {
	client, err := s.clientFn(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get QBO client: %w", err)
	}

	report := &DiscrepancyReport{
		RealmID:       realmID,
		Discrepancies: []Discrepancy{},
		IsReconciled:  true,
	}

	bs, err := client.GetBalanceSheet(startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch BalanceSheet: %w", err)
	}
	s.compareBalanceSheet(ctx, realmID, bs, report)

	pl, err := client.GetProfitAndLoss(startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ProfitAndLoss: %w", err)
	}
	s.compareProfitAndLoss(ctx, realmID, pl, report)

	if len(report.Discrepancies) > 0 {
		report.IsReconciled = false
	}

	return report, nil
}

func (s *ReconciliationService) compareBalanceSheet(ctx context.Context, realmID string, bs *quickbooks.Report, report *DiscrepancyReport) {
	s.logger.Info("📊 Comparing Balance Sheet", "realm_id", realmID)
	localAccounts, err := s.q.GetAllAccountsForRealms(ctx, []string{realmID})
	if err != nil {
		s.logger.Error("Failed to fetch local accounts for reconciliation", "error", err)
		return
	}
	// Balance Sheet: check both presence and running-balance amounts.
	traverseRows(&bs.Rows, buildAccountMap(localAccounts), report, "BalanceSheet", true)
}

func (s *ReconciliationService) compareProfitAndLoss(ctx context.Context, realmID string, pl *quickbooks.Report, report *DiscrepancyReport) {
	s.logger.Info("📈 Comparing Profit And Loss", "realm_id", realmID)
	localAccounts, err := s.q.GetAllAccountsForRealms(ctx, []string{realmID})
	if err != nil {
		s.logger.Error("Failed to fetch local accounts for reconciliation", "error", err)
		return
	}
	// P&L reports reflect period income/expense activity, not running balances.
	// Shadow DB stores CurrentBalance (cumulative), which is not directly comparable
	// to a period-bounded P&L total. Limit checks to existence only to avoid
	// spurious balance-mismatch discrepancies.
	traverseRows(&pl.Rows, buildAccountMap(localAccounts), report, "ProfitAndLoss", false)
}

// buildAccountMap indexes a slice of shadow accounts by their QBO ID for O(1) lookup.
func buildAccountMap(accounts []database.ShadowErpAccount) map[string]database.ShadowErpAccount {
	m := make(map[string]database.ShadowErpAccount, len(accounts))
	for _, acc := range accounts {
		m[acc.QboID] = acc
	}
	return m
}

// traverseRows recursively walks a QBO report's row tree, flagging missing accounts
// and, when checkAmounts is true, balance mismatches against the shadow DB.
func traverseRows(rows *quickbooks.ReportRows, localMap map[string]database.ShadowErpAccount, report *DiscrepancyReport, source string, checkAmounts bool) {
	if rows == nil {
		return
	}
	for _, row := range rows.Row {
		if row.Type == "Data" && len(row.ColData) >= 2 {
			accID := row.ColData[0].ID
			if accID != "" {
				qboVal := row.ColData[len(row.ColData)-1].Value
				report.TotalChecked++

				localAcc, exists := localMap[accID]
				if !exists {
					report.Discrepancies = append(report.Discrepancies, Discrepancy{
						Type:        "MissingLocal",
						Entity:      "Account",
						Description: fmt.Sprintf("[%s] Account %s exists in QBO but missing locally", source, accID),
						QboAmount:   qboVal,
					})
				} else if checkAmounts {
					localValNum, _ := localAcc.CurrentBalance.Float64Value()
					qboValNorm := strings.ReplaceAll(qboVal, ",", "")
					qboFloat, parseErr := strconv.ParseFloat(qboValNorm, 64)
					if parseErr == nil &&
						math.Abs(qboFloat) >= reconcileEpsilon &&
						math.Abs(qboFloat-localValNum.Float64) >= reconcileEpsilon {
						report.Discrepancies = append(report.Discrepancies, Discrepancy{
							Type:        "BalanceMismatch",
							Entity:      "Account",
							Description: fmt.Sprintf("[%s] Balance mismatch for Account %s (%s)", source, accID, localAcc.Name),
							QboAmount:   qboVal,
							LocalAmount: fmt.Sprintf("%.2f", localValNum.Float64),
						})
					}
				}
			}
		}
		if row.Rows != nil {
			traverseRows(row.Rows, localMap, report, source, checkAmounts)
		}
	}
}

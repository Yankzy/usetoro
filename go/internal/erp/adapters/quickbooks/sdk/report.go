package quickbooks

import (
	"fmt"
)

// Report represents a generic QuickBooks Report response structure.
// Specifically tailored to parse BalanceSheet and ProfitAndLoss.
type Report struct {
	Header  ReportHeader  `json:"Header"`
	Rows    ReportRows    `json:"Rows"`
	Columns ReportColumns `json:"Columns"`
}

type ReportHeader struct {
	Time        string         `json:"Time"`
	ReportName  string         `json:"ReportName"`
	ReportBasis string         `json:"ReportBasis"`
	StartPeriod string         `json:"StartPeriod"`
	EndPeriod   string         `json:"EndPeriod"`
	Currency    string         `json:"Currency"`
	Customer    string         `json:"Customer,omitempty"`
	Option      []ReportOption `json:"Option,omitempty"`
}

type ReportOption struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

type ReportColumns struct {
	Column []ReportColumn `json:"Column"`
}

type ReportColumn struct {
	ColTitle string `json:"ColTitle"`
	ColType  string `json:"ColType"`
}

type ReportRows struct {
	Row []ReportRow `json:"Row"`
}

type ReportRow struct {
	Header  *ReportRowHeader  `json:"Header,omitempty"`
	Rows    *ReportRows       `json:"Rows,omitempty"`
	Summary *ReportRowSummary `json:"Summary,omitempty"`
	ColData []ReportColData   `json:"ColData,omitempty"`
	Type    string            `json:"type"` // "Section", "Data", etc.
}

type ReportRowHeader struct {
	ColData []ReportColData `json:"ColData"`
}

type ReportRowSummary struct {
	ColData []ReportColData `json:"ColData"`
}

type ReportColData struct {
	Value string `json:"value"`
	ID    string `json:"id,omitempty"`
}

// dateParams builds a query parameter map for report date filtering.
// Either or both values may be empty, in which case QBO uses its default period.
func dateParams(startDate, endDate string) map[string]string {
	if startDate == "" && endDate == "" {
		return nil
	}
	p := make(map[string]string, 2)
	if startDate != "" {
		p["start_date"] = startDate
	}
	if endDate != "" {
		p["end_date"] = endDate
	}
	return p
}

// GetBalanceSheet fetches the Balance Sheet report. Pass empty strings to use QBO's default period.
func (c *Client) GetBalanceSheet(startDate, endDate string) (*Report, error) {
	var resp Report
	if err := c.get("reports/BalanceSheet", &resp, dateParams(startDate, endDate)); err != nil {
		return nil, fmt.Errorf("failed to get BalanceSheet report: %w", err)
	}
	return &resp, nil
}

// GetProfitAndLoss fetches the Profit and Loss report. Pass empty strings to use QBO's default period.
func (c *Client) GetProfitAndLoss(startDate, endDate string) (*Report, error) {
	var resp Report
	if err := c.get("reports/ProfitAndLoss", &resp, dateParams(startDate, endDate)); err != nil {
		return nil, fmt.Errorf("failed to get ProfitAndLoss report: %w", err)
	}
	return &resp, nil
}

// GetGeneralLedger fetches the General Ledger report. Pass empty strings to use QBO's default period.
func (c *Client) GetGeneralLedger(startDate, endDate string) (*Report, error) {
	var resp Report
	if err := c.get("reports/GeneralLedger", &resp, dateParams(startDate, endDate)); err != nil {
		return nil, fmt.Errorf("failed to get GeneralLedger report: %w", err)
	}
	return &resp, nil
}

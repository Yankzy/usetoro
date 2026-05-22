package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xuri/excelize/v2"
	"strings"
)

// Exporter generates Excel and PDF exports for a completed cleanup session.
type Exporter struct {
	db *database.Queries
}

// NewExporter constructs an Exporter.
func NewExporter(db *database.Queries) *Exporter {
	return &Exporter{db: db}
}

// ExportExcel returns an XLSX workbook for the session.
// Sheet 1: "Cleaned Transactions" (APPROVED / POSTED)
// Sheet 2: "Flagged / Rejected"
// Sheet 3: "Duplicates"
func (e *Exporter) ExportExcel(ctx context.Context, sessionID string) ([]byte, error) {
	pgSessionID, err := parsePGUUID(sessionID)
	if err != nil {
		return nil, err
	}

	session, err := e.db.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	allRows, err := e.db.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: pgSessionID,
		Status:    pgtype.Text{}, // all statuses
	})
	if err != nil {
		return nil, fmt.Errorf("get session rows: %w", err)
	}

	f := excelize.NewFile()
	defer f.Close()

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"2F4F8F"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", WrapText: true},
	})
	if err != nil {
		headerStyle = 0
	}

	// ── Sheet 1: Approved / Posted ─────────────────────────────────────────
	const sheetCleaned = "Cleaned Transactions"
	f.SetSheetName("Sheet1", sheetCleaned)
	cleanedHeaders := []string{"Date", "Vendor (Normalized)", "Raw Description", "Account", "Amount", "AI Confidence", "Recurring", "Status", "QBO Transaction ID"}
	writeSheetHeader(f, sheetCleaned, cleanedHeaders, headerStyle)

	rowNum := 2
	for _, row := range allRows {
		if row.Status != "APPROVED" && row.Status != "POSTED" {
			continue
		}
		writeExcelRow(f, sheetCleaned, rowNum, row)
		rowNum++
	}
	autoFitColumns(f, sheetCleaned, len(cleanedHeaders))

	// ── Sheet 2: Flagged / Rejected ────────────────────────────────────────
	const sheetRejected = "Flagged / Rejected"
	f.NewSheet(sheetRejected)
	writeSheetHeader(f, sheetRejected, cleanedHeaders, headerStyle)
	rowNum = 2
	for _, row := range allRows {
		if row.Status != "REJECTED" && row.Status != "ENRICHED" && row.Status != "PENDING" {
			continue
		}
		writeExcelRow(f, sheetRejected, rowNum, row)
		rowNum++
	}
	autoFitColumns(f, sheetRejected, len(cleanedHeaders))

	// ── Sheet 3: Duplicates ────────────────────────────────────────────────
	const sheetDupes = "Duplicates"
	f.NewSheet(sheetDupes)
	dupHeaders := append(cleanedHeaders, "Duplicate Of")
	writeSheetHeader(f, sheetDupes, dupHeaders, headerStyle)
	rowNum = 2
	for _, row := range allRows {
		if !row.DuplicateOf.Valid {
			continue
		}
		writeExcelRow(f, sheetDupes, rowNum, row)
		if row.DuplicateOf.Valid {
			col := colLetter(len(dupHeaders))
			f.SetCellValue(sheetDupes, fmt.Sprintf("%s%d", col, rowNum),
				uuid.UUID(row.DuplicateOf.Bytes).String())
		}
		rowNum++
	}
	autoFitColumns(f, sheetDupes, len(dupHeaders))

	// ── Summary Tab ────────────────────────────────────────────────────────
	const sheetSummary = "Summary"
	f.NewSheet(sheetSummary)
	realmStr := "(no QBO connection)"
	if session.RealmID.Valid {
		realmStr = session.RealmID.String
	}
	summaryData := [][]string{
		{"Session ID", sessionID},
		{"File Name", session.FileName.String},
		{"Realm", realmStr},
		{"Total Rows", strconv.Itoa(int(session.RowCount))},
		{"Status", session.Status},
		{"Generated At", time.Now().UTC().Format("2006-01-02 15:04:05 UTC")},
	}
	for i, row := range summaryData {
		f.SetCellValue(sheetSummary, fmt.Sprintf("A%d", i+1), row[0])
		f.SetCellValue(sheetSummary, fmt.Sprintf("B%d", i+1), row[1])
	}

	buf := new(bytes.Buffer)
	if err := f.Write(buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

// ExportAuditPDF generates a PDF audit report for the session.
func (e *Exporter) ExportAuditPDF(ctx context.Context, sessionID string) ([]byte, error) {
	pgSessionID, err := parsePGUUID(sessionID)
	if err != nil {
		return nil, err
	}

	session, err := e.db.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	summary, err := e.db.GetSessionSummary(ctx, pgSessionID)
	if err != nil {
		return nil, fmt.Errorf("get session summary: %w", err)
	}

	overriddenRows, err := e.db.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: pgSessionID,
		Status:    pgtype.Text{String: "APPROVED", Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("get overridden rows: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.AddPage()

	// ── Header ─────────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 18)
	pdf.SetTextColor(47, 79, 143)
	pdf.CellFormat(0, 10, "Toro Clean-Up Mode Audit Report", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(100, 100, 100)
	pdf.CellFormat(0, 6, fmt.Sprintf("Generated: %s", time.Now().UTC().Format("January 2, 2006 15:04 UTC")), "", 1, "C", false, 0, "")
	pdf.Ln(6)

	// ── Session Metadata ───────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(0, 8, "Session Information", "", 1, "", false, 0, "")
	pdf.SetDrawColor(47, 79, 143)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(2)

	pdf.SetFont("Helvetica", "", 10)
	realmLabel := "(no QBO connection)"
	if session.RealmID.Valid {
		realmLabel = session.RealmID.String
	}
	metaRows := [][2]string{
		{"Session ID", sessionID},
		{"File", session.FileName.String},
		{"Realm", realmLabel},
		{"Status", session.Status},
		{"Created", session.CreatedAt.Time.UTC().Format("2006-01-02")},
	}
	for _, meta := range metaRows {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(50, 6, meta[0]+":", "", 0, "", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(0, 6, meta[1], "", 1, "", false, 0, "")
	}
	pdf.Ln(6)

	// ── Summary Statistics ─────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 8, "Automation Summary", "", 1, "", false, 0, "")
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(2)

	automationPct := 0.0
	if summary.TotalRows > 0 {
		automationPct = float64(summary.TotalRows-summary.OverriddenRows) / float64(summary.TotalRows) * 100
	}

	stats := [][2]string{
		{"Total Transactions", strconv.FormatInt(summary.TotalRows, 10)},
		{"AI Automated", fmt.Sprintf("%.1f%%", automationPct)},
		{"Manually Overridden", strconv.FormatInt(summary.OverriddenRows, 10)},
		{"Approved", strconv.FormatInt(summary.ApprovedRows, 10)},
		{"Rejected", strconv.FormatInt(summary.RejectedRows, 10)},
		{"Posted to QBO", strconv.FormatInt(summary.PostedRows, 10)},
		{"Duplicates Detected", strconv.FormatInt(summary.DuplicateRows, 10)},
		{"Recurring Charges", strconv.FormatInt(summary.RecurringRows, 10)},
	}
	pdf.SetFont("Helvetica", "", 10)
	for _, stat := range stats {
		pdf.CellFormat(80, 6, stat[0], "", 0, "", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, stat[1], "", 1, "", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
	}
	pdf.Ln(6)

	// ── Appendix A: Manually Overridden Rows ──────────────────────────────
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 8, "Appendix A — Manually Overridden Transactions", "", 1, "", false, 0, "")
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(2)

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(25, 6, "Date", "1", 0, "C", true, 0, "")
	pdf.CellFormat(55, 6, "Description", "1", 0, "C", true, 0, "")
	pdf.CellFormat(30, 6, "Amount", "1", 0, "C", true, 0, "")
	pdf.CellFormat(45, 6, "Overridden Account", "1", 0, "C", true, 0, "")
	pdf.CellFormat(0, 6, "AI Reasoning", "1", 1, "C", true, 0, "")

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetFillColor(255, 255, 255)
	for _, row := range overriddenRows {
		if !row.OverrideAccountID.Valid && !row.OverrideVendorID.Valid {
			continue
		}

		dateStr := ""
		if row.ParsedDate.Valid {
			dateStr = row.ParsedDate.Time.Format("2006-01-02")
		} else if row.RawDate.Valid {
			dateStr = row.RawDate.String
		}
		desc := ""
		if row.RawDescription.Valid {
			desc = truncate(row.RawDescription.String, 30)
		}
		amtStr := ""
		if row.RawAmount != "" {
			amtStr = row.RawAmount
		}
		accountName := ""
		if row.OverrideAccountName.Valid {
			accountName = row.OverrideAccountName.String
		} else if row.PredictedAccountName.Valid {
			accountName = row.PredictedAccountName.String
		}
		reasoning := ""
		if row.AiReasoning.Valid {
			reasoning = truncate(row.AiReasoning.String, 40)
		}

		pdf.CellFormat(25, 5, dateStr, "1", 0, "", false, 0, "")
		pdf.CellFormat(55, 5, desc, "1", 0, "", false, 0, "")
		pdf.CellFormat(30, 5, amtStr, "1", 0, "R", false, 0, "")
		pdf.CellFormat(45, 5, accountName, "1", 0, "", false, 0, "")
		pdf.CellFormat(0, 5, reasoning, "1", 1, "", false, 0, "")

		if pdf.GetY() > 270 {
			pdf.AddPage()
			pdf.SetFont("Helvetica", "", 8)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generate pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func writeSheetHeader(f *excelize.File, sheet string, headers []string, style int) {
	for i, h := range headers {
		cell := fmt.Sprintf("%s1", colLetter(i+1))
		f.SetCellValue(sheet, cell, h)
		if style != 0 {
			f.SetCellStyle(sheet, cell, cell, style)
		}
	}
}

func writeExcelRow(f *excelize.File, sheet string, rowNum int, row database.GetSessionRowsRow) {
	dateStr := ""
	if row.ParsedDate.Valid {
		dateStr = row.ParsedDate.Time.Format("2006-01-02")
	} else if row.RawDate.Valid {
		dateStr = row.RawDate.String
	}
	vendor := ""
	if row.PredictedVendorName != "" {
		vendor = row.PredictedVendorName
	}
	desc := ""
	if row.RawDescription.Valid {
		desc = row.RawDescription.String
	}
	account := ""
	if row.OverrideAccountName.Valid {
		account = row.OverrideAccountName.String
	} else if row.PredictedAccountName.Valid {
		account = row.PredictedAccountName.String
	}
	amount := 0.0
	if row.RawAmount != "" {
		cleaned := strings.ReplaceAll(row.RawAmount, "*", "")
		cleaned = strings.TrimSpace(cleaned)
		if f2, err := strconv.ParseFloat(cleaned, 64); err == nil {
			amount = f2
		}
	}
	confidence := ""
	if row.ConfidenceScore.Valid {
		if f2, err := row.ConfidenceScore.Float64Value(); err == nil {
			confidence = fmt.Sprintf("%.0f%%", f2.Float64*100)
		}
	}
	recurring := "No"
	if row.IsRecurring {
		recurring = "Yes"
	}
	qboID := ""
	if row.ErpTransactionID.Valid {
		qboID = row.ErpTransactionID.String
	}

	vals := []interface{}{dateStr, vendor, desc, account, amount, confidence, recurring, row.Status, qboID}
	for i, v := range vals {
		f.SetCellValue(sheet, fmt.Sprintf("%s%d", colLetter(i+1), rowNum), v)
	}
}

// colLetter converts a 1-based column index to Excel letter(s): 1→A, 26→Z, 27→AA.
func colLetter(n int) string {
	name := ""
	for n > 0 {
		n-- // shift to 0-based
		name = string(rune('A'+n%26)) + name
		n /= 26
	}
	return name
}

func autoFitColumns(f *excelize.File, sheet string, count int) {
	for i := 1; i <= count; i++ {
		col := colLetter(i)
		f.SetColWidth(sheet, col, col, 18)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func parsePGUUID(id string) (pgtype.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

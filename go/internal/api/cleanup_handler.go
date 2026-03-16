package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xuri/excelize/v2"
)

const (
	maxCleanupFileSize = 20 << 20 // 20 MiB
	maxCleanupRows     = 10_000
	cleanupEnrichSubj  = "cleanup.enrich.%s" // cleanup.enrich.{realmID}
)

// CleanupExporter defines the export operations required by the cleanup endpoints.
type CleanupExporter interface {
	ExportExcel(ctx context.Context, sessionID string) ([]byte, error)
	ExportAuditPDF(ctx context.Context, sessionID string) ([]byte, error)
}

// rawRow is an intermediate struct used during CSV/XLSX parsing.
type rawRow struct {
	Date        string
	Description string
	Amount      string
	Vendor      string
}

// HandleCleanupUpload ingests a CSV or XLSX file of messy transactions.
// POST /cleanup/upload (multipart/form-data: realm_id, file)
func (h *Handler) HandleCleanupUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Auth: extract user from JWT.
	userID, err := h.extractUserID(r)
	if err != nil {
		JSONError(w, h.Logger, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 2. Parse multipart — limit memory to 32 MiB for form fields.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "invalid multipart form")
		return
	}

	// realm_id is optional — if absent the session runs in Excel-only mode (no QBO posting).
	realmIDStr := strings.TrimSpace(r.FormValue("realm_id"))
	var realmID pgtype.Text
	if realmIDStr != "" {
		realmID = pgtype.Text{String: realmIDStr, Valid: true}
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()

	limitedFile := io.LimitReader(file, maxCleanupFileSize+1)

	// 3. Parse rows based on extension.
	name := strings.ToLower(header.Filename)
	var rows []rawRow
	switch {
	case strings.HasSuffix(name, ".csv"):
		rows, err = parseCSV(ctx, h.LLMClient, h.Logger, limitedFile)
	case strings.HasSuffix(name, ".xlsx"):
		data, readErr := io.ReadAll(limitedFile)
		if readErr != nil {
			JSONError(w, h.Logger, http.StatusBadRequest, "failed to read file")
			return
		}
		rows, err = parseXLSX(ctx, h.LLMClient, h.Logger, data)
	default:
		JSONError(w, h.Logger, http.StatusBadRequest, "unsupported file type — use .csv or .xlsx")
		return
	}
	if err != nil {
		h.Logger.Warn("cleanup file parse error", "file", header.Filename, "error", err)
		JSONError(w, h.Logger, http.StatusBadRequest, fmt.Sprintf("parse error: %v", err))
		return
	}
	if len(rows) == 0 {
		JSONError(w, h.Logger, http.StatusBadRequest, "file contains no data rows")
		return
	}
	if len(rows) > maxCleanupRows {
		JSONError(w, h.Logger, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("file exceeds maximum of %d rows", maxCleanupRows))
		return
	}

	// 4. Insert session + rows in a single transaction.
	tx, err := h.DBPool.Begin(ctx)
	if err != nil {
		h.Logger.Error("cleanup upload: begin tx", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "internal server error")
		return
	}
	defer tx.Rollback(ctx)

	qtx := h.CleanupDB.WithTx(tx)

	pgUserID := pgtype.UUID{Bytes: userID, Valid: true}
	session, err := qtx.CreateCleanupSession(ctx, database.CreateCleanupSessionParams{
		CreatedBy: pgUserID,
		FileName:  pgtype.Text{String: header.Filename, Valid: true},
		RowCount:  int32(len(rows)),
		RealmID:   realmID,
	})
	if err != nil {
		h.Logger.Error("cleanup upload: create session", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "internal server error")
		return
	}

	inserted := 0
	for _, row := range rows {
		amount, parseErr := strconv.ParseFloat(strings.ReplaceAll(row.Amount, ",", ""), 64)
		if parseErr != nil {
			h.Logger.Warn("cleanup upload: skipping row with invalid amount",
				"amount_str", row.Amount, "session_id", session.ID)
			continue
		}

		var rawDate pgtype.Date
		if row.Date != "" {
			if t := parseFlexibleDate(row.Date); !t.IsZero() {
				rawDate = pgtype.Date{Time: t, Valid: true}
			}
		}

		var amountNumeric pgtype.Numeric
		if scanErr := amountNumeric.Scan(fmt.Sprintf("%.2f", amount)); scanErr != nil {
			continue
		}

		// Determine source type from file extension
		sourceTypeStr := "csv"
		if strings.HasSuffix(name, ".xlsx") {
			sourceTypeStr = "excel" // mapping xlsx to 'excel' as it's more standard
		}

		if _, insertErr := qtx.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
			SessionID:      session.ID,
			RealmID:        realmID,
			SourceType:     sourceTypeStr,
			RawDescription: pgtype.Text{String: row.Description, Valid: row.Description != ""},
			RawAmount:      amountNumeric,
			RawDate:        rawDate,
		}); insertErr != nil {
			h.Logger.Error("cleanup upload: insert row", "error", insertErr)
			JSONError(w, h.Logger, http.StatusInternalServerError, "internal server error")
			return
		}
		inserted++
	}

	if err := tx.Commit(ctx); err != nil {
		h.Logger.Error("cleanup upload: commit tx", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "internal server error")
		return
	}

	// 5. CDC event (ledger.shadow_erp_cleanup_sessions.insert) will trigger the CleanupWorker automatically.
	sessionIDStr := uuid.UUID(session.ID.Bytes).String()

	h.Logger.Info("cleanup upload accepted",
		"session_id", sessionIDStr,
		"realm_id", realmID.String,
		"rows_inserted", inserted,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"session_id": sessionIDStr,
		"status":     "PENDING",
	})
}

// HandleCleanupExport streams a cleaned-up session as an Excel file.
// GET /cleanup/{session_id}/export
func (h *Handler) HandleCleanupExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.extractUserID(r); err != nil {
		JSONError(w, h.Logger, http.StatusUnauthorized, "unauthorized")
		return
	}

	sessionID, err := parsePathUUID(r, "session_id")
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "invalid session_id")
		return
	}

	if h.CleanupExporter == nil {
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "export not available")
		return
	}

	data, err := h.CleanupExporter.ExportExcel(ctx, sessionID)
	if err != nil {
		h.Logger.Error("cleanup export excel", "session_id", sessionID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "export failed")
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cleanup-%s.xlsx"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HandleCleanupAudit streams a PDF audit report for a completed session.
// GET /cleanup/{session_id}/audit
func (h *Handler) HandleCleanupAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.extractUserID(r); err != nil {
		JSONError(w, h.Logger, http.StatusUnauthorized, "unauthorized")
		return
	}

	sessionID, err := parsePathUUID(r, "session_id")
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "invalid session_id")
		return
	}

	if h.CleanupExporter == nil {
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "export not available")
		return
	}

	data, err := h.CleanupExporter.ExportAuditPDF(ctx, sessionID)
	if err != nil {
		h.Logger.Error("cleanup audit pdf", "session_id", sessionID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "audit export failed")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%s.pdf"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ─── parsing helpers ──────────────────────────────────────────────────────────

func parseCSV(ctx context.Context, llmClient *ai.LLMClient, logger *slog.Logger, r io.Reader) ([]rawRow, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	var allRows [][]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV read error: %w", err)
		}
		allRows = append(allRows, rec)
	}
	if len(allRows) == 0 {
		return nil, nil
	}

	sampleLimit := len(allRows)
	if sampleLimit > 5 {
		sampleLimit = 5
	}
	
	systemPrompt := "You are a highly precise data-mapping assistant for an accounting application. Your sole purpose is to output valid JSON matching the exact schema requested, without markdown formatting."
	userPrompt := buildMappingPrompt(headers, allRows[:sampleLimit])

	var mapping ColumnMapping
	if err := llmClient.GenerateJSON(ctx, systemPrompt, userPrompt, &mapping); err != nil {
		return nil, fmt.Errorf("LLM mapping failed: %w", err)
	}

	logger.Info("🤖 LLM Column Mapping Complete (CSV)",
		"mapping", mapping,
	)

	var rows []rawRow
	for _, rec := range allRows {
		rows = append(rows, mapRowUsingLLM(rec, mapping))
	}
	return rows, nil
}

func parseXLSX(ctx context.Context, llmClient *ai.LLMClient, logger *slog.Logger, data []byte) ([]rawRow, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx has no sheets")
	}
	allRows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("failed to read xlsx rows: %w", err)
	}
	if len(allRows) < 2 {
		return nil, nil
	}

	headers := allRows[0]
	dataRows := allRows[1:]

	sampleLimit := len(dataRows)
	if sampleLimit > 5 {
		sampleLimit = 5
	}

	systemPrompt := "You are a highly precise data-mapping assistant for an accounting application. Your sole purpose is to output valid JSON matching the exact schema requested, without markdown formatting."
	userPrompt := buildMappingPrompt(headers, dataRows[:sampleLimit])

	var mapping ColumnMapping
	if err := llmClient.GenerateJSON(ctx, systemPrompt, userPrompt, &mapping); err != nil {
		return nil, fmt.Errorf("LLM mapping failed: %w", err)
	}

	logger.Info("🤖 LLM Column Mapping Complete (XLSX)",
		"mapping", mapping,
	)

	var rows []rawRow
	for _, rec := range dataRows {
		rows = append(rows, mapRowUsingLLM(rec, mapping))
	}
	return rows, nil
}

func mapRowUsingLLM(rec []string, m ColumnMapping) rawRow {
	row := rawRow{}
	if m.DateColIdx != nil {
		row.Date = fieldAt(rec, *m.DateColIdx)
	}
	if m.DescriptionColIdx != nil {
		row.Description = fieldAt(rec, *m.DescriptionColIdx)
	}
	if m.VendorColIdx != nil {
		row.Vendor = fieldAt(rec, *m.VendorColIdx)
	}

	if m.IsSplitAmount {
		debitAmt := 0.0
		creditAmt := 0.0
		if m.DebitColIdx != nil {
			str := strings.ReplaceAll(fieldAt(rec, *m.DebitColIdx), ",", "")
			debitAmt, _ = strconv.ParseFloat(str, 64)
		}
		if m.CreditColIdx != nil {
			str := strings.ReplaceAll(fieldAt(rec, *m.CreditColIdx), ",", "")
			creditAmt, _ = strconv.ParseFloat(str, 64)
		}

		finalAmt := creditAmt
		if m.IsExpensePositive {
			// standard logic: if expense is positive, subtract it
			finalAmt -= debitAmt
		} else {
			// if it's false, then amounts are likely already properly signed, or add them
			finalAmt += debitAmt
		}
		row.Amount = fmt.Sprintf("%.2f", finalAmt)

	} else if m.AmountColIdx != nil {
		amtStr := strings.ReplaceAll(fieldAt(rec, *m.AmountColIdx), ",", "")
		amt, _ := strconv.ParseFloat(amtStr, 64)

		if m.IsExpensePositive {
			amt = -amt
		}
		row.Amount = fmt.Sprintf("%.2f", amt)
	}

	return row
}

func fieldAt(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[idx])
}

var dateLayouts = []string{
	"2006-01-02", "01/02/2006", "1/2/2006",
	"02/01/2006", "2/1/2006", "2006/01/02",
	"01-02-2006", "02-01-2006", "2006-1-2",
	"Jan 2, 2006", "January 2, 2006", "02-Jan-2006",
}

func parseFlexibleDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ─── auth + routing helpers ───────────────────────────────────────────────────

func (h *Handler) extractUserID(r *http.Request) ([16]byte, error) {
	var zero [16]byte
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return zero, fmt.Errorf("missing bearer token")
	}
	if h.Authenticator == nil {
		return zero, nil // dev mode
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := h.Authenticator.VerifyToken(r.Context(), token)
	if err != nil {
		return zero, err
	}
	return claims.UserID, nil
}

func parsePathUUID(r *http.Request, param string) (string, error) {
	raw := r.PathValue(param)
	if _, err := uuid.Parse(raw); err != nil {
		return "", fmt.Errorf("invalid UUID %q: %w", raw, err)
	}
	return raw, nil
}

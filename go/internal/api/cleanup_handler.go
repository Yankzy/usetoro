package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xuri/excelize/v2"
)

const (
	maxCleanupFileSize = 20 << 20 // 20 MiB
	maxCleanupRows     = 10_000
)

// CleanupExporter defines the export operations required by the cleanup endpoints.
type CleanupExporter interface {
	ExportExcel(ctx context.Context, sessionID string) ([]byte, error)
	ExportAuditPDF(ctx context.Context, sessionID string) ([]byte, error)
}

// HandleCleanupUpload ingests a CSV or XLSX file of messy transactions.
// POST /cleanup/upload (multipart/form-data: realm_id, file)
func (h *Handler) HandleCleanupUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Auth: extract user claims from JWT.
	claims, err := h.extractUserClaims(r)
	if err != nil || claims == nil {
		JSONError(w, h.Logger, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 2. Fetch the realm_id for this user's entity
	entityUUID := pgtype.UUID{Bytes: claims.EntityID, Valid: true}

	// We allow the upload to proceed even if no realm_id is linked yet (Excel-only offline mode).
	var realmID pgtype.Text
	var scannedRealmID string
	err = h.DBPool.QueryRow(ctx, `SELECT realm_id FROM toro_core.erp_connections WHERE entity_id = $1 LIMIT 1`, entityUUID).Scan(&scannedRealmID)
	if err == nil && scannedRealmID != "" {
		realmID = pgtype.Text{String: scannedRealmID, Valid: true}
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
	var rows [][]string
	switch {
	case strings.HasSuffix(name, ".csv"):
		rows, err = parseCSV(r.Context(), limitedFile)
	case strings.HasSuffix(name, ".xlsx"):
		data, readErr := io.ReadAll(limitedFile)
		if readErr != nil {
			JSONError(w, h.Logger, http.StatusBadRequest, "failed to read file")
			return
		}
		rows, err = parseXLSX(r.Context(), data)
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

	pgUserID := pgtype.UUID{Bytes: claims.UserID, Valid: true}
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

	// 4. Broadcast the task to the TAP Agent Network
	payload := map[string]interface{}{
		"session_id": session.ID,
		"realm_id":   realmID.String,
		"rows":       rows,
	}
	payloadBytes, _ := json.Marshal(payload)

	taskDef := core.TaskDefinition{
		ID:         uuid.New().String(),
		Domain:     "accounting.cleanup",
		Complexity: 0,
		Reward:     1, // 1 micro-TORO
		Payload:    payloadBytes,
	}

	// The gate API acts as the broadcaster. We need a DID for it.
	// For now, we'll generate an ephemeral one, but ideally the API has a static identity.
	kp, _ := identity.GenerateKeyPair()
	gateDID := identity.CreateDID(kp.Public)

	cfpEnv, err := core.NewEnvelope(
		uuid.New().String(),
		gateDID,
		"",                  // Broadcast
		uuid.New().String(), // New Conversation CID
		core.CFP,
		taskDef,
	)
	if err != nil {
		h.Logger.Error("failed to create CFP envelope", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "failed to dispatch task")
		return
	}
	cfpEnv.Signature = kp.Sign(cfpEnv.Body)

	cfpBytes, _ := json.Marshal(cfpEnv)

	// Broadcast on NATS JetStream topic (0 is complexity)
	topic := "tasks.accounting.cleanup.0"
	if err := h.CleanupNATS.Publish(topic, cfpBytes); err != nil {
		h.Logger.Error("failed to publish CFP", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "failed to dispatch task")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.Logger.Error("cleanup upload: commit tx", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "internal server error")
		return
	}

	sessionIDStr := uuid.UUID(session.ID.Bytes).String()

	h.Logger.Info("cleanup upload delegated to TAP Agent",
		"session_id", sessionIDStr,
		"realm_id", realmID.String,
		"rows_extracted", len(rows),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted because processing is async
	json.NewEncoder(w).Encode(map[string]string{
		"session_id": sessionIDStr,
		"status":     "PROCESSING",
	})
}

// HandleCleanupExport streams a cleaned-up session as an Excel file.
// GET /cleanup/{session_id}/export
func (h *Handler) HandleCleanupExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if claims, err := h.extractUserClaims(r); claims == nil || err != nil {
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
	if claims, err := h.extractUserClaims(r); claims == nil || err != nil {
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

func parseCSV(ctx context.Context, r io.Reader) ([][]string, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	var allRows [][]string
	allRows = append(allRows, headers) // include headers for AI

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

	return allRows, nil
}

func parseXLSX(ctx context.Context, data []byte) ([][]string, error) {
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

	return allRows, nil
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

func (h *Handler) extractUserClaims(r *http.Request) (*auth.UserClaims, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("missing bearer token")
	}
	if h.Authenticator == nil {
		return nil, nil // dev mode
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := h.Authenticator.VerifyToken(r.Context(), token)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func parsePathUUID(r *http.Request, param string) (string, error) {
	raw := r.PathValue(param)
	if _, err := uuid.Parse(raw); err != nil {
		return "", fmt.Errorf("invalid UUID %q: %w", raw, err)
	}
	return raw, nil
}

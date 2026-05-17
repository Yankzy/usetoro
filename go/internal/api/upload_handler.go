package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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
	maxFileSize = 20 << 20 // 20 MiB
	maxRows     = 10_000
)

// Exporter defines the export operations required by the export endpoints.
type Exporter interface {
	ExportExcel(ctx context.Context, sessionID string) ([]byte, error)
	ExportAuditPDF(ctx context.Context, sessionID string) ([]byte, error)
}

// HandleFileIngestion ingests ANY supported file type and broadcasts it for triage.
// POST /files/upload/{domain}/{taskType} (multipart/form-data: file)
func (h *Handler) HandleFileIngestion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.Logger.Info("📡 [DEBUG] HandleFileIngestion received request")

	// 1. Extract routing parameters from the URL path
	domain := r.PathValue("domain")
	taskType := r.PathValue("taskType")

	if domain == "" || taskType == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "domain and taskType path parameters are required")
		return
	}

	// 2. Auth: extract user claims from JWT.
	claims, err := h.extractUserClaims(r)
	if err != nil || claims == nil {
		JSONError(w, h.Logger, http.StatusUnauthorized, "unauthorized")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()

	limitedFile := io.LimitReader(file, maxFileSize+1)
	data, readErr := io.ReadAll(limitedFile)
	if readErr != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "failed to read file")
		return
	}
	if len(data) > maxFileSize {
		JSONError(w, h.Logger, http.StatusBadRequest, "file too large (max 20MiB)")
		return
	}

	ctype := ""
	if header != nil && header.Header != nil {
		ctype = header.Header.Get("Content-Type")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	h.Logger.Info("📡 [DEBUG] HandleFileIngestion received upload",
		"filename", header.Filename,
		"ext", ext,
		"content_type", ctype,
		"bytes", len(data),
	)

	// 3. Parse rows based on extension (with a conservative content-type fallback).
	name := strings.ToLower(header.Filename)
	var rows [][]string
	switch {
	case strings.HasSuffix(name, ".csv"):
		rows, err = parseCSV(ctx, bytes.NewReader(data))
	case strings.HasSuffix(name, ".xlsx"):
		rows, err = parseXLSX(ctx, data)
	default:
		// If a client supplies the wrong filename extension, allow a safe fallback:
		// - XLSX is a ZIP container ("PK..")
		// - CSV should be explicitly labeled as csv by the client (Content-Type)
		if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			rows, err = parseXLSX(ctx, data)
		} else if strings.Contains(strings.ToLower(ctype), "csv") {
			rows, err = parseCSV(ctx, bytes.NewReader(data))
			// Heuristic: treat one-column "csv" as likely not a real CSV.
			// This prevents accidentally ingesting random text/markdown when the client mislabels files.
			if err == nil && !looksLikeDelimitedTable(rows) {
				err = fmt.Errorf("content does not look like a delimited table")
			}
		} else {
			JSONError(w, h.Logger, http.StatusBadRequest, fmt.Sprintf("unsupported file type — use .csv or .xlsx (received %q, content-type %q)", header.Filename, ctype))
			return
		}
	}

	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, fmt.Sprintf("parse error: %v", err))
		return
	}

	// 4. Check for session requirements (e.g. accounting.cleanup)
	var uploadID string
	var realmIDStr string

	outflowIs := r.FormValue("outflow_is")
	if domain == "accounting" && outflowIs == "" {
		outflowIs = "NEGATIVE"
	}

	if domain == "accounting" {
		// Extract optional bank_account_name from the request
		bankAccountName := r.FormValue("bank_account_name")

		var bankAccountID pgtype.UUID

		// Fetch the realm_id for this user's entity
		entityUUID := pgtype.UUID{Bytes: claims.EntityID, Valid: true}
		var realmID pgtype.Text
		var scannedRealmID string
		err = h.DBPool.QueryRow(ctx, `SELECT realm_id FROM toro_core.erp_connections WHERE entity_id = $1 LIMIT 1`, entityUUID).Scan(&scannedRealmID)
		if err == nil && scannedRealmID != "" {
			realmID = pgtype.Text{String: scannedRealmID, Valid: true}
		}
		realmIDStr = realmID.String

		// If a bank account name was provided, resolve it to an ID for this realm.
		if bankAccountName != "" && realmIDStr != "" {
			acc, err := h.DB.GetAccountByName(ctx, database.GetAccountByNameParams{
				RealmID: realmIDStr,
				Name:    bankAccountName,
			})
			if err == nil {
				bankAccountID = acc.ID
			} else {
				h.Logger.Warn("file ingestion: bank account not found", "name", bankAccountName, "realm", realmIDStr, "error", err)
			}
		}

		// Create a persistent session in the database
		pgUserID := pgtype.UUID{Bytes: claims.UserID, Valid: true}
		session, err := h.DB.CreateCleanupSession(ctx, database.CreateCleanupSessionParams{
			CreatedBy:     pgUserID,
			FileName:      pgtype.Text{String: header.Filename, Valid: true},
			RowCount:      int32(len(rows)),
			RealmID:       realmID,
			BankAccountID: bankAccountID,
			OutflowIs:     outflowIs,
		})
		if err != nil {
			h.Logger.Error("file ingestion: create cleanup session", "error", err)
			JSONError(w, h.Logger, http.StatusInternalServerError, "failed to initialize cleanup session")
			return
		}
		uploadID = uuid.UUID(session.ID.Bytes).String()
	} else {
		// Use ephemeral upload ID for other domains
		uploadID = uuid.New().String()
	}

	// 5. Build a dynamic event payload
	payload := map[string]interface{}{
		"upload_id":  uploadID,
		"session_id": uploadID, // Backward compatibility for agents expecting session_id
		"filename":   header.Filename,
		"entity_id":  claims.EntityID, // Pass identity context downstream
		"user_id":    claims.UserID,
		"domain":     domain, // Explicitly pass routing context to the agent
		"task_type":  taskType,
		"realm_id":   realmIDStr,
		"outflow_is": outflowIs,
		"rows":       rows,
	}
	payloadBytes, _ := json.Marshal(payload)

	taskDef := core.TaskDefinition{
		ID:         uploadID,
		Domain:     fmt.Sprintf("%s.%s", domain, taskType), // Dynamic domain definition
		Complexity: 0,
		Payload:    payloadBytes,
	}

	kp, _ := identity.KeyPairFromSeed("gateway")
	gateDID := identity.CreateDID(kp.Public)

	// Use REQUEST performative because the gateway is just announcing a fact, not asking for bids yet.
	informEnv, err := core.NewEnvelope(
		uuid.New().String(),
		gateDID,
		"",
		uuid.New().String(),
		core.REQUEST,
		taskDef,
	)
	if err != nil {
		JSONError(w, h.Logger, http.StatusInternalServerError, "failed to package event")
		return
	}
	informEnv.Signature = kp.Sign(informEnv.Body)
	informBytes, _ := json.Marshal(informEnv)

	// 6. Publish to NATS JetStream topic: e.g., events.accounting.1.cleanup
	topic := core.BuildEventSubject(domain, core.ComplexityEntry, taskType)
	if err := h.NATS.Publish(topic, informBytes); err != nil {
		h.Logger.Error("JetStream publish failed", "topic", topic, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "failed to emit event")
		return
	}

	h.Logger.Info("HandleFileIngestion published to NATS",
		"upload_id", uploadID,
		"domain", domain,
		"task_type", taskType,
		"topic", topic,
		"rows_extracted", len(rows),
	)

	// 7. Success Response
	// Fetch the workflow blueprint from the Orchestrator (via NATS) so the frontend can render immediately.
	var blueprint map[string]interface{}
	if h.NATS != nil {
		msg, err := h.NATS.Conn().Request("workflow.query.blueprint", []byte(topic), 1*time.Second)
		if err == nil {
			json.Unmarshal(msg.Data, &blueprint)
		} else {
			h.Logger.Warn("Failed to fetch blueprint for visualizer", "topic", topic, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"upload_id": uploadID,
		"status":    "processing",
		"topic":     topic,
		"blueprint": blueprint,
	})
}

// HandleExport streams a cleaned-up session as an Excel file.
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
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

	if h.Exporter == nil {
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "export not available")
		return
	}

	data, err := h.Exporter.ExportExcel(ctx, sessionID)
	if err != nil {
		h.Logger.Error("export excel", "session_id", sessionID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "export failed")
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="export-%s.xlsx"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HandleAudit streams a PDF audit report for a completed session.
func (h *Handler) HandleAudit(w http.ResponseWriter, r *http.Request) {
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

	if h.Exporter == nil {
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "export not available")
		return
	}

	data, err := h.Exporter.ExportAuditPDF(ctx, sessionID)
	if err != nil {
		h.Logger.Error("audit pdf", "session_id", sessionID, "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "audit export failed")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%s.pdf"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ─── parsing helpers ──────────────────────────────────────────────────────────

func looksLikeDelimitedTable(rows [][]string) bool {
	// A "real" table almost always has at least one row with 2+ columns.
	for _, r := range rows {
		if len(r) >= 2 {
			return true
		}
	}
	return false
}

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

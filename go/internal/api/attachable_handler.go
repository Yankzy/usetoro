package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// HandleUploadAttachable handles standard multipart form uploads for attaching files to QBO entities.
func (h *Handler) HandleUploadAttachable(w http.ResponseWriter, r *http.Request) {
	realmID := r.PathValue("realmId")
	entityType := r.PathValue("entityType")
	entityID := r.PathValue("entityID")

	if realmID == "" || entityType == "" || entityID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing path parameters (realmId, entityType, or entityID)")
		return
	}

	// 1. Limit Request Size (e.g. 20MB) to prevent large spam payloads
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)

	// 2. Parse Multipart Form
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		h.Logger.Warn("Failed to parse multipart form", "error", err)
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid multipart form data or file too large")
		return
	}

	// 3. Extract the file
	file, fileHeader, err := r.FormFile("file") // Client must send the file using the "file" form field
	if err != nil {
		h.Logger.Warn("Missing 'file' field in upload", "error", err)
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing 'file' form field")
		return
	}
	defer file.Close()

	// 4. Read File Bytes
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		h.Logger.Error("Failed to read file bytes", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to read file")
		return
	}

	filename := fileHeader.Filename
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		// Fallback detection logic if completely missing
		contentType = http.DetectContentType(fileBytes)
	}

	// 5. Save to local simulated Blob Storage and Insert Pending record
	uploadDir := "/tmp/toro-uploads"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		h.Logger.Error("Failed to create upload directory", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Storage error")
		return
	}

	tempID := "pending-" + uuid.NewString()
	filePath := filepath.Join(uploadDir, tempID+"-"+filename)

	if err := os.WriteFile(filePath, fileBytes, 0644); err != nil {
		h.Logger.Error("Failed to write file to local blob storage", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Failed to save file")
		return
	}

	refsJson, _ := json.Marshal(map[string]string{
		"entityType": entityType,
		"entityID":   entityID,
		"filePath":   filePath, // Storing internal file path here for worker
	})

	queries := database.New(h.DBPool)
	if err := queries.UpsertAttachable(r.Context(), database.UpsertAttachableParams{
		RealmID:        realmID,
		ErpID:          tempID,
		FileName:       pgtype.Text{String: filename, Valid: true},
		ContentType:    pgtype.Text{String: contentType, Valid: true},
		Size:           pgtype.Numeric{}, // ignored for now
		Note:           pgtype.Text{String: "Pending Upload", Valid: true},
		AttachableRefs: refsJson,
		SyncToken:      pgtype.Text{String: "0", Valid: true},
	}); err != nil {
		h.Logger.Error("Failed to insert pending attachable", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Database error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "File linked to transaction successfully",
	}); err != nil {
		h.Logger.Error("Failed to encode success response", "error", err)
	}
}

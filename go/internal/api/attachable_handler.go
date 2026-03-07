package api

import (
	"encoding/json"
	"io"
	"net/http"
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

	// 5. Invoke Business Logic (AttachableService)
	if err := h.AttachableService.UploadAttachable(r.Context(), realmID, entityType, entityID, fileBytes, filename, contentType); err != nil {
		h.Logger.Error("Failed to upload attachable to ERP", "realm", realmID, "entity_type", entityType, "entity_id", entityID, "error", err)
		// Usually if QBO fails, we treat it as an external dependency error
		JSONError(w, h.Logger, http.StatusBadGateway, "Failed to complete upload with accounting provider")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "File linked to transaction successfully",
	}); err != nil {
		h.Logger.Error("Failed to encode success response", "error", err)
	}
}

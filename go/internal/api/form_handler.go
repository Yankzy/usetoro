package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
)

// HandleCaptureForm handles lead form submissions from any website.
func (h *Handler) HandleCaptureForm(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path values
	website := r.PathValue("website")
	if website == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing website parameter")
		return
	}

	// Read the request body as raw JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Validate JSON
	if !json.Valid(body) {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	h.Logger.Info("Received lead form submission", "website", website, "form_data", string(body))

	// Persist to database
	lead, err := h.DB.CreateLeadForm(r.Context(), database.CreateLeadFormParams{
		Website:  website,
		FormData: body,
	})
	if err != nil {
		h.Logger.Error("Failed to store lead form", "error", err, "website", website)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Publish outbound chat event over NATS to OmniChatWorker
	if h.Pub != nil {
		h.publishLeadFormEmailEvent(r.Context(), website, lead, body)
	}

	// Success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"id":     lead.ID,
	}); err != nil {
		h.Logger.Error("Failed to encode success response", "error", err)
	}
}

func (h *Handler) publishLeadFormEmailEvent(ctx context.Context, website string, lead database.MarketingLeadForm, body []byte) {
	leadIDStr := ""
	if lead.ID.Valid {
		leadIDStr = uuid.UUID(lead.ID.Bytes).String()
	}

	var formMap map[string]interface{}
	_ = json.Unmarshal(body, &formMap)

	var bodyBuf bytes.Buffer
	bodyBuf.WriteString("New Lead Form Submission Received\n\n")
	bodyBuf.WriteString(fmt.Sprintf("Website: %s\n", website))
	if leadIDStr != "" {
		bodyBuf.WriteString(fmt.Sprintf("Lead ID: %s\n", leadIDStr))
	}
	bodyBuf.WriteString(fmt.Sprintf("Submitted At: %s\n\n", time.Now().Format(time.RFC1123)))
	bodyBuf.WriteString("Form Fields:\n")

	if len(formMap) > 0 {
		for k, v := range formMap {
			bodyBuf.WriteString(fmt.Sprintf("  - %s: %v\n", k, v))
		}
	} else {
		bodyBuf.WriteString(string(body))
	}

	responsePayload := map[string]interface{}{
		"source":      "email",
		"from_handle": "forms@usetoro.io",
		"to_handle":   "yankz@usetoro.io",
		"subject":     fmt.Sprintf("New Lead : %s", website),
		"body_text":   bodyBuf.String(),
	}

	proofData, err := json.Marshal(responsePayload)
	if err != nil {
		h.Logger.Error("Failed to marshal lead form response payload", "error", err)
		return
	}

	proof := core.Proof{
		Type: core.ProofAPI,
		Data: proofData,
	}
	proofBytes, err := json.Marshal(proof)
	if err != nil {
		h.Logger.Error("Failed to marshal lead form proof", "error", err)
		return
	}

	envelope := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		Performative: core.INFORM,
		Body:         proofBytes,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		h.Logger.Error("Failed to marshal lead form envelope", "error", err)
		return
	}

	if err := h.Pub.PublishRaw(ctx, "proof.outgoing.chat", envelopeBytes); err != nil {
		h.Logger.Error("Failed to publish lead form email notification to NATS", "error", err, "website", website)
	} else {
		h.Logger.Info("Published lead form notification to proof.outgoing.chat", "website", website)
	}
}

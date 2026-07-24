package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// HandlePcmOcrWebhook receives OCR parsed payloads and forwards them to ASE via NATS.
func (h *Handler) HandlePcmOcrWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.Logger.Error("pcm ocr webhook: failed to read body", "error", err)
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse incoming JSON which should map to pcm.PcmPayload
	var ocrPayload map[string]interface{}
	if err := json.Unmarshal(body, &ocrPayload); err != nil {
		h.Logger.Error("pcm ocr webhook: failed to unmarshal json", "error", err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Create Envelope
	env := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:ingress",
		Performative: core.REQUEST,
	}

	// Embed dag config inside the envelope so ASE Bridge knows where to route
	taskConfig := map[string]interface{}{
		"dag_name":    "pcm_bank_reconciliation",
		"domain_tool": "pcm_bank_reconciliation",
	}
	bodyData := map[string]interface{}{
		"config":  taskConfig,
		"payload": ocrPayload,
	}
	
	env.Body, _ = json.Marshal(bodyData)

	// Publish to ASE Bridge Worker
	envBytes, err := json.Marshal(env)
	if err != nil {
		h.Logger.Error("pcm ocr webhook: failed to marshal envelope", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// worker.inbox.ase_bridge is the subject ASEBridgeWorker listens to
	if err := h.Pub.PublishRaw(ctx, "worker.inbox.ase_bridge", envBytes); err != nil {
		h.Logger.Error("pcm ocr webhook: failed to publish to NATS", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("pcm ocr webhook: successfully published to ase_bridge")

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}

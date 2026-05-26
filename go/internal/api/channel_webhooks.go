package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// HandleTwilioSMSWebhook receives Twilio SMS webhook payloads and publishes
// them to the Twilio SMS inbound worker for processing.
func (h *Handler) HandleTwilioSMSWebhook(w http.ResponseWriter, r *http.Request) {
	subject, err := core.BuildWorkerInboxFromActivity("workers.twilio_sms_inbound")
	if err != nil {
		h.Logger.Error("twilio(sms) webhook: failed to derive subject", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Twilio sends application/x-www-form-urlencoded
	_ = r.ParseForm()
	payload := map[string]interface{}{
		"From":        r.PostFormValue("From"),
		"To":          r.PostFormValue("To"),
		"Body":        r.PostFormValue("Body"),
		"MessageSid":  r.PostFormValue("MessageSid"),
		"FromCity":    r.PostFormValue("FromCity"),
		"FromCountry": r.PostFormValue("FromCountry"),
	}
	payloadBytes, _ := json.Marshal(payload)

	if err := h.NATS.Publish(subject, payloadBytes); err != nil {
		h.Logger.Error("twilio(sms) webhook: failed to publish", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("twilio(sms) webhook: published", "from", payload["From"])
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleTwilioWhatsAppWebhook receives Twilio WhatsApp webhook payloads.
func (h *Handler) HandleTwilioWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	subject, err := core.BuildWorkerInboxFromActivity("workers.twilio_whatsapp_inbound")
	if err != nil {
		h.Logger.Error("twilio(wa) webhook: failed to derive subject", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Twilio WhatsApp also sends application/x-www-form-urlencoded
	_ = r.ParseForm()
	payload := map[string]interface{}{
		"From":        r.PostFormValue("From"),
		"To":          r.PostFormValue("To"),
		"Body":        r.PostFormValue("Body"),
		"MessageSid":  r.PostFormValue("MessageSid"),
		"NumMedia":    r.PostFormValue("NumMedia"),
		"FromCity":    r.PostFormValue("FromCity"),
		"FromCountry": r.PostFormValue("FromCountry"),
	}
	payloadBytes, _ := json.Marshal(payload)

	if err := h.NATS.Publish(subject, payloadBytes); err != nil {
		h.Logger.Error("twilio(wa) webhook: failed to publish", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("twilio(wa) webhook: published", "from", payload["From"])
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleTelegramWebhook receives Telegram Bot API webhook payloads (JSON)
// and publishes them to the Telegram inbound worker.
func (h *Handler) HandleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	// Telegram sends JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.Logger.Error("telegram webhook: failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	subject, err := core.BuildWorkerInboxFromActivity("workers.telegram_inbound")
	if err != nil {
		h.Logger.Error("telegram webhook: failed to derive subject", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.NATS.Publish(subject, body); err != nil {
		h.Logger.Error("telegram webhook: failed to publish", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("telegram webhook: published", "size", len(body))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

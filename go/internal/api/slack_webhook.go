package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// slackEvent represents an incoming Slack Events API payload.
type slackEvent struct {
	Token     string          `json:"token"`
	Challenge string          `json:"challenge"`
	Type      string          `json:"type"`
	TeamID    string          `json:"team_id"`
	Event     json.RawMessage `json:"event"`
	EventID   string          `json:"event_id"`
	EventTime int64           `json:"event_time"`
}

// HandleSlackWebhook receives Slack Events API webhook payloads and publishes
// them to the Slack inbound worker for processing. It handles URL verification
// challenges synchronously as required by Slack (3-second deadline).
func (h *Handler) HandleSlackWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.Logger.Error("slack webhook: failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	var event slackEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.Logger.Error("slack webhook: failed to unmarshal event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// URL verification challenge — must respond synchronously per Slack spec.
	// Slack sends this when registering the Events API endpoint and expects
	// the challenge string back as plain text within 3 seconds.
	if event.Type == "url_verification" && event.Challenge != "" {
		h.Logger.Info("slack webhook: handling url_verification challenge")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(event.Challenge))
		return
	}

	subject, err := core.BuildWorkerInboxFromActivity("workers.slack_inbound")
	if err != nil {
		h.Logger.Error("slack webhook: failed to derive subject", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.NATS.Publish(subject, body); err != nil {
		h.Logger.Error("slack webhook: failed to publish", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("slack webhook: published", "type", event.Type, "event_id", event.EventID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

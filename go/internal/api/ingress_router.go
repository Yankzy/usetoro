package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

// HandleIngressWorker reads the HTTP body and publishes it directly to NATS.
// Takes an "activity-type" query parameter (without the "workers." prefix).
// The router prepends "workers." and derives the worker inbox subject.
func (h *Handler) HandleIngressWorker(w http.ResponseWriter, r *http.Request) {
	activityType := r.URL.Query().Get("activity-type")

	if activityType == "" {
		http.Error(w, "missing activity-type", http.StatusBadRequest)
		return
	}

	subject, err := core.BuildWorkerInboxFromActivity("workers." + activityType)
	if err != nil {
		h.Logger.Error("ingress: failed to derive worker inbox", "activity_type", activityType, "error", err)
		http.Error(w, "invalid activity-type", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	fmt.Println("ingress: received request", "activity_type", activityType)
	// h.Logger.Info("INGRESS BODY: " + string(body))
	if err != nil {
		h.Logger.Error("ingress: failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Slack URL verification challenge must be handled synchronously
	// (Slack imposes a 3-second deadline), so we intercept it here
	// before the NATS publish path.
	if strings.Contains(strings.ToLower(activityType), "slack") {
		var event slackEvent
		if err := json.Unmarshal(body, &event); err == nil && event.Type == "url_verification" && event.Challenge != "" {
			h.Logger.Info("ingress: handling slack url_verification challenge")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(event.Challenge))
			return
		}
	}

	if len(body) == 0 {
		if r.Method == http.MethodGet {
			// For GET requests (e.g. OAuth callbacks), package the query params as a JSON body
			queryMap := make(map[string]string)
			for k, v := range r.URL.Query() {
				if len(v) > 0 {
					queryMap[k] = v[0]
				}
			}
			jsonBody, err := json.Marshal(queryMap)
			if err == nil && len(jsonBody) > 2 { // > 2 ensures it's not just "{}"
				body = jsonBody
			}
		}
	}

	if len(body) == 0 || string(body) == "{}" {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	msg := nats.NewMsg(subject)
	msg.Data = body
	msg.Header.Set(nats.MsgIdHdr, fmt.Sprintf("ingress-%d", time.Now().UnixNano()))
	msg.Header.Set("Nats-TTL", "1m")

	publishCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if _, err := h.NATS.PublishMsg(msg, nats.Context(publishCtx)); err != nil {
		h.Logger.Error("ingress: failed to publish to NATS", "error", err, "subject", subject)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("ingress: published", "subject", subject, "size", len(body))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"published"}`))
}

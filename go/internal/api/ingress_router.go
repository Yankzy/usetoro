package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	if err != nil {
		h.Logger.Error("ingress: failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
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

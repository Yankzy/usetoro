package api

import (
	"io"
	"net/http"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// HandleIngressWorker is a super simple ingress handler that reads the HTTP body
// and publishes it directly to NATS JetStream. It takes an optional "subject" query parameter.
func (h *Handler) HandleIngressWorker(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	domain := r.URL.Query().Get("domain")

	// 1. Specialized routing logic via domain/task
	switch domain {
	case "postmark":
		derived, err := core.BuildWorkerInboxFromActivity("workers.email.postmark_inbound")
		if err != nil {
			h.Logger.Error("ingress: failed to derive postmark subject", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		subject = derived

	case "accounting":
		// Restore accounting domain routing (e.g. events.accounting.1.cleanup)
		taskType := r.URL.Query().Get("task")
		if taskType == "" {
			taskType = "cleanup"
		}
		subject = core.BuildEventSubject("accounting", core.ComplexityEntry, taskType)
	}

	if subject == "" {
		h.Logger.Error("ingress: no subject provided")
		http.Error(w, "no subject provided", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
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

	err = h.NATS.Publish(subject, body)
	if err != nil {
		h.Logger.Error("ingress: failed to publish to NATS", "error", err, "subject", subject)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("ingress: successfully published to NATS", "subject", subject, "size", len(body))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"published"}`))
}

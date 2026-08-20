package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
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

	// Meta / WhatsApp webhook verification challenge (hub.mode=subscribe)
	hubMode := r.URL.Query().Get("hub.mode")
	if hubMode == "" {
		hubMode = r.URL.Query().Get("hub_mode")
	}
	hubChallenge := r.URL.Query().Get("hub.challenge")
	if hubChallenge == "" {
		hubChallenge = r.URL.Query().Get("hub_challenge")
	}
	if hubMode == "subscribe" && hubChallenge != "" {
		h.Logger.Info("ingress: handling meta verification challenge", "activity_type", activityType, "challenge", hubChallenge)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(hubChallenge))
		return
	}

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
	// log msg
	// h.Logger.Info("ingress: publishing", "NATS_MSG", msg)
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

// HandleEmailClick handles tracked links in marketing emails.
// Endpoint: GET /c/{hash}

// HandleMailpoolWebhook receives deliverability updates from Mailpool
// Endpoint: POST /webhooks/mailpool/deliverability
func (h *Handler) HandleMailpoolWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.Logger.Error("mailpool webhook: failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// Signature verification
	receivedSig := r.Header.Get("X-Signature")
	if receivedSig == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		return
	}

	secret := config.GetGlobal().MailpoolWebhookSecret
	if secret == "" {
		h.Logger.Error("mailpool webhook: secret not configured")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(receivedSig), []byte(expectedSig)) != 1 {
		h.Logger.Warn("mailpool webhook: invalid signature", "expected", expectedSig, "received", receivedSig)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Fire and forget
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := h.Pub.PublishRaw(ctx, "webhooks.mailpool.received", body); err != nil {
			h.Logger.Error("mailpool webhook: failed to publish to NATS", "error", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"received"}`))
}

func (h *Handler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	c := r.URL.Query().Get("c")

	if p == "" || c == "" {
		http.Error(w, "missing tracking parameters", http.StatusBadRequest)
		return
	}

	prospectBytes, err := hex.DecodeString(p)
	if err != nil || len(prospectBytes) != 16 {
		http.Error(w, "invalid tracking parameter", http.StatusBadRequest)
		return
	}

	campaignBytes, err := hex.DecodeString(c)
	if err != nil || len(campaignBytes) != 16 {
		http.Error(w, "invalid tracking parameter", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Update prospect status to opted_out
	err = h.DB.UpdateProspectStatus(ctx, database.UpdateProspectStatusParams{
		Status: "opted_out",
		ID:     pgtype.UUID{Bytes: [16]byte(prospectBytes), Valid: true},
	})

	if err != nil {
		h.Logger.Error("failed to update prospect status on unsubscribe", "error", err)
	}

	// Log the unsubscribe event
	_ = h.DB.LogEmailEvent(ctx, database.LogEmailEventParams{
		ProspectID: pgtype.UUID{Bytes: [16]byte(prospectBytes), Valid: true},
		CampaignID: pgtype.UUID{Bytes: [16]byte(campaignBytes), Valid: true},
		ListID:     pgtype.UUID{Valid: false},
		NatsMsgID:  pgtype.Text{String: "unsubscribe_api", Valid: true},
		EventType:  "unsubscribed",
		Metadata:   []byte(`{"source": "one_click"}`),
		UserAgent:  pgtype.Text{String: r.UserAgent(), Valid: r.UserAgent() != ""},
		IpAddress:  pgtype.Text{String: r.RemoteAddr, Valid: true},
		IsHuman:    pgtype.Bool{Bool: true, Valid: true},
	})

	// If POST, we return 200 OK for RFC 8058 One-Click Unsubscribe
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusOK)
		return
	}

	// If GET, render a simple confirmation page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Unsubscribed</title>
		<style>
			body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; text-align: center; padding-top: 50px; background-color: #f9fafb; color: #111827; }
			.container { background-color: white; max-width: 500px; margin: 0 auto; padding: 40px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
			h1 { font-size: 24px; margin-bottom: 16px; }
			p { color: #6b7280; }
		</style>
	</head>
	<body>
		<div class="container">
			<h1>You have been unsubscribed</h1>
			<p>You will no longer receive emails from this sender.</p>
		</div>
	</body>
	</html>
	`)
}

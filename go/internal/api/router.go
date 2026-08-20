package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
)

// NewRouter sets up the HTTP routes for the application.
func NewRouter(h *Handler, wm *micrion.WalletManager, mailpoolHandler *mailpool.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Check) // Legacy
	mux.HandleFunc("GET /health/live", h.Liveness)
	mux.HandleFunc("GET /health/ready", h.Readiness)

	// Generic webhook endpoint (supports all providers)
	mux.HandleFunc("POST /webhooks/{provider}/{conn_id}", h.HandleWebhook)

	// Email Tracking Proxy (Revenue Attribution)
	mux.HandleFunc("GET /api/track/click", h.HandleTrackClick)
	mux.HandleFunc("GET /api/track/open", h.HandleTrackOpen)
	mux.HandleFunc("POST /api/webhooks/conversion", h.HandleConversionWebhook)

	// Unsubscribe Endpoint (One-Click & Web)
	mux.HandleFunc("GET /api/v1/marketing/unsubscribe", h.HandleUnsubscribe)
	mux.HandleFunc("POST /api/v1/marketing/unsubscribe", h.HandleUnsubscribe)

	// Mailpool Deliverability Webhook
	mux.HandleFunc("POST /webhooks/mailpool", h.HandleMailpoolWebhook)

	// Channel webhooks for multi-channel conversational ingress
	mux.HandleFunc("POST /webhooks/twilio/sms", h.HandleTwilioSMSWebhook)
	mux.HandleFunc("POST /webhooks/twilio/whatsapp", h.HandleTwilioWhatsAppWebhook)
	mux.HandleFunc("POST /webhooks/telegram/{bot_token}", h.HandleTelegramWebhook)
	mux.HandleFunc("POST /webhooks/slack", h.HandleSlackWebhook)

	// PCM Auto-Reconciliation OCR Webhook
	mux.HandleFunc("POST /webhooks/pcm/ocr", h.HandlePcmOcrWebhook)

	// QBO OAuth2 endpoints
	mux.HandleFunc("GET /auth/qbo/callback", h.HandleQBOCallback)
	mux.HandleFunc("GET /auth/qbo/url", h.HandleGetQBOAuthURL)

	// Slack OAuth2 endpoints
	mux.HandleFunc("GET /auth/slack/url", h.HandleGetSlackAuthURL)

	// CPA Review Loop & Feedback
	mux.HandleFunc("POST /transactions/{id}/approve", h.HandleApproveTransaction)
	mux.HandleFunc("POST /reconcile/{realmId}", h.HandleReconcileMonth)

	// Attachable & File Uploads
	mux.HandleFunc("POST /realms/{realmId}/entities/{entityType}/{entityID}/attachable", h.HandleUploadAttachable)

	// Local Data Pull API
	mux.HandleFunc("GET /realms/{realmId}/transactions", h.HandleGetUnifiedTransactions)
	mux.HandleFunc("GET /realms/{realmId}/accounts", h.HandleGetAccounts)
	mux.HandleFunc("GET /realms/{realmId}/vendors", h.HandleGetVendors)
	mux.HandleFunc("GET /realms/{realmId}/customers", h.HandleGetCustomers)

	// Clean-Up Mode: CSV/XLSX ingestion and exports
	mux.HandleFunc("POST /files/upload", h.HandleFileIngestion)
	mux.HandleFunc("POST /files/upload/{domain}/{taskType}", h.HandleFileIngestion)
	mux.HandleFunc("GET /files/{session_id}/export", h.HandleExport)
	mux.HandleFunc("GET /files/{session_id}/audit", h.HandleAudit)

	// Moroccan Cognitive Financial Enrichment Engine (CEE-MA) A2A Endpoints
	mux.HandleFunc("POST /api/v1/enrichment/morocco", h.HandleMoroccanEnrichment)
	mux.HandleFunc("GET /api/v1/enrichment/morocco/foreign-totals", h.HandleGetForeignProviderRunningTotals)

	// Wallet Operations (Stripe / Checks)
	mux.Handle("GET /wallet/balance", h.Authenticator.Middleware(http.HandlerFunc(h.HandleGetWalletBalance)))
	mux.Handle("POST /wallet/topup", h.Authenticator.Middleware(http.HandlerFunc(h.HandleCreateWalletTopUp)))
	mux.Handle("POST /wallet/payment-sheet", h.Authenticator.Middleware(http.HandlerFunc(h.HandleCreatePaymentSheet)))

	// Agent Tollbooth Endpoints (Strictly Metered)
	agentToll := micrion.TollboothMiddleware(wm, 1616)
	mux.Handle("GET /agent/realms/{realmId}/transactions", agentToll(http.HandlerFunc(h.HandleGetUnifiedTransactions)))
	mux.Handle("GET /agent/realms/{realmId}/accounts", agentToll(http.HandlerFunc(h.HandleGetAccounts)))
	mux.Handle("GET /agent/realms/{realmId}/vendors", agentToll(http.HandlerFunc(h.HandleGetVendors)))
	mux.Handle("GET /agent/realms/{realmId}/customers", agentToll(http.HandlerFunc(h.HandleGetCustomers)))
	mux.Handle("POST /agent/files/upload", agentToll(http.HandlerFunc(h.HandleFileIngestion)))

	// Stripe Reverse Proxy to Python Worker
	stripeURL, _ := url.Parse("http://python-worker:8000")
	stripeProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(stripeURL)
			if pr.Out.URL.Path == "/financial-connections/sessions" {
				pr.Out.URL.Path = "/v1/financial-connections/session"
			} else if strings.HasPrefix(pr.Out.URL.Path, "/financial-connections/") {
				pr.Out.URL.Path = "/v1" + pr.Out.URL.Path
			} else if strings.HasPrefix(pr.Out.URL.Path, "/webhooks/stripe") {
				pr.Out.URL.Path = "/v1/webhooks/stripe"
			}
		},
	}

	mux.Handle("/financial-connections/", stripeProxy)
	mux.Handle("POST /webhooks/stripe/{conn_id}", stripeProxy)
	mux.Handle("POST /webhooks/stripe", stripeProxy)

	// E2E Redux Test Flow (Global Entrypoint)
	mux.HandleFunc("POST /test/redux", h.HandleTestRedux)

	// Simple Ingress Worker Endpoint
	mux.HandleFunc("/ingress", h.HandleIngressWorker)

	// System & Actor Discovery
	mux.HandleFunc("GET /v1/system/actors", h.HandleListActors)
	mux.HandleFunc("GET /v1/workflows/{id}", h.HandleGetWorkflowStatus)

	// ASE Configuration API
	mux.HandleFunc("GET /ase/configs", h.HandleListASEConfigs)
	mux.HandleFunc("GET /ase/config", h.HandleGetASEConfig)
	mux.HandleFunc("POST /ase/config", h.HandleUpsertASEConfig)
	mux.HandleFunc("GET /ase/editor", h.HandleASEDebugUI)
	mux.HandleFunc("GET /ase/config/versions", h.HandleListASEDagVersions)
	mux.HandleFunc("GET /ase/config/version", h.HandleGetASEDagVersion)
	mux.HandleFunc("POST /ase/config/restore", h.HandleRestoreASEDagVersion)

	// Marketing Lead Forms
	// eg: https://prime-legible-turkey.ngrok-free.app/api/forms/susanaai.com
	mux.HandleFunc("POST /forms/{website}", h.HandleCaptureForm)

	if mailpoolHandler != nil {
		mailpoolHandler.Mount(mux, h.Authenticator.Middleware)
	}

	return mux
}

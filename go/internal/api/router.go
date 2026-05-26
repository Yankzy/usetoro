package api

import (
	"net/http"

	"github.com/Yankzy/usetoro/tap/pkg/micrion"
)

// NewRouter sets up the HTTP routes for the application.
func NewRouter(h *Handler, wm *micrion.WalletManager) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Check) // Legacy
	mux.HandleFunc("GET /health/live", h.Liveness)
	mux.HandleFunc("GET /health/ready", h.Readiness)

	// Generic webhook endpoint (supports all providers)
	mux.HandleFunc("POST /webhooks/{provider}/{conn_id}", h.HandleWebhook)

	// Channel webhooks for multi-channel conversational ingress
	mux.HandleFunc("POST /webhooks/twilio/sms", h.HandleTwilioSMSWebhook)
	mux.HandleFunc("POST /webhooks/twilio/whatsapp", h.HandleTwilioWhatsAppWebhook)
	mux.HandleFunc("POST /webhooks/telegram/{bot_token}", h.HandleTelegramWebhook)

	// QBO OAuth2 endpoints
	mux.HandleFunc("GET /auth/qbo/callback", h.HandleQBOCallback)
	mux.HandleFunc("GET /auth/qbo/url", h.HandleGetQBOAuthURL)

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

	// Wallet Operations (Stripe / Checks)
	mux.HandleFunc("GET /wallet/balance", h.HandleGetWalletBalance)
	mux.HandleFunc("POST /wallet/topup", h.HandleCreateWalletTopUp)
	mux.HandleFunc("POST /financial-connections/sessions", h.HandleCreateStripeSession)
	mux.HandleFunc("GET /financial-connections/accounts/{account_id}", h.HandleGetStripeAccount)

	// Agent Tollbooth Endpoints (Strictly Metered)
	agentToll := micrion.TollboothMiddleware(wm, 1616)
	mux.Handle("GET /agent/realms/{realmId}/transactions", agentToll(http.HandlerFunc(h.HandleGetUnifiedTransactions)))
	mux.Handle("GET /agent/realms/{realmId}/accounts", agentToll(http.HandlerFunc(h.HandleGetAccounts)))
	mux.Handle("GET /agent/realms/{realmId}/vendors", agentToll(http.HandlerFunc(h.HandleGetVendors)))
	mux.Handle("GET /agent/realms/{realmId}/customers", agentToll(http.HandlerFunc(h.HandleGetCustomers)))
	mux.Handle("POST /agent/files/upload", agentToll(http.HandlerFunc(h.HandleFileIngestion)))

	// Backward compatibility: specific Stripe endpoint
	mux.HandleFunc("POST /webhooks/stripe/{conn_id}", h.HandleStripeWebhook)

	// E2E Redux Test Flow (Global Entrypoint)
	mux.HandleFunc("POST /test/redux", h.HandleTestRedux)

	// Simple Ingress Worker Endpoint
	mux.HandleFunc("POST /ingress", h.HandleIngressWorker)

	// System & Actor Discovery
	mux.HandleFunc("GET /v1/system/actors", h.HandleListActors)
	mux.HandleFunc("GET /v1/workflows/{id}", h.HandleGetWorkflowStatus)


	// Marketing Lead Forms
	// eg: https://prime-legible-turkey.ngrok-free.app/api/forms/susanaai.com
	mux.HandleFunc("POST /forms/{website}", h.HandleCaptureForm)

	return mux
}

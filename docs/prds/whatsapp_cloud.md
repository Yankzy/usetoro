# Product Requirements Document (PRD): Native Meta WhatsApp Cloud API Integration

**Status:** Draft / Review  
**Target Release:** Q3 2026  
**Module / Scope:** `go/internal/workers/omni_chat_worker.go`, `go/internal/workers/whatsapp_inbound_worker.go`, `go/internal/api/ingress_router.go`, `go/internal/config`  
**Webhook Ingress Endpoint:** `/api/ingress?activity-type=events.whatsapp.inbound`  
**Architecture Paradigm:** Go Microservices + NATS JetStream Event-Driven Pipeline  

---

## 1. Executive Summary

This Product Requirements Document details the architectural overhaul and implementation specification for replacing third-party WhatsApp providers (e.g., Twilio / WAHA) with a direct, high-performance, native integration with **Meta’s WhatsApp Cloud API (v20.0)**.

By leveraging **Go** and **NATS JetStream**, the platform achieves sub-10ms HTTP webhook ingestion via `/api/ingress?activity-type=events.whatsapp.inbound`. `HandleIngressWorker()` acts as a fast ingress gateway (handling Meta GET challenges and instantly publishing raw POST payloads to NATS), offloading HMAC SHA-256 signature validation, `wamid` deduplication, 24-hour service window tracking via NATS KV Store, and PostgreSQL persistence to the async `whatsapp_inbound_worker`.

---

## 2. Goals & Key Objectives

1. **Native Integration**: Eliminate third-party messaging relays by posting directly to `https://graph.facebook.com/v20.0/{PHONE_NUMBER_ID}/messages`.
2. **Sub-10ms Ingress Performance**: Route Meta webhooks directly through `/api/ingress?activity-type=events.whatsapp.inbound`. Handle verification challenges (`GET`) synchronously, push raw `POST` payloads straight to NATS JetStream (`workers.events.whatsapp.inbound`), and return `HTTP 200 OK` in `<10ms` **without** performing HMAC validation in the API handler.
3. **Async Signature Validation & Extra Work**: Perform HMAC SHA-256 (`X-Hub-Signature-256`) verification asynchronously inside `whatsapp_inbound_worker`.
4. **Message Deduplication**: Utilize Meta's unique WhatsApp Message IDs (`wamid`) inside `whatsapp_inbound_worker` to natively discard duplicate webhook payloads.
5. **24-Hour Service Window Management**: Track incoming client interaction timestamps per phone number using NATS Key-Value store (`whatsapp_sessions`). Dynamically route outbound agent messages through **Meta Pre-Approved Templates** (out-of-window) or **Freeform / Media Messages** (in-window).
6. **Refactor Existing `sendWhatsApp`**: Upgrade `sendWhatsApp(...)` in `go/internal/workers/omni_chat_worker.go` to construct Meta Graph API JSON payloads rather than Twilio API payloads.

---

## 3. System Architecture & Component Design

```text
               +-------------------------------------------------------+
               |                  Meta Graph API                       |
               +---------------------------+---------------------------+
                                           | (Inbound Webhooks)
                                           | URL: /api/ingress?activity-type=events.whatsapp.inbound
                                           v
+---------------------------------------------------------------------------------+
|                               GO BACKEND SERVICE                                |
|                                                                                 |
|  1. Fast Ingress Handler (`/api/ingress?activity-type=events.whatsapp.inbound`) |
|     • GET: Returns `hub.challenge` synchronously for Meta verification          |
|     • POST: Reads body, publishes directly to NATS (`workers.events.whatsapp...`) |
|     • Responds `HTTP 200 OK` in <10ms (NO HMAC validation in API tier)           |
|                                                                                 |
+------------------------------------+--------------------------------------------+
                                     |
                                     v
+---------------------------------------------------------------------------------+
|                                 NATS JETSTREAM                                  |
|                                                                                 |
|  Subjects / Topics:                                                             |
|  • `workers.events.whatsapp.inbound`   (Raw incoming webhook event payload)      |
|  • `events.whatsapp.inbound.statuses`  (Message delivered/read/failed)               |
|  • `whatsapp.outbound.send`            (Agent dispatch queue)                   |
|                                                                                 |
|  NATS KV Bucket:                                                                |
|  • `whatsapp_sessions`                 (Key: `client:<phone>`, Val: Timestamp)  |
|                                                                                 |
+------------------+-----------------------------------------------+--------------+
                   |                                               ^
                   v                                               |
+------------------+--------------------+     +---------------------+--------------+
|   WhatsApp Inbound Worker             |     |     OmniChatWorker (Go Worker)     |
|   (`whatsapp_inbound_worker.go`)       |     |                                          |
|                                        |     |  • Listens to `whatsapp.outbound.send`   |
|  • Listens: `workers.events.whatsapp..`|     |  • Refactored `sendWhatsApp()` method    |
|  • Validates HMAC SHA-256 Signature    |     |  • Handles Meta Bearer Token             |
|  • Extracts `wamid` & updates KV store |     |  • Posts to Meta Graph API v20.0        |
|  • Persists message to Postgres DB     |--+  |                                          |
|  • Forwards to Accounting Agent        |  |  +------------------------------------------+
+----------------------------------------+  |
                                            v
                         +-----------------------------------+
                         | Agent Engine / Accounting Workflow|
                         +-----------------------------------+
```

---

## 4. Key Implementation Components

### 4.1 Ingress Webhook Handler (`go/internal/api/ingress_router.go`)

The Meta webhook URL configured in Meta App Dashboard is:
`https://<domain>/api/ingress?activity-type=events.whatsapp.inbound`

The implementation in `HandleIngressWorker` provides ultra-fast ingress (<10ms):

#### 1. GET Request (Verification Challenge)
When setting up or updating webhooks, Meta sends a `GET` request:
- `hub.mode`: `subscribe`
- `hub.verify_token`: Verified against `MetaWhatsAppVerifyToken` in configuration.
- `hub.challenge`: Synchronously echoed back in body with `HTTP 200 OK`.

#### 2. POST Request (Zero-Overhead Event Ingress)
- Reads the raw request body.
- Passes original headers (including `X-Hub-Signature-256`) along with body data into NATS message headers.
- Asynchronously publishes to NATS JetStream subject derived from `workers.events.whatsapp.inbound`.
- Returns `HTTP 200 OK` immediately without running HMAC calculation.

---

### 4.2 Async Inbound Worker & Validation (`go/internal/workers/whatsapp_inbound_worker.go`)

`whatsapp_inbound_worker` processes the queued messages off NATS JetStream:

1. **HMAC Signature Validation**:
   - Inspects `X-Hub-Signature-256` header on the NATS message.
   - Computes `crypto/hmac` SHA-256 over raw body using `MetaWhatsAppAppSecret`.
   - If invalid, logs warning, terminates message delivery (`msg.Term()`), and exits.
2. **Deduplication via `wamid`**:
   - Parses `entry[0].changes[0].value.messages[0].id`.
   - Uses `wamid` for deduplication.
3. **Session Window KV Update**:
   - Updates NATS KV bucket `whatsapp_sessions` key `client:{phone_number}` with `time.Now().Unix()`.
4. **Database Persistence & Agent Handoff**:
   - Writes conversation to PostgreSQL (`SaveConversationSessionMessage`).
   - Dispatches parsed message & attachments to agent execution framework (`proof.incoming.chat`).

---

### 4.3 NATS KV 24-Hour Session Store (`go/internal/services/whatsapp_session_store.go`)

Meta restricts business-initiated messages outside of a 24-hour customer service window:

| Scenario | Message Type | Rule & Engine Behavior |
| :--- | :--- | :--- |
| **Agent initiates contact** *(e.g., missing receipt alert)* | **Template Message** | Must use a pre-approved Meta Template ID (e.g. `missing_receipt_reminder`). Freeform text is rejected by Meta. |
| **Client replies** *(e.g., sends receipt PDF / text)* | **Freeform / Media** | Opens a **24-Hour Service Window**. Agent can reply freely with AI-generated text, interactive buttons, or media. |

#### NATS KV Store Specifications:
- **Bucket Name**: `whatsapp_sessions`
- **Key**: `client:{phone_number}` (E.164 formatted string, e.g. `client:14155552671`)
- **Value**: Unix timestamp of the last incoming user message.
- **TTL**: 24 Hours (`24 * time.Hour`).

---

### 4.4 Outbound Dispatcher Refactoring (`go/internal/workers/omni_chat_worker.go`)

The core outbound dispatcher function `sendWhatsApp` currently uses Twilio API. It will be refactored to support Meta Graph API v20.0.

#### Standard Freeform Text Payload Structs:
```go
type MetaWhatsAppTextPayload struct {
	MessagingProduct string           `json:"messaging_product"` // "whatsapp"
	RecipientType    string           `json:"recipient_type"`    // "individual"
	To               string           `json:"to"`                // "14155552671"
	Type             string           `json:"type"`              // "text"
	Text             MetaWhatsAppText `json:"text"`
}

type MetaWhatsAppText struct {
	PreviewURL bool   `json:"preview_url"`
	Body       string `json:"body"`
}
```

#### Pre-Approved Template Payload Structs:
```go
type MetaWhatsAppTemplatePayload struct {
	MessagingProduct string               `json:"messaging_product"` // "whatsapp"
	To               string               `json:"to"`                // "14155552671"
	Type             string               `json:"type"`              // "template"
	Template         MetaWhatsAppTemplate `json:"template"`
}

type MetaWhatsAppTemplate struct {
	Name       string                  `json:"name"`       // e.g. "missing_receipt_reminder"
	Language   MetaWhatsAppLanguage   `json:"language"`   // e.g. {"code": "en_US"}
	Components []MetaWhatsAppComponent `json:"components,omitempty"`
}

type MetaWhatsAppLanguage struct {
	Code string `json:"code"`
}

type MetaWhatsAppComponent struct {
	Type       string                  `json:"type"`       // "body", "header"
	Parameters []MetaWhatsAppParameter `json:"parameters"`
}

type MetaWhatsAppParameter struct {
	Type string `json:"type"` // "text", "image", "document"
	Text string `json:"text,omitempty"`
}
```

---

## 5. Technical Implementation Plan & File Modifications

### [MODIFY] [config.go](file:///Users/Yankz/programming/usetoro/go/internal/config/config.go) & [defaults.yml](file:///Users/Yankz/programming/usetoro/go/internal/config/defaults.yml)
Add Meta WhatsApp configuration options and NATS stream subject registration:
```yaml
# defaults.yml
jetstream:
  subjects:
    - whatsapp.>
```
```go
// config.go
// Meta WhatsApp Cloud API Config
MetaWhatsAppToken         string `mapstructure:"meta_whatsapp_token"`
MetaWhatsAppPhoneNumberID string `mapstructure:"meta_whatsapp_phone_number_id"`
MetaWhatsAppAppSecret     string `mapstructure:"meta_whatsapp_app_secret"`
MetaWhatsAppVerifyToken   string `mapstructure:"meta_whatsapp_verify_token"`
```

---

### [MODIFY] [ingress_router.go](file:///Users/Yankz/programming/usetoro/go/internal/api/ingress_router.go)
Simplify `HandleIngressWorker` so it handles the GET challenge, attaches headers (such as `X-Hub-Signature-256`), publishes to NATS immediately, and returns HTTP 200 OK:

```go
func (h *Handler) HandleIngressWorker(w http.ResponseWriter, r *http.Request) {
	activityType := r.URL.Query().Get("activity-type")
	if activityType == "" {
		http.Error(w, "missing activity-type", http.StatusBadRequest)
		return
	}

	// 1. Meta WhatsApp GET Verification Challenge
	if activityType == "events.whatsapp.inbound" && r.Method == http.MethodGet {
		mode := r.URL.Query().Get("hub.mode")
		token := r.URL.Query().Get("hub.verify_token")
		challenge := r.URL.Query().Get("hub.challenge")

		if mode == "subscribe" && token == h.Config.MetaWhatsAppVerifyToken {
			h.Logger.Info("ingress: meta whatsapp verification succeeded")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(challenge))
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	subject, err := core.BuildWorkerInboxFromActivity("workers." + activityType)
	if err != nil {
		http.Error(w, "invalid activity-type", http.StatusBadRequest)
		return
	}

	msg := nats.NewMsg(subject)
	msg.Data = body
	msg.Header.Set(nats.MsgIdHdr, fmt.Sprintf("ingress-%d", time.Now().UnixNano()))

	// Pass X-Hub-Signature-256 header to worker for async verification
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		msg.Header.Set("X-Hub-Signature-256", sig)
	}

	if _, err := h.NATS.PublishMsg(msg); err != nil {
		h.Logger.Error("ingress: failed to publish to NATS", "error", err, "subject", subject)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return 200 OK immediately (<10ms response time)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"published"}`))
}
```

---

### [NEW] [whatsapp_inbound_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/whatsapp_inbound_worker.go)
Create a dedicated NATS JetStream worker that subscribes to `workers.events.whatsapp.inbound`:

```go
func (w *WhatsAppInboundWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// 1. Validate HMAC SHA-256 signature asynchronously inside the worker
	sig := msg.Header.Get("X-Hub-Signature-256")
	if !verifyMetaSignature(sig, msg.Data, w.cfg.MetaWhatsAppAppSecret) {
		w.logger.Warn("whatsapp_inbound_worker: invalid HMAC SHA-256 signature")
		msg.Term() // Poison pill: stop redelivery
		return nil
	}

	// 2. Parse Meta Webhook Payload & Extract wamid / Phone / Message Content
	payload, err := parseMetaPayload(msg.Data)
	if err != nil {
		w.logger.Error("whatsapp_inbound_worker: invalid payload", "error", err)
		msg.Term()
		return nil
	}

	// 3. Update NATS KV 24-hr session window timestamp
	if err := w.sessionStore.UpdateSession(ctx, payload.FromPhone); err != nil {
		w.logger.Error("whatsapp_inbound_worker: failed to update session window", "error", err)
	}

	// 4. Save to PostgreSQL & Forward to Accounting Agent
	if err := w.persistAndForward(ctx, payload); err != nil {
		w.logger.Error("whatsapp_inbound_worker: failed to process message", "error", err)
		return err // Nak for retry
	}

	return nil
}
```

---

### [MODIFY] [omni_chat_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/omni_chat_worker.go)
Refactor `sendWhatsApp` to execute native Graph API HTTP requests:

```go
func (w *OmniChatWorker) sendWhatsApp(ctx context.Context, to, body string) error {
	if w.cfg.MetaWhatsAppToken == "" || w.cfg.MetaWhatsAppPhoneNumberID == "" {
		w.logger.Warn("WhatsApp channel not configured (missing Meta Cloud API credentials)")
		return nil
	}

	cleanPhone := sanitizePhoneNumber(to)
	
	// Check if 24-hr session is active or if template outreach is required
	isSessionActive := w.sessionStore.IsSessionActive(ctx, cleanPhone)

	var payloadBytes []byte
	var err error

	if isSessionActive {
		payload := MetaWhatsAppTextPayload{
			MessagingProduct: "whatsapp",
			RecipientType:    "individual",
			To:               cleanPhone,
			Type:             "text",
			Text:             MetaWhatsAppText{Body: body, PreviewURL: true},
		}
		payloadBytes, err = json.Marshal(payload)
	} else {
		// Fallback to pre-approved Meta missing receipt template
		payload := MetaWhatsAppTemplatePayload{
			MessagingProduct: "whatsapp",
			To:               cleanPhone,
			Type:             "template",
			Template: MetaWhatsAppTemplate{
				Name:     "missing_receipt_reminder",
				Language: MetaWhatsAppLanguage{Code: "en_US"},
				Components: []MetaWhatsAppComponent{
					{
						Type: "body",
						Parameters: []MetaWhatsAppParameter{
							{Type: "text", Text: body},
						},
					},
				},
			},
		}
		payloadBytes, err = json.Marshal(payload)
	}

	if err != nil {
		return fmt.Errorf("whatsapp: failed to marshal payload: %w", err)
	}

	apiURL := fmt.Sprintf("https://graph.facebook.com/v20.0/%s/messages", w.cfg.MetaWhatsAppPhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("whatsapp: failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+w.cfg.MetaWhatsAppToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp: Meta API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	w.logger.Info("whatsapp: message successfully dispatched via Meta Cloud API", "to", cleanPhone)
	return nil
}
```

---

## 6. Verification & Test Plan

### Automated Unit & Integration Tests
1. **Meta Webhook Verification (GET)**: Test `HandleIngressWorker` with `activity-type=events.whatsapp.inbound`, `hub.mode=subscribe`, and `hub.verify_token`, verifying exact `hub.challenge` return.
2. **Fast Ingress Test (POST)**: Verify `HandleIngressWorker` returns `HTTP 200 OK` in `<10ms` without performing HMAC checks.
3. **Async Worker HMAC Validation**: Test `whatsapp_inbound_worker` HMAC validation against valid and invalid `X-Hub-Signature-256` headers.
4. **Session Window Logic**:
   - Verify `IsSessionActive` returns `true` when timestamp is `< 24 hours`.
   - Verify `IsSessionActive` returns `false` when key is expired/missing, triggering template payloads.
5. **Mock HTTP Graph API**: Unit test `sendWhatsApp` with an `httptest.Server` verifying correct headers (`Bearer Token`), endpoint formatting, and JSON payload structure.

---

## 7. Migration Strategy

1. **Dual Routing Stage**: Configure both Twilio and Meta endpoints during transition.
2. **Credential Provisioning**: Add `META_WHATSAPP_TOKEN`, `META_WHATSAPP_PHONE_NUMBER_ID`, `META_WHATSAPP_APP_SECRET`, and `META_WHATSAPP_VERIFY_TOKEN` to environment secrets.
3. **Cutover**: Configure Meta App Webhook URL to `https://api.usetoro.io/api/ingress?activity-type=events.whatsapp.inbound`.

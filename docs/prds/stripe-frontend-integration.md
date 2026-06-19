# Stripe Frontend Integration Guide

## Task: Integrate Stripe Checkout and Financial Connections

We have a Stripe microservice running at the base URL below. Wire up the frontend to call these endpoints and handle the resulting Stripe flows.

**Base URL:** `http://localhost:8005` (Go API Gateway)
(Internal nginx route for webhooks: `/webhooks/stripe` — securely proxied to the Python worker)

---

## Endpoint 1: Create Checkout Session

**Purpose:** Start a one-time payment via Stripe Hosted Checkout. You get back a URL that you redirect the user to.

**Request:**
```
POST {BASE_URL}/wallet/topup
Content-Type: application/json

{
  "micrion_amount": 255000,
  "success_url": "https://yourapp.com/settings?payment=success",
  "cancel_url": "https://yourapp.com/settings?payment=cancelled"
}
```

**Response (200):**
```json
{ "session_url": "https://checkout.stripe.com/c/pay/cs_test_abc123..." }
```

**Frontend flow:**
1. Call this endpoint with the desired micrion amount to purchase. The Go backend automatically infers your user ID.
2. On success, redirect the browser to `session_url` (Stripe's hosted page).
3. The user completes payment on Stripe's page, then is redirected to your `success_url` or `cancel_url`.
4. A webhook from Stripe will mark the transaction COMPLETED in the backend — your frontend doesn't need to poll for this, but you may want to listen for a real-time event from the WebSocket server or just refresh state when the user lands on success_url.

**Validation:**
- `micrion_amount` must be > 0 (10,000 Micrions = $1.00 USD)
- `success_url` and `cancel_url` must be valid HTTPS URLs

**Errors:**
- `409 Conflict` — duplicate session (already exists for this purchase)
- `502 Bad Gateway` — Stripe API error
- `500 Internal Server Error` — auth failure or broker error

---

## Endpoint 2: Create Financial Connections Session

**Purpose:** Open Stripe's bank-linking modal. You get back a `client_secret` that you pass to the Stripe.js SDK to render the UI sheet.

**Request:**
```
POST {BASE_URL}/financial-connections/sessions
Content-Type: application/json

{
  "user_id": "<current-user-uuid>",
  "stripe_customer_id": "cus_ABC123"
}
```

**Response (200):**
```json
{ "client_secret": "fcsess_abc123_secret_xyz..." }
```

**Frontend flow:**
1. Call this endpoint with the user's UUID and their Stripe customer ID.
2. Use the returned `client_secret` with Stripe.js (`@stripe/stripe-js`) to open the Financial Connections modal:

```js
import { loadStripe } from '@stripe/stripe-js';

const stripe = await loadStripe('pk_test_...');

const result = await stripe.collectFinancialConnectionsAccount({
  clientSecret: 'fcsess_abc123_secret_xyz...',
});
```

The user will be shown Stripe's bank-selection modal. When they finish, the returned `result` contains the linked `financialConnectionsSession` with account details.

3. After the user completes linking in Stripe's modal, call Endpoint 3 to sync.

**Validation:**
- `stripe_customer_id` must be non-empty
- `user_id` is required

**Errors:**
- `409 Conflict` — session already exists
- `502 Bad Gateway` — Stripe API error

---

## Endpoint 3: Sync Linked Accounts

**Purpose:** After the user finishes the Financial Connections flow, call this to pull their linked bank accounts into our database and notify the Go backend via NATS.

**Request:**
```
POST {BASE_URL}/financial-connections/sync
Content-Type: application/json

{
  "session_id": "fcsess_abc123",
  "user_id": "<current-user-uuid>"
}
```

**Response (200):**
```json
[
  {
    "id": "fca_xyz789",
    "stripe_account_id": "fca_xyz789",
    "institution_name": "Chase",
    "last4": "1234",
    "subcategory": "checking",
    "status": "active"
  }
]
```

Returns an empty array `[]` if no accounts were linked yet.

**Frontend flow:**
1. Call this after the Stripe modal closes successfully.
2. Display the returned accounts to the user for confirmation.
3. The Go backend receives a `stripe.financial_connections.linked` event via NATS and can begin syncing transaction data.

**Errors:**
- `500 Internal Server Error` — event broker unavailable (triggers Stripe retry)

---

## Webhook Endpoint (server-side only)

The frontend does NOT call this. Stripe sends `POST /webhooks/stripe` directly. It handles:
- `checkout.session.completed` — marks payment done, emits NATS event
- `checkout.session.expired` — marks payment expired, emits NATS event
- `financial_connections.session.updated` — emits NATS event for backend sync pipelines

---

## What you need from the environment

| Variable | Purpose |
|----------|---------|
| `STRIPE_PUBLISHABLE_KEY` | Stripe publishable key (`pk_test_...`) — used client-side for the Stripe.js SDK |
| Stripe customer ID | Retrieved after creating a Stripe customer for the logged-in user (call your Go backend's customer-creation endpoint or have the Go backend create it on signup) |

---

## Typical payment flow (end-to-end)

1. User clicks "Upgrade to Premium ($25.50 / 255,000 Micrions)"
2. Frontend calls `POST /wallet/topup`
3. Frontend redirects browser to `session_url`
4. User pays on Stripe's hosted page
5. Stripe redirects browser to `success_url`
6. Simultaneously, Stripe sends `checkout.session.completed` webhook to the backend
7. Backend marks transaction COMPLETED and publishes to NATS
8. Go backend provisions the purchase

## Typical bank-linking flow (end-to-end)

1. User clicks "Link Bank Account"
2. Frontend calls `POST /financial-connections/sessions`
3. Frontend uses `client_secret` to open Stripe Connect modal
4. User selects their bank and authenticates
5. Stripe modal closes — frontend calls `POST /financial-connections/sync`
6. Frontend displays linked accounts
7. Go backend receives NATS event and begins background data sync

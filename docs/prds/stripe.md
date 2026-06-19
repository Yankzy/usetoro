# Product Requirement Document (PRD): Stripe Integration Microservice

## 1. System Architecture & Constraints

This document mandates the creation of a standalone Python microservice responsible for interacting with the Stripe API.

* **Language/Framework:** Python 3.10+ (FastAPI recommended for async concurrency).
* **Isolation Constraint:** Do not generate any frontend or Go code. This service operates independently.
* **Shared Infrastructure:** The service connects directly to a shared relational database (used by the core Go backend) and communicates state changes via an ATS Event Broker (e.g., NATS, Kafka, RabbitMQ).
* **SDK Constraint:** All outbound API requests to Stripe must strictly use the modern thread-safe `StripeClient` instance (SDK v8+). The legacy global `stripe.api_key` configuration is strictly prohibited.

---

## 2. Environment & Bootstrapping

The microservice must be configured via environment variables upon startup.

* `STRIPE_SECRET_KEY`: Used to instantiate the `StripeClient`.
* `STRIPE_WEBHOOK_SECRET`: Used to cryptographically verify incoming Stripe events.
* `DATABASE_URL`: Connection string for the shared database.
* `ATS_BROKER_URL`: Connection string for the event broker.

**Global Client Instantiation Protocol:**

```python
import os
import stripe
from stripe import StripeClient

# This single instance must be injected or imported across the service
stripe_client = StripeClient(os.environ.get("STRIPE_SECRET_KEY"))
webhook_secret = os.environ.get("STRIPE_WEBHOOK_SECRET")

```

---

## 3. Feature 1: Stripe Checkout (Hosted Payment)

This feature allows users to pay via Stripe's hosted Checkout page.

### 3.1 Endpoint: Create Checkout Session

* **Route:** `POST /v1/payments/checkout/session`
* **Input Payload (JSON):**
```json
{
  "user_id": "uuid-1234",
  "amount_in_dollars": 25.50,
  "product_name": "Premium Plan",
  "success_url": "https://yourapp.com/success",
  "cancel_url": "https://yourapp.com/cancel"
}

```


* **Execution Flow:**
1. Convert `amount_in_dollars` to cents (integer).
2. Call the `StripeClient` to generate a Checkout Session:
```python
session = stripe_client.v1.checkout.sessions.create(
    params={
        "mode": "payment",
        "success_url": payload["success_url"],
        "cancel_url": payload["cancel_url"],
        "line_items": [{
            "price_data": {
                "currency": "usd",
                "product_data": {"name": payload["product_name"]},
                "unit_amount": amount_in_cents,
            },
            "quantity": 1,
        }],
        # Attach user_id to metadata so it returns in the webhook
        "metadata": {"user_id": payload["user_id"]} 
    }
)

```


3. **Database Write:** Insert a row into the shared `transactions` table with status `PENDING` and the `session.id`.
4. **Return (200 OK):** Respond with the generated `checkout_url` so the frontend can redirect the user.
```json
{ "checkout_url": "https://checkout.stripe.com/c/pay/cs_test_..." }

```





---

## 4. Feature 2: Stripe Financial Connections

This feature enables users to securely link their external bank accounts via the Stripe modal.

### 4.1 Endpoint: Initialize Bank Link Session

* **Route:** `POST /v1/financial-connections/session`
* **Input Payload (JSON):**
```json
{
  "user_id": "uuid-1234",
  "stripe_customer_id": "cus_ABC123"
}

```


* **Execution Flow:**
1. Use the `StripeClient` to create the session with required data permissions:
```python
fc_session = stripe_client.v1.financial_connections.sessions.create(
    params={
        "account_holder": {
            "type": "customer",
            "customer": payload["stripe_customer_id"]
        },
        "permissions": ["balances", "ownership", "payment_method", "transactions"]
    }
)

```


2. **Database Write:** Log the connection attempt in `financial_connection_attempts` with status `INITIATED`.
3. **Return (200 OK):** Respond with the `client_secret` so the frontend can open the Stripe UI sheet.



### 4.2 Endpoint: Sync Linked Accounts

* **Route:** `POST /v1/financial-connections/sync`
* **Input Payload (JSON):**
```json
{
  "session_id": "fcsess_123456",
  "user_id": "uuid-1234"
}

```


* **Execution Flow:**
1. Retrieve the completed session: `stripe_client.v1.financial_connections.sessions.retrieve(session_id)`.
2. Extract linked accounts from the `accounts.data` array.
3. **Database Write:** Upsert each bank account's metadata (institution, last 4 digits) into the `linked_bank_accounts` table.
4. **ATS Publish:** Emit `stripe.financial_connections.linked` to the ATS broker to alert the Go backend.



---

## 5. Webhook Processing & ATS Translation

The microservice must receive asynchronous state changes from Stripe and publish them to the ATS broker so the core Go backend can react.

### 5.1 Endpoint: Stripe Webhook Listener

* **Route:** `POST /v1/webhooks/stripe`
* **Security & Parsing Protocol:** You must capture the raw request body as bytes/text (do not parse as JSON first). Use `stripe.Webhook.construct_event` to cryptographically verify the payload.

```python
import stripe
from fastapi import Request, HTTPException

@app.post("/v1/webhooks/stripe")
async def stripe_webhook(request: Request):
    payload = await request.body()
    sig_header = request.headers.get("stripe-signature")

    try:
        # Note: Event parsing relies on the base stripe utility for V1 webhooks
        event = stripe.Webhook.construct_event(
            payload, sig_header, webhook_secret
        )
    except stripe.SignatureVerificationError as e:
        raise HTTPException(status_code=400, detail="Invalid signature")
    
    # Pass event to the router below
    await handle_stripe_event(event)
    return {"status": "success"}

```

### 5.2 Event Routing Logic (`handle_stripe_event`)

The handler must switch logic based on `event.type`:

* **Condition: `checkout.session.completed**`
1. Extract `session_id` and `metadata.user_id`.
2. **Database Write:** Update `transactions` table status from `PENDING` to `COMPLETED`.
3. **ATS Publish:** Emit `stripe.checkout.completed` with the `user_id` so the Go backend provisions the purchased goods/services.


* **Condition: `checkout.session.expired**`
1. **Database Write:** Update `transactions` status to `FAILED/EXPIRED`.
2. **ATS Publish:** Emit `stripe.checkout.expired`.


* **Condition: `financial_connections.session.updated**`
1. Extract `session_id`.
2. **ATS Publish:** Emit `stripe.financial_connections.session_updated` to trigger background data sync pipelines.



---

## 6. Strict Error Handling Guidelines

* **API Failure Wrapping:** All `stripe_client.v1` executions must be wrapped in `try/except stripe.StripeError` blocks.
* **Broker Fault Tolerance:** All ATS publish commands must be fault-tolerant. If the broker is unreachable, log heavily and return a 500 status to trigger Stripe's automatic webhook retry mechanisms.
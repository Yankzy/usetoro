# Test Results: Stripe Integration Microservice

**Date:** 2026-06-06
**Python:** 3.14.4
**pytest:** 9.0.3
**Result: ALL 115 TESTS PASSED**

## Summary

| Category | Tests | Passed | Failed |
|----------|-------|--------|--------|
| Models (Pydantic validation) | 23 | 23 | 0 |
| Error classes | 14 | 14 | 0 |
| Stripe service | 13 | 13 | 0 |
| Database functions | 16 | 16 | 0 |
| NATS client | 10 | 10 | 0 |
| Route handlers | 28 | 28 | 0 |
| Main app (error handlers, health) | 11 | 11 | 0 |
| **Total** | **115** | **115** | **0** |

## Test Files

All under `python-worker/tests/`:
- `conftest.py` — sets required env vars (`STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `DATABASE_URL`, `NATS_URL`)
- `test_models.py` — 23 tests
- `test_errors.py` — 14 tests
- `test_stripe_service.py` — 13 tests
- `test_database.py` — 16 tests
- `test_nats_client.py` — 10 tests
- `test_routes.py` — 28 tests
- `test_main.py` — 11 tests
- `__init__.py` — empty

## Test Details

### 1. Models Validation (23 tests)
- **CreateCheckoutSessionRequest:** Valid requests, amount > 0 enforcement (0.0 and -10.0 rejected), HttpUrl validation (non-URL and non-HTTP URLs rejected), missing fields, type coercion (int -> float)
- **CreateFCSessionRequest:** Valid requests, min_length=1 enforcement (empty string rejected), whitespace-only passes, missing fields
- **SyncFCSessionRequest:** Valid requests, min_length=1 on session_id, missing fields
- **Response models:** Serialization correctness, nullable fields (last4, subcategory), empty values allowed where appropriate, missing required fields rejected

### 2. Error Classes (14 tests)
- **StripeAPIError:** Default status_code=502, default code="STRIPE_ERROR", custom status_code and code, exception inheritance (raises/catchable), cause chaining
- **NATSPublishError:** Basic construction, exception inheritance, cause chaining, type independence from StripeAPIError

### 3. Stripe Service (13 tests)
- **create_checkout_session:** Returns dict with id+url, passes correct params (mode, success_url, cancel_url, line_items [price_data, product_data, unit_amount], metadata with user_id), wraps stripe.StripeError -> StripeAPIError(502), amount_cents passed as unit_amount
- **create_fc_session:** Returns dict with id+client_secret, passes account_holder (type=customer, customer=stripe_customer_id), hardcoded permissions ["balances", "ownership", "payment_method", "transactions"], wraps StripeError
- **retrieve_fc_session:** Returns session with accounts.data array, handles null accounts (returns []), wraps StripeError
- **verify_webhook_signature:** Returns stripe.Event on valid signature, raises StripeAPIError(400) on SignatureVerificationError, passes raw_body/signature_header/webhook_secret to construct_event

### 4. Database Functions (16 tests)
- **Pool lifecycle:** init_pool creates pool singleton, get_pool raises RuntimeError when not initialized, close_pool calls close() on initialized pool, close_pool is no-op when pool is None, init_pool correctly sets global
- **insert_checkout_session:** Executes INSERT with correct params (entity_id, stripe_session_id, amount_cents, product_name, metadata), SQL contains `::jsonb` cast for metadata, propagates asyncpg.UniqueViolationError
- **update_checkout_session_status:** COMPLETED and EXPIRED status updates, SQL contains `updated_at = NOW()`
- **insert_fc_attempt:** Correct params (entity_id, fc_session_id, stripe_customer_id), propagates UniqueViolationError
- **upsert_linked_bank_account:** All 7 fields, nullable last4/subcategory passed as None, SQL contains `ON CONFLICT (stripe_account_id) DO UPDATE` with `updated_at = NOW()`

### 5. NATS Client (10 tests)
- **connect:** Splits comma-separated URL into server list, sets client name "stripe-worker", handles single server
- **close:** Calls drain() when connected, no-op when not connected
- **publish:** JSON-encodes payload as bytes, raises NATSPublishError("NATS not connected") when _nc is None, raises NATSPublishError with descriptive message on publish Exception, logs error on failure, correct subject routing

### 6. Route Handlers (28 tests)
- **POST /payments/checkout/session (7 tests):**
  - Dollar-to-cents conversion: 25.50 -> 2550, 0.10 -> 10 (round prevents float truncation)
  - DB insert called with PENDING status and correct params
  - 409 Conflict on asyncpg.UniqueViolationError
  - Success/cancel URLs passed to stripe_service
  - StripeAPIError propagation
- **POST /financial-connections/session (4 tests):**
  - Returns FCSessionResponse with client_secret
  - Inserts INITIATED attempt via database module
  - 409 Conflict on duplicate
  - StripeAPIError propagation
- **POST /financial-connections/sync (7 tests):**
  - Syncs accounts and returns BankAccountResponse list
  - Publishes "stripe.financial_connections.linked" NATS event with accounts
  - Empty list when accounts.data is empty (no NATS publish)
  - Empty list when accounts key is missing
  - 500 on NATS publish failure ("Event broker unavailable")
  - Handles missing optional account fields (last4, subcategory, institution_name)
  - StripeAPIError on session not found (404)
- **POST /webhooks/stripe (10 tests):**
  - Unknown event type returns {"status": "success"} without DB/NATS action (idempotent for Stripe)
  - checkout.session.completed: updates status to COMPLETED, publishes "stripe.checkout.completed"
  - checkout.session.completed with missing user_id in metadata: returns 200, no DB update, no NATS publish
  - checkout.session.completed NATS failure: returns 500 ("Event broker unavailable") for Stripe retry
  - checkout.session.expired: updates status to EXPIRED, publishes "stripe.checkout.expired"
  - checkout.session.expired NATS failure: returns 500
  - financial_connections.session.updated: publishes "stripe.financial_connections.session_updated" with session_id, accounts, status
  - financial_connections.session.updated NATS failure: returns 500
  - Missing stripe-signature header: returns 400
  - Invalid signature (SignatureVerificationError): returns 400

### 7. Main App (11 tests)
- **Health endpoint:** Returns {"status": "healthy"}
- **StripeAPIError handler:** Returns custom status_code and code in JSONResponse body, preserves custom status codes (404), default code is "STRIPE_ERROR"
- **NATSPublishError handler:** Always returns 500, body has generic "Event broker unavailable" message (does NOT leak internal NATS error details), code is "NATS_ERROR"
- **App metadata:** title = "Stripe Integration Microservice", version = "1.0.0", all 5 routes mounted (/health + 4 /v1 routes)

## Coverage by Spec Requirements

| Spec Edge Case | Test Coverage |
|---------------|---------------|
| amount_in_dollars > 0 | test_amount_must_be_positive, test_amount_must_be_positive_negative_value |
| stripe_customer_id min_length=1 | test_empty_stripe_customer_id |
| HttpUrl validation for success_url/cancel_url | test_invalid_success_url, test_invalid_cancel_url, test_non_http_url_rejected |
| Dollar-to-cents with round() | test_converts_dollars_to_cents_correctly, test_converts_floating_point_dollars_safely |
| PENDING row inserted on checkout | test_inserts_pending_row_into_database |
| 409 on duplicate checkout session | test_returns_409_on_duplicate_session_id |
| 409 on duplicate FC session | test_returns_409_on_duplicate_session |
| Hardcoded FC permissions | test_passes_hardcoded_permissions |
| Empty accounts list returns [] | test_returns_empty_list_when_no_accounts |
| No accounts key returns [] | test_returns_empty_list_when_no_data_key |
| NATS publish failure returns 500 | test_handles_nats_publish_failure (sync), test_checkout_completed_nats_failure_returns_500, test_checkout_expired_nats_failure_returns_500, test_fc_session_updated_nats_failure_returns_500 |
| Missing user_id in webhook metadata returns 200 | test_checkout_completed_missing_user_id_still_returns_200 |
| Unknown webhook event returns 200 | test_unknown_event_type_returns_success_ignored |
| Missing stripe-signature returns 400 | test_missing_signature_header_returns_400 |
| Invalid signature returns 400 | test_invalid_signature_returns_400 |
| StripeError wraps to StripeAPIError | test_wraps_stripe_error_as_stripe_api_error (service), test_propagates_stripe_api_error (routes) |
| NATSPublishError handler returns generic message | test_provides_generic_message_not_raw_error |
| Health endpoint always returns healthy | test_health_returns_healthy |
| ON CONFLICT UPDATE in upsert | test_uses_on_conflict_clause |
| Pool not initialized raises RuntimeError | test_get_pool_raises_when_not_initialized |
| UT NATS disconnected raises NATSPublishError | test_raises_nats_publish_error_when_not_connected |

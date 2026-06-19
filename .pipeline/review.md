# Review: Stripe Integration Microservice

## VERDICT: NEEDS WORK

The implementation is substantially correct and well-structured, but contains a **security issue** (Stripe error message leakage) that must be fixed before shipping, plus several minor spec deviations.

---

## ISSUE 1 — SECURITY: Stripe error messages exposed to client (CRITICAL)

**File:** `python-worker/app/stripe_service.py`, lines 37-38, 54-55, 69-70

```python
except stripe.StripeError as e:
    raise StripeAPIError(str(e), status_code=502) from e
```

Raw `str(e)` from Stripe SDK errors is passed directly into `StripeAPIError.message`, which is returned verbatim in the HTTP response body via the global exception handler in `main.py`. Stripe error messages can include API key prefixes (e.g., "Invalid API Key provided: sk_live_..."), resource IDs, and request IDs.

**Fix:** Catch `stripe.AuthenticationError` specifically and return a generic message. Log the raw error server-side:

```python
except stripe.AuthenticationError:
    raise StripeAPIError("Stripe authentication failed", status_code=500)
except stripe.StripeError as e:
    logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
    raise StripeAPIError("Stripe API error", status_code=502) from e
```

---

## ISSUE 2 — SPEC DEVIATION: checkout.session.expired does not check affected rows

**File:** `python-worker/app/routes.py`, lines 144-157

The spec states: "If the DB row doesn't exist (race condition or idempotency), log and return 200." The implementation calls `update_checkout_session_status` but never checks the affected row count.

**Fix:** Return affected-row count from `update_checkout_session_status` and log in route handler.

---

## ISSUE 3 — CODE QUALITY: Bare `except Exception` in webhook verification

**File:** `python-worker/app/routes.py`, lines 112-119

```python
try:
    event = await stripe_service.verify_webhook_signature(...)
except Exception:
    raise HTTPException(status_code=400, detail="Invalid signature")
```

Catching bare `Exception` is overly broad. If an unrelated error occurs, it would be misreported as 400 instead of 500.

**Fix:** Catch `StripeAPIError` specifically.

---

## ISSUE 4 — SPEC DEVIATION: Auth errors return 502 instead of 500

**File:** `python-worker/app/stripe_service.py`

All `StripeError` subclasses (including `AuthenticationError`) return 502. The spec says auth errors should return 500. Fixed by Issue 1.

---

## ISSUE 5 — CODE QUALITY: Missing guard for None session_id

**File:** `python-worker/app/routes.py`, lines 125, 146

Both checkout webhook handlers call `event.data.object.get("id")` and pass directly to DB. None values silently match zero rows.

**Fix:** Guard with `if not session_id` before DB call.

---

## ISSUE 6 — CODE QUALITY: UPDATE without entity_id in WHERE

**File:** `python-worker/app/database.py`, line 53

```sql
UPDATE toro_core.stripe_checkout_sessions
SET status = $2, updated_at = NOW()
WHERE stripe_session_id = $1
```

No `entity_id` in WHERE clause. Acceptable given spec's "No RLS" decision, but noted for hardening.

---

## ISSUE 7 — NIT: Unused dependency `python-dotenv`

**File:** `python-worker/requirements.txt`, line 6

`python-dotenv` is listed but never imported. Remove it.

---

## What is correct and well done

- **Webhook signature verification**: `stripe.Webhook.construct_event` properly called before business logic.
- **Parameterized queries**: All SQL uses `$1, $2, ...` placeholders — zero SQL injection surface.
- **Dollar-to-cents**: `int(round(amount * 100))` avoids floating-point truncation.
- **Webhook idempotency**: Unknown events return 200 (Stripe won't retry). Missing user_id returns 200. Status updates are idempotent.
- **NATS error messages**: Global handler returns generic "Event broker unavailable" — no internal NATS details leak.
- **Docker/nginx**: Port mapping 8005:8000, env vars correctly wired, nginx location block proxies without conflicts.
- **Fail-fast startup**: Missing env vars crash immediately via `os.environ[...]`, Docker restarts.
- **Test coverage**: 115 tests covering validation, error propagation, idempotency, NATS failures — all passing.

---

## Summary

| Priority | # | File | Required Action |
|----------|---|------|-----------------|
| **CRITICAL** | 1 | `stripe_service.py` | Replace raw `str(e)` with generic messages; log raw errors server-side |
| LOW | 2 | `routes.py` | Log when expired session update affects zero rows |
| LOW | 3 | `routes.py` | Replace bare `except Exception` with `except StripeAPIError` |
| LOW | 4 | `stripe_service.py` | Return 500 for auth errors (fixed by #1) |
| LOW | 5 | `routes.py` | Guard against None session_id from Stripe event data |
| LOW | 6 | `database.py` | Add entity_id to UPDATE WHERE clause (future hardening) |
| NIT | 7 | `requirements.txt` | Remove unused `python-dotenv` |

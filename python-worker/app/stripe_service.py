import logging

import stripe

from app.config import STRIPE_SECRET_KEY
from app.errors import StripeAPIError

stripe_client = stripe.StripeClient(STRIPE_SECRET_KEY)
logger = logging.getLogger(__name__)


async def create_checkout_session(
    amount_cents: int,
    product_name: str,
    return_url: str,
    user_id: str,
    customer_email: str | None = None,
) -> dict:
    try:
        params = {
            "mode": "payment",
            "ui_mode": "embedded_page",
            "line_items": [{
                "price_data": {
                    "currency": "usd",
                    "product_data": {"name": product_name},
                    "unit_amount": amount_cents,
                },
                "quantity": 1,
            }],
            "metadata": {"user_id": user_id},
        }
        
        if return_url:
            params["return_url"] = return_url
        else:
            params["redirect_on_completion"] = "never"

        if customer_email:
            params["customer_email"] = customer_email

        session = await stripe_client.v1.checkout.sessions.create_async(
            params=params,
        )
        return {"id": session.id, "client_secret": session.client_secret}
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def get_or_create_customer(user_id: str) -> str:
    try:
        customers = await stripe_client.v1.customers.search_async(
            params={
                "query": f"metadata['user_id']:'{user_id}'",
                "limit": 1,
            }
        )
        if customers.data:
            return customers.data[0].id

        customer = await stripe_client.v1.customers.create_async(
            params={"metadata": {"user_id": user_id}}
        )
        return customer.id
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error creating/finding customer", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def create_payment_sheet_intent(amount_cents: int, user_id: str) -> dict:
    try:
        customer_id = await get_or_create_customer(user_id)
        ephemeral_key = await stripe_client.v1.ephemeral_keys.create_async(
            params={"customer": customer_id},
            options={"stripe_version": "2023-10-16"},
        )
        intent = await stripe_client.v1.payment_intents.create_async(
            params={
                "amount": amount_cents,
                "currency": "usd",
                "customer": customer_id,
                "automatic_payment_methods": {"enabled": True},
                "metadata": {"user_id": user_id},
            }
        )
        return {
            "clientSecret": intent.client_secret,
            "ephemeralKey": ephemeral_key.secret,
            "customer": customer_id,
        }
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def create_fc_session(user_id: str) -> dict:
    try:
        stripe_customer_id = await get_or_create_customer(user_id)
        session = await stripe_client.v1.financial_connections.sessions.create_async(
            params={
                "account_holder": {
                    "type": "customer",
                    "customer": stripe_customer_id,
                },
                "permissions": ["balances", "ownership", "payment_method", "transactions"],
            },
        )
        return {"id": session.id, "client_secret": session.client_secret, "stripe_customer_id": stripe_customer_id}
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def retrieve_fc_session(session_id: str) -> dict:
    try:
        session = await stripe_client.v1.financial_connections.sessions.retrieve_async(
            session_id,
        )
        return {
            "id": session.id,
            "status": getattr(session, "status", None),
            "accounts": {"data": session.accounts.data if session.accounts else []},
        }
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def retrieve_fc_account(account_id: str) -> dict:
    try:
        account = await stripe_client.v1.financial_connections.accounts.retrieve_async(
            account_id,
            params={"expand": ["balance", "ownership"]},
        )
        institution_name = account.institution_name or ""
        subcategory = str(account.subcategory) if account.subcategory else None
        last4 = account.last4 if account.last4 else None

        result: dict = {
            "id": account.id,
            "status": str(account.status),
            "institution_name": institution_name,
            "subcategory": subcategory,
            "livemode": account.livemode,
            "created": account.created,
        }

        if account.balance:
            current_balances = account.balance.current
            if current_balances:
                for curr, val in current_balances.items():
                    result["currency"] = curr
                    result["current_balance"] = float(val) / 100.0
                    break
            cash = account.balance.cash
            if cash and cash.available:
                for _, val in cash.available.items():
                    result["current_balance_cash"] = float(val) / 100.0
                    break
            credit = account.balance.credit
            if credit and credit.used:
                for _, val in credit.used.items():
                    result["current_balance_credit"] = float(val) / 100.0
                    break

        account_holder = account.account_holder
        if account_holder and account_holder.name:
            result["account_holder_name"] = account_holder.name

        if last4:
            result["last4"] = last4

        supported_types = account.supported_payment_method_types
        if supported_types:
            result["supported_networks"] = [str(t) for t in supported_types]

        return result
    except stripe.AuthenticationError as e:
        raise StripeAPIError("Stripe authentication failed", status_code=500) from e
    except stripe.StripeError as e:
        logger.error("Stripe API error", extra={"error": str(e), "type": type(e).__name__})
        raise StripeAPIError("Stripe API error", status_code=502) from e


async def verify_webhook_signature(
    raw_body: bytes,
    signature_header: str,
    webhook_secret: str,
) -> stripe.Event:
    try:
        event = stripe.Webhook.construct_event(
            raw_body, signature_header, webhook_secret
        )
        return event
    except stripe.SignatureVerificationError as e:
        raise StripeAPIError("Invalid signature", status_code=400) from e

import logging

import asyncpg
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import HTMLResponse

from app import database, nats_client, stripe_service
from app.config import STRIPE_PUBLISHABLE_KEY, STRIPE_WEBHOOK_SECRET
from app.errors import StripeAPIError
from app.models import (
    BankAccountResponse,
    CheckoutSessionResponse,
    CreateCheckoutSessionRequest,
    CreateFCSessionRequest,
    CreatePaymentSheetRequest,
    FCCallbackRequest,
    FCSessionResponse,
    PaymentSheetResponse,
    SyncFCSessionRequest,
)

router = APIRouter(prefix="/v1")
logger = logging.getLogger(__name__)


@router.post("/payments/checkout/session", response_model=CheckoutSessionResponse)
async def create_checkout_session(req: CreateCheckoutSessionRequest) -> CheckoutSessionResponse:
    amount_cents = int(round(req.amount_in_dollars * 100))
    session = await stripe_service.create_checkout_session(
        amount_cents=amount_cents,
        product_name=req.product_name,
        return_url=str(req.return_url),
        user_id=req.user_id,
        customer_email=req.customer_email,
    )
    
    entity_id = await database.get_entity_id_for_user(req.user_id)
    
    try:
        await database.insert_checkout_session(
            entity_id=entity_id,
            stripe_session_id=session["id"],
            amount_cents=amount_cents,
            product_name=req.product_name,
            metadata={"user_id": req.user_id},
        )
    except asyncpg.UniqueViolationError:
        raise HTTPException(status_code=409, detail="Checkout session already exists")

    return CheckoutSessionResponse(client_secret=session["client_secret"])


@router.post("/payments/payment-sheet", response_model=PaymentSheetResponse)
async def create_payment_sheet(req: CreatePaymentSheetRequest) -> PaymentSheetResponse:
    amount_cents = int(round(req.amount_in_dollars * 100))
    result = await stripe_service.create_payment_sheet_intent(
        amount_cents=amount_cents,
        user_id=req.user_id,
    )
    return PaymentSheetResponse(
        clientSecret=result["clientSecret"],
        ephemeralKey=result["ephemeralKey"],
        customer=result["customer"],
    )


@router.post("/financial-connections/session", response_model=FCSessionResponse)
async def create_fc_session(req: CreateFCSessionRequest) -> FCSessionResponse:
    session = await stripe_service.create_fc_session(
        user_id=req.user_id,
    )
    
    entity_id = await database.get_entity_id_for_user(req.user_id)
    
    try:
        await database.insert_fc_attempt(
            entity_id=entity_id,
            fc_session_id=session["id"],
            stripe_customer_id=session["stripe_customer_id"],
        )
    except asyncpg.UniqueViolationError:
        raise HTTPException(status_code=409, detail="Financial Connections session already exists")

    return FCSessionResponse(
        client_secret=session["client_secret"],
        publishable_key=STRIPE_PUBLISHABLE_KEY,
    )


@router.get("/financial-connections/auth-page", response_class=HTMLResponse)
async def get_fc_auth_page(user_id: str):
    session = await stripe_service.create_fc_session(user_id=user_id)
    entity_id = await database.get_entity_id_for_user(user_id)
    
    try:
        await database.insert_fc_attempt(
            entity_id=entity_id,
            fc_session_id=session["id"],
            stripe_customer_id=session["stripe_customer_id"],
        )
    except asyncpg.UniqueViolationError:
        pass
        
    client_secret = session["client_secret"]
    publishable_key = STRIPE_PUBLISHABLE_KEY
    session_id = session["id"]

    html_content = f"""
    <!DOCTYPE html>
    <html>
    <head>
        <title>Connect your bank</title>
        <script src="https://js.stripe.com/v3/"></script>
        <style>
            body {{ font-family: sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; background: #f9fafb; margin: 0; }}
            .loader {{ border: 4px solid #f3f3f3; border-top: 4px solid #3498db; border-radius: 50%; width: 40px; height: 40px; animation: spin 1s linear infinite; }}
            @keyframes spin {{ 0% {{ transform: rotate(0deg); }} 100% {{ transform: rotate(360deg); }} }}
        </style>
    </head>
    <body>
        <div class="loader"></div>
        <script>
            const stripe = Stripe('{publishable_key}');
            
            stripe.collectFinancialConnectionsAccounts({{
                clientSecret: '{client_secret}'
            }}).then(function(result) {{
                if (result.error) {{
                    document.body.innerHTML = '<h3>Error connecting account: ' + result.error.message + '</h3>';
                }} else {{
                    document.body.innerHTML = '<h3>Successfully connected! You can close this window.</h3>';
                    fetch('callback', {{
                        method: 'POST',
                        headers: {{ 'Content-Type': 'application/json' }},
                        body: JSON.stringify({{ session_id: '{session_id}', user_id: '{user_id}' }})
                    }}).catch(console.error);
                }}
            }}).catch(function(err) {{
                document.body.innerHTML = '<h3>An unexpected error occurred.</h3>';
                console.error(err);
            }});
        </script>
    </body>
    </html>
    """
    return HTMLResponse(content=html_content)


@router.post("/financial-connections/callback")
async def fc_callback(req: FCCallbackRequest):
    entity_id = await database.get_entity_id_for_user(req.user_id)
    try:
        await nats_client.publish(
            "stripe_fc_connected",
            {"session_id": req.session_id, "entity_id": entity_id}
        )
    except Exception:
        logger.exception("Failed to publish stripe_fc_connected event")
        raise HTTPException(status_code=500, detail="Event broker unavailable")
        
    return {"status": "success"}


@router.post("/financial-connections/sync", response_model=list[BankAccountResponse])
async def sync_fc_session(req: SyncFCSessionRequest) -> list[BankAccountResponse]:
    session = await stripe_service.retrieve_fc_session(req.session_id)
    accounts = session.get("accounts", {}).get("data", [])

    if not accounts:
        return []

    entity_id = await database.get_entity_id_for_user(req.user_id)

    results: list[BankAccountResponse] = []
    for account in accounts:
        institution_name = getattr(account, "institution_name", "")
        last4_val = getattr(account, "last4", None)
        last4_str = str(last4_val) if last4_val else None
        await database.upsert_linked_bank_account(
            entity_id=entity_id,
            fc_session_id=req.session_id,
            stripe_account_id=getattr(account, "id", ""),
            institution_name=institution_name,
            last4=last4_str,
            subcategory=getattr(account, "subcategory", None),
            status=getattr(account, "status", "unknown"),
        )
        results.append(BankAccountResponse(
            id=getattr(account, "id", ""),
            stripe_account_id=getattr(account, "id", ""),
            institution_name=institution_name,
            last4=last4_str,
            subcategory=getattr(account, "subcategory", None),
            status=getattr(account, "status", "unknown"),
        ))

    try:
        await nats_client.publish(
            "stripe.financial_connections.linked",
            {"session_id": req.session_id, "accounts": [a.model_dump() for a in results]},
        )
    except Exception:
        logger.exception("Failed to publish stripe.financial_connections.linked event")
        raise HTTPException(status_code=500, detail="Event broker unavailable")

    return results


@router.get("/financial-connections/accounts/{account_id}")
async def get_fc_account(account_id: str):
    account = await stripe_service.retrieve_fc_account(account_id)
    return account


@router.post("/webhooks/stripe")
async def stripe_webhook(request: Request):
    raw_body = await request.body()
    sig_header = request.headers.get("stripe-signature")
    if not sig_header:
        raise HTTPException(status_code=400, detail="Missing stripe-signature header")

    try:
        event = await stripe_service.verify_webhook_signature(
            raw_body=raw_body,
            signature_header=sig_header,
            webhook_secret=STRIPE_WEBHOOK_SECRET,
        )
    except StripeAPIError:
        raise HTTPException(status_code=400, detail="Invalid signature")

    event_type = event.type
    logger.info("Received webhook", extra={"event_type": event_type})

    if event_type == "checkout.session.completed":
        obj = event.data.object
        session_id = obj.get("id")
        if not session_id:
            logger.error("checkout.session.completed missing session ID")
            return {"status": "success"}
        user_id = obj.get("metadata", {}).get("user_id")
        if not user_id:
            logger.error("checkout.session.completed missing user_id in metadata")
            return {"status": "success"}
        await database.update_checkout_session_status(
            stripe_session_id=session_id,
            status="COMPLETED",
        )
        try:
            await nats_client.publish(
                "stripe.checkout.completed",
                {"session_id": session_id, "user_id": user_id, "event_type": event_type},
            )
        except Exception:
            logger.exception("Failed to publish stripe.checkout.completed event")
            raise HTTPException(status_code=500, detail="Event broker unavailable")

    elif event_type == "checkout.session.expired":
        session_id = event.data.object.get("id")
        if not session_id:
            logger.error("checkout.session.expired missing session ID")
            return {"status": "success"}
        await database.update_checkout_session_status(
            stripe_session_id=session_id,
            status="EXPIRED",
        )
        try:
            await nats_client.publish(
                "stripe.checkout.expired",
                {"session_id": session_id, "event_type": event_type},
            )
        except Exception:
            logger.exception("Failed to publish stripe.checkout.expired event")
            raise HTTPException(status_code=500, detail="Event broker unavailable")

    elif event_type == "financial_connections.session.updated":
        session_id = event.data.object.get("id")
        try:
            await nats_client.publish(
                "stripe.financial_connections.session_updated",
                {
                    "session_id": session_id,
                    "accounts": event.data.object.get("accounts", {}).get("data", []),
                    "status": event.data.object.get("status"),
                },
            )
        except Exception:
            logger.exception("Failed to publish stripe.financial_connections.session_updated event")
            raise HTTPException(status_code=500, detail="Event broker unavailable")

    elif event_type == "payment_intent.succeeded":
        obj = event.data.object
        intent_id = obj.get("id")
        user_id = obj.get("metadata", {}).get("user_id")
        if not user_id:
            logger.error("payment_intent.succeeded missing user_id in metadata")
            return {"status": "success"}

        try:
            await nats_client.publish(
                "stripe.payment_intent.succeeded",
                {"intent_id": intent_id, "user_id": user_id, "event_type": event_type},
            )
        except Exception:
            logger.exception("Failed to publish stripe.payment_intent.succeeded event")
            raise HTTPException(status_code=500, detail="Event broker unavailable")

    else:
        logger.warning("Unhandled webhook event type", extra={"event_type": event_type})

    return {"status": "success"}

"""Tests for FastAPI route handlers.

Each test mocks the underlying module-level functions (stripe_service, database,
nats_client) rather than spinning up a real server.

We test the handler functions directly rather than through HTTP, to keep tests
fast and deterministic.
"""
from unittest.mock import AsyncMock, MagicMock, patch

import asyncpg
import pytest
from fastapi import HTTPException

from app.errors import StripeAPIError
from app.models import (
    BankAccountResponse,
    CheckoutSessionResponse,
    CreateCheckoutSessionRequest,
    CreateFCSessionRequest,
    FCCallbackRequest,
    FCSessionResponse,
    SyncFCSessionRequest,
)
from app.routes import (
    STRIPE_PUBLISHABLE_KEY,
    create_checkout_session,
    create_fc_session,
    fc_callback,
    get_fc_auth_page,
    stripe_webhook,
    sync_fc_session,
)


# ===================================================================
# POST /v1/payments/checkout/session
# ===================================================================

class TestCreateCheckoutSession:
    @pytest.mark.asyncio
    async def test_creates_session_and_returns_client_secret(self):
        req = CreateCheckoutSessionRequest(
            user_id="user-1",
            amount_in_dollars=25.50,
            product_name="Premium Plan",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "cs_test_123", "client_secret": "cs_secret_123"}
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database") as mock_db:
                mock_db.insert_checkout_session = AsyncMock()
                result = await create_checkout_session(req)

        assert isinstance(result, CheckoutSessionResponse)
        assert result.client_secret == "cs_secret_123"

    @pytest.mark.asyncio
    async def test_converts_dollars_to_cents_correctly(self):
        # 25.50 --> round(2550.0) --> 2550
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=25.50,
            product_name="Test",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "cs_test", "client_secret": "cs_secret_test"}
        )
        mock_db = MagicMock()
        mock_db.insert_checkout_session = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                await create_checkout_session(req)

        mock_stripe.create_checkout_session.assert_awaited_once()
        call_kwargs = mock_stripe.create_checkout_session.call_args.kwargs
        assert call_kwargs["amount_cents"] == 2550

    @pytest.mark.asyncio
    async def test_converts_floating_point_dollars_safely(self):
        """0.10 should become 10, not 9 due to float truncation."""
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=0.10,
            product_name="Test",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "cs_10c", "client_secret": "cs_secret_10c"}
        )
        mock_db = MagicMock()
        mock_db.insert_checkout_session = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                await create_checkout_session(req)

        # 0.10 * 100 should round to 10
        assert mock_stripe.create_checkout_session.call_args.kwargs["amount_cents"] == 10

    @pytest.mark.asyncio
    async def test_inserts_pending_row_into_database(self):
        req = CreateCheckoutSessionRequest(
            user_id="user-42",
            amount_in_dollars=100.00,
            product_name="Annual Plan",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "cs_db_test", "client_secret": "cs_secret_db"}
        )
        mock_db = MagicMock()
        mock_db.insert_checkout_session = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                await create_checkout_session(req)

        mock_db.insert_checkout_session.assert_awaited_once_with(
            entity_id="user-42",
            stripe_session_id="cs_db_test",
            amount_cents=10000,
            product_name="Annual Plan",
            metadata={"user_id": "user-42"},
        )

    @pytest.mark.asyncio
    async def test_returns_409_on_duplicate_session_id(self):
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=10.00,
            product_name="Test",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "dup_session", "client_secret": "cs_secret_dup"}
        )
        mock_db = MagicMock()
        mock_db.insert_checkout_session = AsyncMock(
            side_effect=asyncpg.UniqueViolationError("duplicate key")
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with pytest.raises(HTTPException) as exc_info:
                    await create_checkout_session(req)

        assert exc_info.value.status_code == 409
        assert "Checkout session already exists" in exc_info.value.detail

    @pytest.mark.asyncio
    async def test_passes_return_url_to_stripe(self):
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=50.00,
            product_name="Pro Plan",
            return_url="https://mysite.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            return_value={"id": "cs_urls", "client_secret": "cs_secret_urls"}
        )
        mock_db = MagicMock()
        mock_db.insert_checkout_session = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                await create_checkout_session(req)

        call_kwargs = mock_stripe.create_checkout_session.call_args.kwargs
        assert call_kwargs["return_url"].rstrip("/") == "https://mysite.com/return"

    @pytest.mark.asyncio
    async def test_propagates_stripe_api_error(self):
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=10.00,
            product_name="Test",
            return_url="https://example.com/return",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_checkout_session = AsyncMock(
            side_effect=StripeAPIError("Invalid API key", status_code=502)
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with pytest.raises(StripeAPIError) as exc_info:
                await create_checkout_session(req)

        assert exc_info.value.status_code == 502
        assert "Invalid API key" in exc_info.value.message


# ===================================================================
# POST /v1/financial-connections/session
# ===================================================================

class TestCreateFCSession:
    @pytest.mark.asyncio
    async def test_creates_fc_session_and_returns_client_secret(self):
        req = CreateFCSessionRequest(
            user_id="user-1",
            stripe_customer_id="cus_test123",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_fc_session = AsyncMock(
            return_value={"id": "fcsess_abc", "client_secret": "fcsess_secret_abc"}
        )
        mock_db = MagicMock()
        mock_db.insert_fc_attempt = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                result = await create_fc_session(req)

        assert isinstance(result, FCSessionResponse)
        assert result.client_secret == "fcsess_secret_abc"
        assert result.publishable_key == STRIPE_PUBLISHABLE_KEY

    @pytest.mark.asyncio
    async def test_inserts_initiated_attempt_in_database(self):
        req = CreateFCSessionRequest(
            user_id="user-99",
            stripe_customer_id="cus_999",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_fc_session = AsyncMock(
            return_value={"id": "fcsess_init", "client_secret": "secret"}
        )
        mock_db = MagicMock()
        mock_db.insert_fc_attempt = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                await create_fc_session(req)

        mock_db.insert_fc_attempt.assert_awaited_once_with(
            entity_id="user-99",
            fc_session_id="fcsess_init",
            stripe_customer_id="cus_999",
        )

    @pytest.mark.asyncio
    async def test_returns_409_on_duplicate_session(self):
        req = CreateFCSessionRequest(
            user_id="u1",
            stripe_customer_id="cus_dup",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_fc_session = AsyncMock(
            return_value={"id": "fcsess_dup", "client_secret": "dup_secret"}
        )
        mock_db = MagicMock()
        mock_db.insert_fc_attempt = AsyncMock(
            side_effect=asyncpg.UniqueViolationError("duplicate")
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with pytest.raises(HTTPException) as exc_info:
                    await create_fc_session(req)

        assert exc_info.value.status_code == 409
        assert "Financial Connections session already exists" in exc_info.value.detail

    @pytest.mark.asyncio
    async def test_propagates_stripe_api_error(self):
        req = CreateFCSessionRequest(
            user_id="u1",
            stripe_customer_id="bad_cus",
        )

        mock_stripe = MagicMock()
        mock_stripe.create_fc_session = AsyncMock(
            side_effect=StripeAPIError("Customer not found", status_code=404)
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with pytest.raises(StripeAPIError) as exc_info:
                await create_fc_session(req)

        assert exc_info.value.status_code == 404


# ===================================================================
# GET /v1/financial-connections/auth-page
# ===================================================================

class TestGetFCAuthPage:
    @pytest.mark.asyncio
    async def test_returns_html_content(self):
        mock_stripe = MagicMock()
        mock_stripe.create_fc_session = AsyncMock(
            return_value={"id": "fcsess_html", "client_secret": "html_secret", "stripe_customer_id": "cus_html"}
        )
        mock_db = MagicMock()
        mock_db.get_entity_id_for_user = AsyncMock(return_value="entity-123")
        mock_db.insert_fc_attempt = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                response = await get_fc_auth_page("user-123")

        assert "Connect your bank" in response.body.decode()
        assert "html_secret" in response.body.decode()
        assert "user-123" in response.body.decode()
        assert "fcsess_html" in response.body.decode()


# ===================================================================
# POST /v1/financial-connections/callback
# ===================================================================

class TestFCCallback:
    @pytest.mark.asyncio
    async def test_callback_publishes_event(self):
        req = FCCallbackRequest(session_id="sess_123", user_id="user_123")
        
        mock_db = MagicMock()
        mock_db.get_entity_id_for_user = AsyncMock(return_value="entity-123")
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.database", mock_db):
            with patch("app.routes.nats_client", mock_nats):
                result = await fc_callback(req)

        assert result == {"status": "success"}
        mock_nats.publish.assert_awaited_once_with(
            "stripe_fc_connected",
            {"session_id": "sess_123", "entity_id": "entity-123"}
        )

    @pytest.mark.asyncio
    async def test_callback_returns_500_on_nats_failure(self):
        req = FCCallbackRequest(session_id="sess_123", user_id="user_123")
        
        mock_db = MagicMock()
        mock_db.get_entity_id_for_user = AsyncMock(return_value="entity-123")
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock(side_effect=Exception("NATS error"))

        with patch("app.routes.database", mock_db):
            with patch("app.routes.nats_client", mock_nats):
                with pytest.raises(HTTPException) as exc_info:
                    await fc_callback(req)

        assert exc_info.value.status_code == 500


# ===================================================================
# POST /v1/financial-connections/sync
# ===================================================================

class TestSyncFCSession:
    @pytest.mark.asyncio
    async def test_syncs_accounts_and_returns_them(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_sync_1",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_sync_1",
            "accounts": {
                "data": [
                    {
                        "id": "fca_acc1",
                        "institution_name": "Chase",
                        "last4": "6789",
                        "subcategory": "checking",
                        "status": "active",
                    },
                    {
                        "id": "fca_acc2",
                        "institution_name": "Wells Fargo",
                        "last4": "1234",
                        "subcategory": "savings",
                        "status": "active",
                    },
                ]
            },
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    result = await sync_fc_session(req)

        assert len(result) == 2
        assert result[0].institution_name == "Chase"
        assert result[0].last4 == "6789"
        assert result[1].institution_name == "Wells Fargo"
        assert mock_db.upsert_linked_bank_account.call_count == 2

    @pytest.mark.asyncio
    async def test_publishes_nats_event_after_sync(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_pub",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_pub",
            "accounts": {
                "data": [
                    {
                        "id": "fca_one",
                        "institution_name": "Chase",
                        "last4": "0000",
                        "subcategory": "checking",
                        "status": "active",
                    },
                ]
            },
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    await sync_fc_session(req)

        mock_nats.publish.assert_awaited_once()
        call_args = mock_nats.publish.call_args
        assert call_args.args[0] == "stripe.financial_connections.linked"
        payload = call_args.args[1]
        assert payload["session_id"] == "fcsess_pub"
        assert len(payload["accounts"]) == 1

    @pytest.mark.asyncio
    async def test_returns_empty_list_when_no_accounts(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_empty",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_empty",
            "accounts": {"data": []},
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    result = await sync_fc_session(req)

        assert result == []
        mock_db.upsert_linked_bank_account.assert_not_called()
        mock_nats.publish.assert_not_called()

    @pytest.mark.asyncio
    async def test_returns_empty_list_when_no_data_key(self):
        """Session without accounts key should return empty list."""
        req = SyncFCSessionRequest(
            session_id="fcsess_nodata",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_nodata",
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    result = await sync_fc_session(req)

        assert result == []

    @pytest.mark.asyncio
    async def test_handles_nats_publish_failure(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_natsfail",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_natsfail",
            "accounts": {
                "data": [
                    {
                        "id": "fca_err",
                        "institution_name": "Chase",
                        "last4": "1111",
                        "subcategory": "checking",
                        "status": "active",
                    },
                ]
            },
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock(side_effect=Exception("NATS down"))

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    with pytest.raises(HTTPException) as exc_info:
                        await sync_fc_session(req)

        assert exc_info.value.status_code == 500
        assert "Event broker unavailable" in exc_info.value.detail

    @pytest.mark.asyncio
    async def test_handles_missing_optional_account_fields(self):
        """Accounts may lack last4, subcategory, institution_name."""
        req = SyncFCSessionRequest(
            session_id="fcsess_minimal",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(return_value={
            "id": "fcsess_minimal",
            "accounts": {
                "data": [
                    {
                        "id": "fca_min",
                        "status": "active",
                    }
                ]
            },
        })
        mock_db = MagicMock()
        mock_db.upsert_linked_bank_account = AsyncMock()
        mock_nats = MagicMock()
        mock_nats.publish = AsyncMock()

        with patch("app.routes.stripe_service", mock_stripe):
            with patch("app.routes.database", mock_db):
                with patch("app.routes.nats_client", mock_nats):
                    result = await sync_fc_session(req)

        assert len(result) == 1
        assert result[0].institution_name == ""

    @pytest.mark.asyncio
    async def test_handles_stripe_session_not_found(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_404",
            user_id="user-1",
        )

        mock_stripe = MagicMock()
        mock_stripe.retrieve_fc_session = AsyncMock(
            side_effect=StripeAPIError("No such Financial Connection session", status_code=404)
        )

        with patch("app.routes.stripe_service", mock_stripe):
            with pytest.raises(StripeAPIError) as exc_info:
                await sync_fc_session(req)

        assert exc_info.value.status_code == 404


# ===================================================================
# POST /v1/webhooks/stripe — event routing
# ===================================================================

class TestStripeWebhookRouting:
    """Webhook routing logic: which events trigger which actions."""

    @pytest.mark.asyncio
    async def test_unknown_event_type_returns_success_ignored(self):
        """Unhandled event types log warning and return 200 so Stripe doesn't retry."""
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b'{"type":"unknown.event"}')
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "account.updated"

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                with patch("app.routes.nats_client") as mock_nats:
                    result = await stripe_webhook(mock_request)

        assert result == {"status": "success"}
        mock_db.update_checkout_session_status.assert_not_called()
        mock_nats.publish.assert_not_called()

    @pytest.mark.asyncio
    async def test_checkout_completed_updates_status_and_publishes(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b'{"type":"checkout.session.completed"}')
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "checkout.session.completed"
        mock_event.data.object = {
            "id": "cs_completed_1",
            "metadata": {"user_id": "user-100"},
        }

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                mock_db.update_checkout_session_status = AsyncMock()
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock()
                    result = await stripe_webhook(mock_request)

        assert result == {"status": "success"}
        mock_db.update_checkout_session_status.assert_awaited_once_with(
            stripe_session_id="cs_completed_1",
            status="COMPLETED",
        )
        mock_nats.publish.assert_awaited_once_with(
            "stripe.checkout.completed",
            {"session_id": "cs_completed_1", "user_id": "user-100", "event_type": "checkout.session.completed"},
        )

    @pytest.mark.asyncio
    async def test_checkout_completed_missing_user_id_still_returns_200(self):
        """If metadata is missing user_id, log error but return 200 so Stripe doesn't retry."""
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "checkout.session.completed"
        mock_event.data.object = {
            "id": "cs_no_user",
            "metadata": {},  # No user_id
        }

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                mock_db.update_checkout_session_status = AsyncMock()
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock()
                    result = await stripe_webhook(mock_request)

        assert result == {"status": "success"}
        # Should NOT update DB or publish NATS when user_id is missing
        mock_db.update_checkout_session_status.assert_not_called()
        mock_nats.publish.assert_not_called()

    @pytest.mark.asyncio
    async def test_checkout_completed_nats_failure_returns_500(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "checkout.session.completed"
        mock_event.data.object = {
            "id": "cs_natsfail",
            "metadata": {"user_id": "user-1"},
        }

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                mock_db.update_checkout_session_status = AsyncMock()
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock(side_effect=Exception("NATS gone"))
                    with pytest.raises(HTTPException) as exc_info:
                        await stripe_webhook(mock_request)

        assert exc_info.value.status_code == 500
        assert "Event broker unavailable" in exc_info.value.detail

    @pytest.mark.asyncio
    async def test_checkout_expired_updates_status_and_publishes(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "checkout.session.expired"
        mock_event.data.object = {"id": "cs_expired_1"}

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                mock_db.update_checkout_session_status = AsyncMock()
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock()
                    result = await stripe_webhook(mock_request)

        assert result == {"status": "success"}
        mock_db.update_checkout_session_status.assert_awaited_once_with(
            stripe_session_id="cs_expired_1",
            status="EXPIRED",
        )
        mock_nats.publish.assert_awaited_once_with(
            "stripe.checkout.expired",
            {"session_id": "cs_expired_1", "event_type": "checkout.session.expired"},
        )

    @pytest.mark.asyncio
    async def test_checkout_expired_nats_failure_returns_500(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "checkout.session.expired"
        mock_event.data.object = {"id": "cs_exp_natsfail"}

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                mock_db.update_checkout_session_status = AsyncMock()
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock(side_effect=Exception("NATS down"))
                    with pytest.raises(HTTPException) as exc_info:
                        await stripe_webhook(mock_request)

        assert exc_info.value.status_code == 500

    @pytest.mark.asyncio
    async def test_fc_session_updated_publishes_event(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "financial_connections.session.updated"
        mock_event.data.object = {
            "id": "fcsess_updated",
            "status": "active",
            "accounts": {
                "data": [
                    {"id": "fca_1", "institution_name": "Chase"},
                    {"id": "fca_2", "institution_name": "BofA"},
                ],
            },
        }

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock()
                    result = await stripe_webhook(mock_request)

        assert result == {"status": "success"}
        mock_nats.publish.assert_awaited_once()
        payload = mock_nats.publish.call_args.args[1]
        assert payload["session_id"] == "fcsess_updated"
        assert payload["status"] == "active"
        assert len(payload["accounts"]) == 2

    @pytest.mark.asyncio
    async def test_fc_session_updated_nats_failure_returns_500(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=123,v1=sig"}

        mock_event = MagicMock()
        mock_event.type = "financial_connections.session.updated"
        mock_event.data.object = {"id": "fcsess_natsfail", "status": "active", "accounts": {"data": []}}

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(return_value=mock_event)
            with patch("app.routes.database") as mock_db:
                with patch("app.routes.nats_client") as mock_nats:
                    mock_nats.publish = AsyncMock(side_effect=Exception("NATS down"))
                    with pytest.raises(HTTPException) as exc_info:
                        await stripe_webhook(mock_request)

        assert exc_info.value.status_code == 500

    @pytest.mark.asyncio
    async def test_missing_signature_header_returns_400(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {}  # No stripe-signature

        with pytest.raises(HTTPException) as exc_info:
            await stripe_webhook(mock_request)

        assert exc_info.value.status_code == 400
        assert "Missing stripe-signature header" in exc_info.value.detail

    @pytest.mark.asyncio
    async def test_invalid_signature_returns_400(self):
        mock_request = MagicMock()
        mock_request.body = AsyncMock(return_value=b"{}")
        mock_request.headers = {"stripe-signature": "t=bad,v1=bad"}

        with patch("app.routes.stripe_service") as mock_stripe:
            mock_stripe.verify_webhook_signature = AsyncMock(
                side_effect=StripeAPIError("Invalid signature", status_code=400)
            )
            with pytest.raises(HTTPException) as exc_info:
                await stripe_webhook(mock_request)

        assert exc_info.value.status_code == 400
        assert "Invalid signature" in exc_info.value.detail

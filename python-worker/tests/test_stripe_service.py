"""Tests for stripe_service module functions.

Uses unittest.mock AsyncMock for native async Stripe SDK v11+ methods.
"""
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import stripe

from app.errors import StripeAPIError
from app.stripe_service import (
    create_checkout_session,
    create_fc_session,
    retrieve_fc_session,
    retrieve_fc_account,
    verify_webhook_signature,
)


# ---------------------------------------------------------------------------
# create_checkout_session
# ---------------------------------------------------------------------------

class TestCreateCheckoutSession:
    @pytest.mark.asyncio
    async def test_creates_session_and_returns_id_and_client_secret(self):
        mock_client = MagicMock()
        mock_session = MagicMock()
        mock_session.id = "cs_test_123"
        mock_session.client_secret = "cs_secret_123"
        mock_client.v1.checkout.sessions.create_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await create_checkout_session(
                amount_cents=2500,
                product_name="Premium",
                return_url="https://example.com/return",
                user_id="user-1",
            )

        assert result == {"id": "cs_test_123", "client_secret": "cs_secret_123"}

    @pytest.mark.asyncio
    async def test_passes_correct_params_to_stripe(self):
        mock_client = MagicMock()
        mock_session = MagicMock()
        mock_session.id = "cs_test_456"
        mock_session.client_secret = "cs_secret_456"
        mock_client.v1.checkout.sessions.create_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            await create_checkout_session(
                amount_cents=5000,
                product_name="Deluxe Plan",
                return_url="https://example.com/return",
                user_id="user-99",
            )

        call_kwargs = mock_client.v1.checkout.sessions.create_async.call_args.kwargs
        params = call_kwargs.get("params", {})
        assert params["mode"] == "payment"
        assert params["ui_mode"] == "form"
        assert params["return_url"] == "https://example.com/return"
        assert params["line_items"][0]["price_data"]["unit_amount"] == 5000
        assert params["line_items"][0]["price_data"]["product_data"]["name"] == "Deluxe Plan"
        assert params["metadata"]["user_id"] == "user-99"

    @pytest.mark.asyncio
    async def test_wraps_stripe_error_as_stripe_api_error(self):
        mock_client = MagicMock()
        mock_client.v1.checkout.sessions.create_async = AsyncMock(
            side_effect=stripe.StripeError("API key invalid")
        )

        with patch("app.stripe_service.stripe_client", mock_client):
            with pytest.raises(StripeAPIError) as exc_info:
                await create_checkout_session(
                    amount_cents=1000,
                    product_name="Test",
                    return_url="https://example.com/return",
                    user_id="u1",
                )
            assert exc_info.value.status_code == 502
            assert "Stripe API error" in exc_info.value.message

    @pytest.mark.asyncio
    async def test_converts_amount_cents_correctly(self):
        """1500 cents should be passed as unit_amount=1500."""
        mock_client = MagicMock()
        mock_session = MagicMock()
        mock_session.id = "cs_amt"
        mock_session.client_secret = "cs_secret_amt"
        mock_client.v1.checkout.sessions.create_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            await create_checkout_session(
                amount_cents=1500,
                product_name="Test",
                return_url="https://example.com/return",
                user_id="u1",
            )

        call_kwargs = mock_client.v1.checkout.sessions.create_async.call_args.kwargs
        params_from_call = call_kwargs.get("params", {})
        assert params_from_call["line_items"][0]["price_data"]["unit_amount"] == 1500


# ---------------------------------------------------------------------------
# create_fc_session
# ---------------------------------------------------------------------------

class TestCreateFCSession:
    @pytest.mark.asyncio
    async def test_creates_session_and_returns_id_and_client_secret(self):
        mock_client = MagicMock()
        mock_session = MagicMock()
        mock_session.id = "fcsess_test_abc"
        mock_session.client_secret = "fcsess_secret_abc"
        mock_client.v1.financial_connections.sessions.create_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await create_fc_session(stripe_customer_id="cus_123")

        assert result == {"id": "fcsess_test_abc", "client_secret": "fcsess_secret_abc"}

    @pytest.mark.asyncio
    async def test_passes_hardcoded_permissions(self):
        mock_client = MagicMock()
        mock_session = MagicMock()
        mock_session.id = "fcsess_perm"
        mock_session.client_secret = "secret"
        mock_client.v1.financial_connections.sessions.create_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            await create_fc_session(stripe_customer_id="cus_456")

        call_kwargs = mock_client.v1.financial_connections.sessions.create_async.call_args.kwargs
        params = call_kwargs.get("params", {})
        assert params["account_holder"]["type"] == "customer"
        assert params["account_holder"]["customer"] == "cus_456"
        assert set(params["permissions"]) == {"balances", "ownership", "payment_method", "transactions"}

    @pytest.mark.asyncio
    async def test_wraps_stripe_error(self):
        mock_client = MagicMock()
        mock_client.v1.financial_connections.sessions.create_async = AsyncMock(
            side_effect=stripe.StripeError("Invalid customer")
        )

        with patch("app.stripe_service.stripe_client", mock_client):
            with pytest.raises(StripeAPIError) as exc_info:
                await create_fc_session(stripe_customer_id="bad_customer")
            assert exc_info.value.status_code == 502


# ---------------------------------------------------------------------------
# retrieve_fc_session
# ---------------------------------------------------------------------------

class TestRetrieveFCSession:
    @pytest.mark.asyncio
    async def test_retrieves_session_with_accounts(self):
        mock_account_1 = MagicMock()
        mock_account_1.id = "fca_1"
        mock_account_1.institution_name = "Chase"
        mock_account_1.last4 = "6789"
        mock_account_1.subcategory = "checking"
        mock_account_1.status = "active"
        mock_account_2 = MagicMock()
        mock_account_2.id = "fca_2"
        mock_account_2.institution_name = "Bank of America"
        mock_account_2.last4 = "1234"
        mock_account_2.subcategory = "savings"
        mock_account_2.status = "active"

        mock_accounts = MagicMock()
        mock_accounts.data = [mock_account_1, mock_account_2]

        mock_session = MagicMock()
        mock_session.id = "fcsess_xyz"
        mock_session.status = "active"
        mock_session.accounts = mock_accounts

        mock_client = MagicMock()
        mock_client.v1.financial_connections.sessions.retrieve_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await retrieve_fc_session("fcsess_xyz")

        assert result["id"] == "fcsess_xyz"
        assert result["status"] == "active"
        assert len(result["accounts"]["data"]) == 2
        assert result["accounts"]["data"][0].id == "fca_1"

    @pytest.mark.asyncio
    async def test_retrieves_session_with_no_accounts(self):
        mock_session = MagicMock()
        mock_session.id = "fcsess_empty"
        mock_session.status = "pending"
        mock_session.accounts = None

        mock_client = MagicMock()
        mock_client.v1.financial_connections.sessions.retrieve_async = AsyncMock(return_value=mock_session)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await retrieve_fc_session("fcsess_empty")

        assert result["id"] == "fcsess_empty"
        assert result["accounts"]["data"] == []

    @pytest.mark.asyncio
    async def test_wraps_stripe_error(self):
        mock_client = MagicMock()
        mock_client.v1.financial_connections.sessions.retrieve_async = AsyncMock(
            side_effect=stripe.StripeError("Not found")
        )

        with patch("app.stripe_service.stripe_client", mock_client):
            with pytest.raises(StripeAPIError) as exc_info:
                await retrieve_fc_session("nonexistent")
            assert exc_info.value.status_code == 502


# ---------------------------------------------------------------------------
# retrieve_fc_account
# ---------------------------------------------------------------------------

class TestRetrieveFCAccount:
    @pytest.mark.asyncio
    async def test_retrieves_account_with_balance_and_details(self):
        mock_account = MagicMock()
        mock_account.id = "fca_789"
        mock_account.status = "active"
        mock_account.institution_name = "Chase"
        mock_account.subcategory = "checking"
        mock_account.livemode = True
        mock_account.created = 1700000000
        mock_account.last4 = "4321"
        mock_account.supported_payment_method_types = ["us_bank_account", "link"]

        mock_balance = MagicMock()
        mock_balance.current = {"usd": 150000}  # amounts in cents
        mock_balance.cash = MagicMock()
        mock_balance.cash.available = {"usd": 50000}
        mock_balance.credit = MagicMock()
        mock_balance.credit.used = {"usd": 0}
        mock_account.balance = mock_balance

        mock_account_holder = MagicMock()
        mock_account_holder.name = "John Doe"
        mock_account.account_holder = mock_account_holder

        mock_client = MagicMock()
        mock_client.v1.financial_connections.accounts.retrieve_async = AsyncMock(return_value=mock_account)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await retrieve_fc_account("fca_789")

        assert result["id"] == "fca_789"
        assert result["status"] == "active"
        assert result["institution_name"] == "Chase"
        assert result["subcategory"] == "checking"
        assert result["current_balance"] == 1500.0
        assert result["current_balance_cash"] == 500.0
        assert result["current_balance_credit"] == 0.0
        assert result["account_holder_name"] == "John Doe"
        assert result["last4"] == "4321"
        assert result["supported_networks"] == ["us_bank_account", "link"]
        assert result["livemode"] is True

    @pytest.mark.asyncio
    async def test_retrieves_account_with_no_balance(self):
        mock_account = MagicMock()
        mock_account.id = "fca_minimal"
        mock_account.status = "pending"
        mock_account.institution_name = "Unknown Bank"
        mock_account.subcategory = None
        mock_account.livemode = False
        mock_account.created = 1700000000
        mock_account.last4 = None
        mock_account.supported_payment_method_types = None
        mock_account.balance = None
        mock_account.account_holder = None

        mock_client = MagicMock()
        mock_client.v1.financial_connections.accounts.retrieve_async = AsyncMock(return_value=mock_account)

        with patch("app.stripe_service.stripe_client", mock_client):
            result = await retrieve_fc_account("fca_minimal")

        assert result["id"] == "fca_minimal"
        assert result["status"] == "pending"
        assert result["institution_name"] == "Unknown Bank"
        assert result["subcategory"] is None
        assert "current_balance" not in result
        assert result.get("last4") is None

    @pytest.mark.asyncio
    async def test_wraps_stripe_error(self):
        mock_client = MagicMock()
        mock_client.v1.financial_connections.accounts.retrieve_async = AsyncMock(
            side_effect=stripe.StripeError("Account not found")
        )

        with patch("app.stripe_service.stripe_client", mock_client):
            with pytest.raises(StripeAPIError) as exc_info:
                await retrieve_fc_account("nonexistent")
            assert exc_info.value.status_code == 502


# ---------------------------------------------------------------------------
# verify_webhook_signature
# ---------------------------------------------------------------------------

class TestVerifyWebhookSignature:
    @pytest.mark.asyncio
    async def test_returns_event_on_valid_signature(self):
        mock_event = MagicMock(spec=stripe.Event)
        mock_event.type = "checkout.session.completed"

        with patch("app.stripe_service.stripe.Webhook.construct_event", return_value=mock_event):
            event = await verify_webhook_signature(
                raw_body=b'{"type": "checkout.session.completed"}',
                signature_header="t=12345,v1=sig",
                webhook_secret="whsec_test",
            )
            assert event is mock_event
            assert event.type == "checkout.session.completed"

    @pytest.mark.asyncio
    async def test_raises_stripe_api_error_on_invalid_signature(self):
        with patch(
            "app.stripe_service.stripe.Webhook.construct_event",
            side_effect=stripe.SignatureVerificationError("Invalid sig", "sig_header"),
        ):
            with pytest.raises(StripeAPIError) as exc_info:
                await verify_webhook_signature(
                    raw_body=b"bad payload",
                    signature_header="t=bad,sig=bad",
                    webhook_secret="whsec_wrong",
                )
            assert exc_info.value.status_code == 400
            assert "Invalid signature" in exc_info.value.message

    @pytest.mark.asyncio
    async def test_passes_correct_arguments_to_construct_event(self):
        mock_event = MagicMock(spec=stripe.Event)

        with patch("app.stripe_service.stripe.Webhook.construct_event") as mock_construct:
            mock_construct.return_value = mock_event
            await verify_webhook_signature(
                raw_body=b"payload",
                signature_header="hdr",
                webhook_secret="sec",
            )
            mock_construct.assert_called_once_with(b"payload", "hdr", "sec")

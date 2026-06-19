"""Tests for Pydantic request/response models."""

import pytest
from pydantic import ValidationError

from app.models import (
    BankAccountResponse,
    CheckoutSessionResponse,
    CreateCheckoutSessionRequest,
    CreateFCSessionRequest,
    FCCallbackRequest,
    FCSessionResponse,
    SyncFCSessionRequest,
    WebhookResponse,
)


class TestCreateCheckoutSessionRequest:
    def test_valid_request(self):
        req = CreateCheckoutSessionRequest(
            user_id="user-123",
            amount_in_dollars=25.50,
            product_name="Premium Plan",
            return_url="https://example.com/return",
        )
        assert req.user_id == "user-123"
        assert req.amount_in_dollars == 25.50
        assert req.product_name == "Premium Plan"
        assert str(req.return_url).rstrip("/") == "https://example.com/return"

    def test_amount_must_be_positive(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateCheckoutSessionRequest(
                user_id="user-123",
                amount_in_dollars=0.0,
                product_name="Test",
                return_url="https://example.com/return",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("amount_in_dollars",) for e in errors)

    def test_amount_must_be_positive_negative_value(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateCheckoutSessionRequest(
                user_id="user-123",
                amount_in_dollars=-10.0,
                product_name="Test",
                return_url="https://example.com/return",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("amount_in_dollars",) for e in errors)

    def test_invalid_return_url(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateCheckoutSessionRequest(
                user_id="user-123",
                amount_in_dollars=10.0,
                product_name="Test",
                return_url="not-a-url",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("return_url",) for e in errors)

    def test_non_http_url_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateCheckoutSessionRequest(
                user_id="user-123",
                amount_in_dollars=10.0,
                product_name="Test",
                return_url="ftp://example.com/return",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("return_url",) for e in errors)

    def test_missing_required_fields(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateCheckoutSessionRequest()
        assert len(exc_info.value.errors()) >= 4  # all 4 fields required

    def test_amount_as_float_conversion(self):
        """Amount as int should be coerced to float."""
        req = CreateCheckoutSessionRequest(
            user_id="u1",
            amount_in_dollars=100,
            product_name="Test",
            return_url="https://example.com/return",
        )
        assert isinstance(req.amount_in_dollars, float)
        assert req.amount_in_dollars == 100.0


class TestCreateFCSessionRequest:
    def test_valid_request(self):
        req = CreateFCSessionRequest(
            user_id="user-123",
            stripe_customer_id="cus_test123",
        )
        assert req.user_id == "user-123"
        assert req.stripe_customer_id == "cus_test123"

    def test_empty_stripe_customer_id(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateFCSessionRequest(
                user_id="user-123",
                stripe_customer_id="",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("stripe_customer_id",) for e in errors)

    def test_whitespace_only_customer_id(self):
        """Whitespace-only string has min_length=1 but is just spaces — should pass validation
        (Pydantic min_length only checks string length, not content)."""
        req = CreateFCSessionRequest(
            user_id="user-123",
            stripe_customer_id=" ",
        )
        assert req.stripe_customer_id == " "

    def test_missing_stripe_customer_id(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateFCSessionRequest(user_id="user-123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("stripe_customer_id",) for e in errors)

    def test_missing_user_id(self):
        with pytest.raises(ValidationError) as exc_info:
            CreateFCSessionRequest(stripe_customer_id="cus_123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("user_id",) for e in errors)


class TestSyncFCSessionRequest:
    def test_valid_request(self):
        req = SyncFCSessionRequest(
            session_id="fcsess_test123",
            user_id="user-123",
        )
        assert req.session_id == "fcsess_test123"
        assert req.user_id == "user-123"

    def test_empty_session_id(self):
        with pytest.raises(ValidationError) as exc_info:
            SyncFCSessionRequest(
                session_id="",
                user_id="user-123",
            )
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("session_id",) for e in errors)

    def test_missing_session_id(self):
        with pytest.raises(ValidationError) as exc_info:
            SyncFCSessionRequest(user_id="user-123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("session_id",) for e in errors)

    def test_missing_user_id(self):
        with pytest.raises(ValidationError) as exc_info:
            SyncFCSessionRequest(session_id="sess_123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("user_id",) for e in errors)


class TestFCCallbackRequest:
    def test_valid_request(self):
        req = FCCallbackRequest(
            session_id="fcsess_test123",
            user_id="user-123",
        )
        assert req.session_id == "fcsess_test123"
        assert req.user_id == "user-123"

    def test_missing_session_id(self):
        with pytest.raises(ValidationError) as exc_info:
            FCCallbackRequest(user_id="user-123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("session_id",) for e in errors)

    def test_missing_user_id(self):
        with pytest.raises(ValidationError) as exc_info:
            FCCallbackRequest(session_id="sess_123")
        errors = exc_info.value.errors()
        assert any(e["loc"] == ("user_id",) for e in errors)


class TestCheckoutSessionResponse:
    def test_valid_response(self):
        resp = CheckoutSessionResponse(client_secret="cs_secret_123")
        assert resp.client_secret == "cs_secret_123"

    def test_empty_secret_allowed(self):
        """Pydantic model does NOT validate the format here — it's just a string."""
        resp = CheckoutSessionResponse(client_secret="")
        assert resp.client_secret == ""


class TestFCSessionResponse:
    def test_valid_response(self):
        resp = FCSessionResponse(client_secret="fcsess_secret_123", publishable_key="pk_test_123")
        assert resp.client_secret == "fcsess_secret_123"
        assert resp.publishable_key == "pk_test_123"


class TestWebhookResponse:
    def test_valid_response(self):
        resp = WebhookResponse(status="success")
        assert resp.status == "success"


class TestBankAccountResponse:
    def test_valid_response(self):
        acct = BankAccountResponse(
            id="ba_123",
            stripe_account_id="fca_123",
            institution_name="Chase",
            last4="6789",
            subcategory="checking",
            status="active",
        )
        assert acct.id == "ba_123"
        assert acct.stripe_account_id == "fca_123"
        assert acct.institution_name == "Chase"
        assert acct.last4 == "6789"
        assert acct.subcategory == "checking"
        assert acct.status == "active"

    def test_nullable_fields(self):
        acct = BankAccountResponse(
            id="ba_123",
            stripe_account_id="fca_123",
            institution_name="Unknown Bank",
            last4=None,
            subcategory=None,
            status="active",
        )
        assert acct.last4 is None
        assert acct.subcategory is None

    def test_missing_required_fields(self):
        with pytest.raises(ValidationError):
            BankAccountResponse()

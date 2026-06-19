"""Tests for the FastAPI application in main.py.

Tests global error handlers and health endpoint. Error handlers are tested
directly (without HTTP), and the health function is tested directly to avoid
lifespan complexity.
"""
import json

import pytest
from fastapi import Request

from app.errors import NATSPublishError, StripeAPIError
from main import app, health, nats_error_handler, stripe_error_handler


class TestHealthEndpoint:
    """Test the health endpoint function directly."""

    @pytest.mark.asyncio
    async def test_health_returns_healthy(self):
        result = await health()
        assert result == {"status": "healthy"}


class TestStripeErrorHandler:
    """Test the StripeAPIError exception handler directly."""

    @pytest.mark.asyncio
    async def test_returns_json_with_status_and_code(self):
        exc = StripeAPIError("Stripe API timeout", status_code=502, code="STRIPE_ERROR")
        mock_request = Request(scope={"type": "http"})

        resp = await stripe_error_handler(mock_request, exc)
        assert resp.status_code == 502
        body = resp.body if isinstance(resp.body, bytes) else resp.body
        if isinstance(body, bytes):
            body = json.loads(body)
        assert body["detail"] == "Stripe API timeout"
        assert body["code"] == "STRIPE_ERROR"

    @pytest.mark.asyncio
    async def test_custom_status_code_preserved(self):
        exc = StripeAPIError("Not found", status_code=404, code="CUSTOM_ERROR")
        mock_request = Request(scope={"type": "http"})

        resp = await stripe_error_handler(mock_request, exc)
        assert resp.status_code == 404

    @pytest.mark.asyncio
    async def test_default_code_is_stripe_error(self):
        exc = StripeAPIError("Something failed")
        mock_request = Request(scope={"type": "http"})

        resp = await stripe_error_handler(mock_request, exc)
        body = resp.body if isinstance(resp.body, bytes) else resp.body
        if isinstance(body, bytes):
            body = json.loads(body)
        assert body["code"] == "STRIPE_ERROR"


class TestNATSErrorHandler:
    """Test the NATSPublishError exception handler directly."""

    @pytest.mark.asyncio
    async def test_always_returns_500(self):
        exc = NATSPublishError("NATS connection dropped")
        mock_request = Request(scope={"type": "http"})

        resp = await nats_error_handler(mock_request, exc)
        assert resp.status_code == 500

        body = resp.body if isinstance(resp.body, bytes) else resp.body
        if isinstance(body, bytes):
            body = json.loads(body)
        assert body["detail"] == "Event broker unavailable"
        assert body["code"] == "NATS_ERROR"

    @pytest.mark.asyncio
    async def test_provides_generic_message_not_raw_error(self):
        """The NATS error handler should NOT leak the raw error message."""
        exc = NATSPublishError(
            "Very detailed internal NATS error: connection refused 127.0.0.1:4222"
        )
        mock_request = Request(scope={"type": "http"})

        resp = await nats_error_handler(mock_request, exc)
        body = resp.body if isinstance(resp.body, bytes) else resp.body
        if isinstance(body, bytes):
            body = json.loads(body)
        # Raw message is not exposed to client
        assert body["detail"] == "Event broker unavailable"
        assert body["code"] == "NATS_ERROR"


class TestAppMetadata:
    """Verify the FastAPI app is configured correctly."""

    def test_app_title(self):
        assert app.title == "Stripe Integration Microservice"

    def test_app_version(self):
        assert app.version == "1.0.0"

    def test_router_mounted(self):
        """Verify the router is mounted at /v1."""
        routes = [r.path for r in app.routes]
        assert "/v1/payments/checkout/session" in routes
        assert "/v1/financial-connections/session" in routes
        assert "/v1/financial-connections/sync" in routes
        assert "/v1/webhooks/stripe" in routes
        assert "/health" in routes

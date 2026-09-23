import os
import pytest
from django.conf import settings
from django.test import RequestFactory
from config.urls import healthz_view


def test_healthz_endpoint():
    """Verify the /healthz and /ledger/healthz endpoints return 200 OK with plain text."""
    rf = RequestFactory()
    for path in ["/healthz", "/ledger/healthz"]:
        request = rf.get(path)
        response = healthz_view(request)
        assert response.status_code == 200
        assert response.content == b"OK"
        assert "text/plain" in response["Content-Type"]


def test_static_root_configured():
    """Verify STATIC_ROOT is configured and WhiteNoise middleware is loaded."""
    assert getattr(settings, "STATIC_ROOT", None) is not None
    assert str(settings.STATIC_ROOT).endswith("staticfiles")
    assert settings.STATIC_URL == "/static/"
    assert "whitenoise.middleware.WhiteNoiseMiddleware" in settings.MIDDLEWARE


def test_nats_url_configured():
    """Verify NATS_URL is present in Django settings."""
    assert hasattr(settings, "NATS_URL")
    assert settings.NATS_URL != ""


def test_nats_cluster_url_splitting():
    """Verify multi-node cluster NATS_URL strings split cleanly into server lists."""
    cluster_nats_url = "nats://nats-1:4222, nats://nats-2:4222,  nats://nats-3:4222 "
    servers = [s.strip() for s in cluster_nats_url.split(",") if s.strip()]
    assert servers == [
        "nats://nats-1:4222",
        "nats://nats-2:4222",
        "nats://nats-3:4222",
    ]


def test_allowed_hosts_parsing():
    """Verify comma-separated ALLOWED_HOSTS parsing logic."""
    raw = "api.usetoro.io, localhost, ledger, 127.0.0.1 "
    parsed = [h.strip() for h in raw.split(",") if h.strip()]
    assert parsed == ["api.usetoro.io", "localhost", "ledger", "127.0.0.1"]

"""Tests for custom error classes."""

import pytest

from app.errors import NATSPublishError, StripeAPIError


class TestStripeAPIError:
    def test_default_values(self):
        err = StripeAPIError("Something went wrong")
        assert err.message == "Something went wrong"
        assert err.status_code == 502
        assert err.code == "STRIPE_ERROR"
        assert str(err) == "Something went wrong"

    def test_custom_status_code(self):
        err = StripeAPIError("Not found", status_code=404)
        assert err.status_code == 404
        assert err.code == "STRIPE_ERROR"

    def test_custom_code(self):
        err = StripeAPIError("Invalid key", code="AUTH_ERROR")
        assert err.code == "AUTH_ERROR"
        assert err.status_code == 502

    def test_custom_all_fields(self):
        err = StripeAPIError("Bad request", status_code=400, code="INVALID_REQUEST")
        assert err.message == "Bad request"
        assert err.status_code == 400
        assert err.code == "INVALID_REQUEST"

    def test_is_exception(self):
        err = StripeAPIError("test")
        with pytest.raises(StripeAPIError) as exc_info:
            raise err
        assert exc_info.value is err

    def test_empty_message(self):
        err = StripeAPIError("")
        assert err.message == ""

    def test_can_be_caught_as_exception(self):
        with pytest.raises(Exception):
            raise StripeAPIError("test")
        # Should not reach here if the raise didn't work

    def test_from_cause(self):
        cause = ValueError("original error")
        err = StripeAPIError("wrapped", status_code=502)
        err.__cause__ = cause
        assert err.__cause__ is cause


class TestNATSPublishError:
    def test_basic_error(self):
        err = NATSPublishError("NATS is down")
        assert err.message == "NATS is down"
        assert str(err) == "NATS is down"

    def test_empty_message(self):
        err = NATSPublishError("")
        assert err.message == ""

    def test_is_exception(self):
        err = NATSPublishError("fail")
        with pytest.raises(NATSPublishError) as exc_info:
            raise err
        assert exc_info.value is err

    def test_can_be_caught_as_exception(self):
        with pytest.raises(Exception):
            raise NATSPublishError("test")

    def test_from_cause(self):
        cause = ConnectionError("no route to host")
        err = NATSPublishError("Failed to publish")
        err.__cause__ = cause
        assert err.__cause__ is cause

    def test_distinct_from_stripe_error(self):
        nat_err = NATSPublishError("nats down")
        stripe_err = StripeAPIError("stripe down")
        assert not isinstance(nat_err, StripeAPIError)
        assert not isinstance(stripe_err, NATSPublishError)

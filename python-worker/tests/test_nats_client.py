"""Tests for nats_client module functions.

Uses unittest.mock to avoid requiring a real NATS server.
"""
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from app.errors import NATSPublishError
from app.nats_client import close, connect, publish, subscribe_jetstream


class TestConnect:
    @pytest.mark.asyncio
    async def test_connects_with_split_servers(self):
        mock_nats = MagicMock()
        mock_nats.connect = AsyncMock()

        with patch("app.nats_client.nats.connect", mock_nats.connect):
            await connect("nats://host1:4222,nats://host2:4222,nats://host3:4222")

        mock_nats.connect.assert_awaited_once()
        call_args = mock_nats.connect.call_args
        servers = call_args.kwargs.get("servers") or call_args.args[0]
        assert servers == ["nats://host1:4222", "nats://host2:4222", "nats://host3:4222"]

    @pytest.mark.asyncio
    async def test_sets_client_name(self):
        mock_nats = MagicMock()
        mock_nats.connect = AsyncMock()

        with patch("app.nats_client.nats.connect", mock_nats.connect):
            await connect("nats://localhost:4222")

        call_kwargs = mock_nats.connect.call_args.kwargs
        assert call_kwargs.get("name") == "stripe-worker"

    @pytest.mark.asyncio
    async def test_single_server(self):
        mock_nats = MagicMock()
        mock_nats.connect = AsyncMock()

        with patch("app.nats_client.nats.connect", mock_nats.connect):
            await connect("nats://localhost:4222")

        call_args = mock_nats.connect.call_args
        servers = call_args.kwargs.get("servers") or call_args.args[0]
        assert servers == ["nats://localhost:4222"]

    @pytest.mark.asyncio
    async def test_retries_on_failure_and_succeeds(self):
        mock_connect = AsyncMock(side_effect=[Exception("Connection refused"), MagicMock()])

        with patch("app.nats_client.nats.connect", mock_connect):
            with patch("asyncio.sleep", new_callable=AsyncMock) as mock_sleep:
                await connect("nats://localhost:4222", max_retries=3, retry_delay=0.01)

        assert mock_connect.await_count == 2
        mock_sleep.assert_awaited_once_with(0.01)

    @pytest.mark.asyncio
    async def test_raises_after_max_retries_exceeded(self):
        mock_connect = AsyncMock(side_effect=Exception("Connection refused"))

        with patch("app.nats_client.nats.connect", mock_connect):
            with patch("asyncio.sleep", new_callable=AsyncMock):
                with pytest.raises(Exception, match="Connection refused"):
                    await connect("nats://localhost:4222", max_retries=3, retry_delay=0.01)

        assert mock_connect.await_count == 3


class TestClose:
    @pytest.mark.asyncio
    async def test_calls_drain_when_connected(self):
        mock_nc = MagicMock()
        mock_nc.drain = AsyncMock()

        with patch("app.nats_client._nc", mock_nc):
            await close()
            mock_nc.drain.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_noop_when_not_connected(self):
        with patch("app.nats_client._nc", None):
            # Should not raise
            await close()


class TestPublish:
    @pytest.mark.asyncio
    async def test_publishes_json_encoded_message(self):
        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock()

        with patch("app.nats_client._nc", mock_nc):
            with patch("app.nats_client.json.dumps", return_value='{"key":"value"}'):
                await publish("stripe.checkout.completed", {"session_id": "cs_1"})

        mock_nc.publish.assert_awaited_once_with("stripe.checkout.completed", b'{"key":"value"}')

    @pytest.mark.asyncio
    async def test_raises_nats_publish_error_when_not_connected(self):
        with patch("app.nats_client._nc", None):
            with pytest.raises(NATSPublishError, match="NATS not connected"):
                await publish("test.subject", {"data": "value"})

    @pytest.mark.asyncio
    async def test_raises_nats_publish_error_on_publish_failure(self):
        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock(side_effect=Exception("Connection closed"))

        with patch("app.nats_client._nc", mock_nc):
            with patch("app.nats_client.json.dumps", return_value='{"x":"y"}'):
                with pytest.raises(NATSPublishError) as exc_info:
                    await publish("stripe.event", {"x": "y"})
                assert "Failed to publish to stripe.event" in exc_info.value.message
                assert "Connection closed" in exc_info.value.message

    @pytest.mark.asyncio
    async def test_encodes_payload_as_bytes(self):
        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock()

        with patch("app.nats_client._nc", mock_nc):
            await publish("subject.test", {"key": "value"})

        call_args = mock_nc.publish.call_args
        data_arg = call_args.args[1] if len(call_args.args) > 1 else call_args.kwargs.get("payload")
        assert isinstance(data_arg, bytes)

    @pytest.mark.asyncio
    async def test_publishes_to_correct_subject(self):
        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock()

        with patch("app.nats_client._nc", mock_nc):
            await publish("stripe.financial_connections.linked", {"session_id": "fc_1"})

        call_args = mock_nc.publish.call_args
        subject = call_args.args[0] if call_args.args else call_args.kwargs.get("subject")
        assert subject == "stripe.financial_connections.linked"

    @pytest.mark.asyncio
    async def test_logs_on_failure(self):
        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock(side_effect=RuntimeError("boom"))
        mock_logger = MagicMock()

        with patch("app.nats_client._nc", mock_nc):
            with patch("app.nats_client.json.dumps", return_value="{}"):
                with patch("app.nats_client.logger", mock_logger):
                    with pytest.raises(NATSPublishError):
                        await publish("test", {})
                    mock_logger.error.assert_called_once()


class TestSubscribeJetstream:
    @pytest.mark.asyncio
    async def test_raises_error_when_not_connected(self):
        with patch("app.nats_client._nc", None):
            with pytest.raises(NATSPublishError, match="NATS not connected"):
                await subscribe_jetstream("test.subject", "test_durable", AsyncMock())

    @pytest.mark.asyncio
    async def test_subscribes_successfully(self):
        mock_nc = MagicMock()
        mock_js = MagicMock()
        mock_js.subscribe = AsyncMock()
        mock_nc.jetstream.return_value = mock_js

        cb = AsyncMock()
        with patch("app.nats_client._nc", mock_nc):
            await subscribe_jetstream("worker.inbox.test", "test_group", cb)

        mock_js.subscribe.assert_awaited_once_with(
            "worker.inbox.test",
            durable="test_group",
            cb=cb,
            manual_ack=True
        )

    @pytest.mark.asyncio
    async def test_retries_jetstream_subscription_on_failure(self):
        mock_nc = MagicMock()
        mock_js = MagicMock()
        mock_js.subscribe = AsyncMock(side_effect=[Exception("503 Leader not found"), MagicMock()])
        mock_nc.jetstream.return_value = mock_js

        cb = AsyncMock()
        with patch("app.nats_client._nc", mock_nc):
            with patch("asyncio.sleep", new_callable=AsyncMock) as mock_sleep:
                await subscribe_jetstream("worker.inbox.test", "test_group", cb, max_retries=3, retry_delay=0.01)

        assert mock_js.subscribe.await_count == 2
        mock_sleep.assert_awaited_once_with(0.01)

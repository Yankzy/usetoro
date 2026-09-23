"""
Unit tests for app.reconciliation_prod.routing_worker module.
"""
import json
from unittest.mock import AsyncMock, MagicMock, patch
import pytest

pytest_plugins = ("pytest_asyncio",)

from app.reconciliation_prod.routing_worker import (
    get_openai_client,
    handle_routing_request,
    handler,
    start_routing_worker,
)


class TestGetOpenAIClient:
    def test_creates_client(self):
        with patch.dict("os.environ", {"OPENAI_API_KEY": "sk-test-routing"}):
            with patch("app.reconciliation_prod.routing_worker._openai_client", None):
                client = get_openai_client()
                assert client is not None


class TestRoutingWorkerHandler:
    @pytest.mark.asyncio
    async def test_handler_success_and_publishes_result(self):
        msg = MagicMock()
        msg.subject = "worker.inbox.routing"
        msg.reply = None
        msg.headers = None
        msg.ack = AsyncMock()

        payload = {
            "book_items": [],
            "accounts_bank_items": {},
            "account_metadata": {},
            "evidence_text": "Sample evidence",
        }
        envelope = {
            "type": "REQUEST",
            "reply_subject": "routing.reply.test",
            "payload": json.dumps(payload),
        }
        msg.data = json.dumps(envelope).encode("utf-8")

        mock_routing_state = MagicMock()
        mock_routing_state.model_dump.return_value = {"routes": [], "status": "OPTIMAL"}

        mock_llm = MagicMock()

        with patch(
            "app.reconciliation_prod.routing_worker.run_routing_pipeline",
            new=AsyncMock(return_value=mock_routing_state),
        ) as mock_pipeline, patch(
            "app.reconciliation_prod.routing_worker.nats_client.publish",
            new=AsyncMock(),
        ) as mock_publish:
            await handler(msg, mock_llm)

            mock_pipeline.assert_awaited_once()
            mock_publish.assert_awaited_once_with(
                "routing.reply.test",
                {
                    "type": "INFORM",
                    "payload": json.dumps({"routes": [], "status": "OPTIMAL"}),
                },
            )
            msg.ack.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_handler_failure_publishes_error_envelope(self):
        msg = MagicMock()
        msg.subject = "worker.inbox.routing"
        msg.reply = "reply.failure.target"
        msg.headers = None
        msg.data = b"invalid-json-format"

        with patch(
            "app.reconciliation_prod.routing_worker.nats_client.publish",
            new=AsyncMock(),
        ) as mock_publish:
            await handler(msg, MagicMock())

            mock_publish.assert_awaited_once()
            args, _ = mock_publish.call_args
            assert args[0] == "reply.failure.target"
            assert args[1]["type"] == "FAILURE"
            assert "error" in json.loads(args[1]["payload"])

    @pytest.mark.asyncio
    async def test_handle_routing_request_delegates_to_handler(self):
        msg = MagicMock()
        with patch("app.reconciliation_prod.routing_worker.get_openai_client") as mock_get_llm, \
             patch("app.reconciliation_prod.routing_worker.handler", new=AsyncMock()) as mock_handler:
            mock_llm = MagicMock()
            mock_get_llm.return_value = mock_llm
            await handle_routing_request(msg)
            mock_handler.assert_awaited_once_with(msg, mock_llm)


class TestStartRoutingWorker:
    @pytest.mark.asyncio
    async def test_start_routing_worker_jetstream_success(self):
        nc = MagicMock()
        js = MagicMock()
        js.subscribe = AsyncMock()
        nc.jetstream.return_value = js
        llm = MagicMock()

        await start_routing_worker(nc, llm)

        nc.jetstream.assert_called_once()
        js.subscribe.assert_awaited_once()
        args, kwargs = js.subscribe.call_args
        assert args[0] == "worker.inbox.routing"
        assert kwargs["durable"] == "worker-inbox-routing-group"
        assert kwargs["manual_ack"] is True

    @pytest.mark.asyncio
    async def test_start_routing_worker_fallback_to_core_nats(self):
        nc = MagicMock()
        nc.jetstream.side_effect = RuntimeError("JetStream not available")
        nc.subscribe = AsyncMock()
        llm = MagicMock()

        await start_routing_worker(nc, llm)

        nc.subscribe.assert_awaited_once()
        args, kwargs = nc.subscribe.call_args
        assert args[0] == "worker.inbox.routing"

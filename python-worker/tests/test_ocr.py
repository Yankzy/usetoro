"""
Unit tests for app.openai.ocr module.
"""
import json
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from app.openai.ocr import (
    _publish_ocr_response,
    get_openai_client,
    handle_ocr_request,
    perform_ocr,
)


class TestGetOpenAIClient:
    def test_creates_client(self):
        with patch("app.openai.ocr.OPENAI_API_KEY", "sk-test-key"):
            with patch("app.openai.ocr._openai_client", None):
                client = get_openai_client()
                assert client is not None


class TestPerformOCR:
    @pytest.mark.asyncio
    async def test_perform_ocr_chat_completions_success(self):
        mock_response = MagicMock()
        mock_response.choices = [
            MagicMock(
                message=MagicMock(
                    content=json.dumps(
                        {
                            "doc_type": "invoice",
                            "confidence": 0.99,
                            "data": {"vendor_name": "Acme Corp", "total": 120.50},
                        }
                    )
                )
            )
        ]

        mock_client = MagicMock()
        mock_client.chat.completions.create = AsyncMock(return_value=mock_response)

        with patch("app.openai.ocr.get_openai_client", return_value=mock_client):
            result = await perform_ocr("https://example.com/receipt.jpg")

        assert result["doc_type"] == "invoice"
        assert result["confidence"] == 0.99
        assert result["data"]["vendor_name"] == "Acme Corp"
        mock_client.chat.completions.create.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_perform_ocr_fallback_responses_api(self):
        mock_client = MagicMock()
        mock_client.chat.completions.create = AsyncMock(side_effect=Exception("API unsupported"))
        
        mock_resp = MagicMock()
        mock_resp.output_text = json.dumps({"doc_type": "receipt", "confidence": 0.95})
        mock_client.responses.create = AsyncMock(return_value=mock_resp)

        with patch("app.openai.ocr.get_openai_client", return_value=mock_client):
            result = await perform_ocr("https://example.com/receipt.jpg")

        assert result["doc_type"] == "receipt"
        assert result["confidence"] == 0.95
        mock_client.responses.create.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_perform_ocr_non_json_response(self):
        mock_response = MagicMock()
        mock_response.choices = [
            MagicMock(message=MagicMock(content="Plain text OCR result"))
        ]

        mock_client = MagicMock()
        mock_client.chat.completions.create = AsyncMock(return_value=mock_response)

        with patch("app.openai.ocr.get_openai_client", return_value=mock_client):
            result = await perform_ocr("https://example.com/receipt.jpg")

        assert result["data"]["raw_text"] == "Plain text OCR result"
        assert result["doc_type"] == "other"



class TestHandleOCRRequest:
    @pytest.mark.asyncio
    async def test_handles_request_and_publishes_back(self):
        incoming_payload = {
            "session_id": "sess-123",
            "attachment_name": "receipt.jpg",
            "image_url": "https://example.com/receipt.jpg",
            "final_destination_subject": "worker.inbox.pcm",
        }
        msg = MagicMock()
        msg.subject = "worker.inbox.openai.ocr"
        msg.data = json.dumps(incoming_payload).encode("utf-8")
        msg.reply = "reply.subject.123"
        msg.ack = AsyncMock()

        ocr_result = {
            "doc_type": "receipt",
            "confidence": 0.98,
            "data": {"total": 50.0},
        }

        mock_nc = MagicMock()
        mock_nc.publish = AsyncMock()

        with patch("app.openai.ocr.perform_ocr", AsyncMock(return_value=ocr_result)):
            with patch("app.nats_client.publish", AsyncMock()) as mock_publish:
                with patch("app.nats_client._nc", mock_nc):
                    await handle_ocr_request(msg)

        msg.ack.assert_awaited_once()

        # Check published to final_destination_subject
        mock_publish.assert_awaited_once()
        published_subject, published_payload = mock_publish.call_args.args
        assert published_subject == "worker.inbox.pcm"
        assert published_payload["session_id"] == "sess-123"
        assert published_payload["attachment_name"] == "receipt.jpg"
        assert published_payload["status"] == "OCR_SUCCESS"
        assert published_payload["ocr_extraction"] == ocr_result

        # Check published to reply subject
        mock_nc.publish.assert_awaited_once()
        reply_subject, reply_bytes = mock_nc.publish.call_args.args
        assert reply_subject == "reply.subject.123"
        reply_json = json.loads(reply_bytes.decode())
        assert reply_json["status"] == "OCR_SUCCESS"

    @pytest.mark.asyncio
    async def test_missing_image_url_returns_error(self):
        incoming_payload = {
            "session_id": "sess-456",
            "final_destination_subject": "worker.inbox.pcm",
        }
        msg = MagicMock()
        msg.subject = "worker.inbox.openai.ocr"
        msg.data = json.dumps(incoming_payload).encode("utf-8")
        msg.reply = None
        msg.ack = AsyncMock()

        with patch("app.nats_client.publish", AsyncMock()) as mock_publish:
            await handle_ocr_request(msg)

        msg.ack.assert_awaited_once()
        mock_publish.assert_awaited_once()
        pub_subject, pub_payload = mock_publish.call_args.args
        assert pub_subject == "worker.inbox.pcm"
        assert pub_payload["status"] == "ERROR"
        assert "No image_url" in pub_payload["error"]

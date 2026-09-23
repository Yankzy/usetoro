import asyncio
from unittest.mock import AsyncMock, MagicMock, patch
import pytest
from django.core.checks.urls import check_url_config
import nats.js.errors

from ledger.management.commands.run_bookkeeping_session_worker import Command


def test_url_namespace_unique_no_w005_warning():
    """Verify that Django URL check does not emit urls.W005 warning for namespace 'ledger'."""
    all_warnings = check_url_config(None)
    w005_warnings = [w for w in all_warnings if w.id == "urls.W005" and "ledger" in str(w.msg)]
    assert len(w005_warnings) == 0, f"Expected 0 W005 warnings, got: {w005_warnings}"


@pytest.mark.asyncio
async def test_worker_ensures_stream_when_not_found():
    """Verify that _run_worker creates BOOKKEEPING_EVENTS stream when find_stream_name_by_subject raises NotFoundError."""
    cmd = Command()

    mock_nc = MagicMock()
    mock_js = MagicMock()
    mock_jsm = MagicMock()
    mock_sub = MagicMock()
    mock_sub.unsubscribe = AsyncMock()

    mock_nc.jetstream.return_value = mock_js
    mock_nc.jsm.return_value = mock_jsm
    mock_nc.drain = AsyncMock()

    # find_stream_name_by_subject raises NotFoundError initially
    mock_jsm.find_stream_name_by_subject = AsyncMock(side_effect=nats.js.errors.NotFoundError)
    mock_jsm.add_stream = AsyncMock()
    mock_js.subscribe = AsyncMock(return_value=mock_sub)

    with patch("ledger.management.commands.run_bookkeeping_session_worker.nats.connect", AsyncMock(return_value=mock_nc)):
        with patch("asyncio.Event.wait", AsyncMock()):  # Instant shutdown
            await cmd._run_worker(
                nats_url="nats://localhost:4222",
                subject="events.bookkeeping.trigger",
                queue_group="test_group",
            )

    mock_jsm.add_stream.assert_awaited_once_with(
        name="BOOKKEEPING_EVENTS",
        subjects=["events.bookkeeping.>"],
    )
    mock_js.subscribe.assert_awaited_once()


@pytest.mark.asyncio
async def test_worker_ensures_stream_with_custom_subject():
    """Verify that _run_worker includes both BOOKKEEPING_EVENTS default and custom subject when non-overlapping."""
    cmd = Command()

    mock_nc = MagicMock()
    mock_js = MagicMock()
    mock_jsm = MagicMock()
    mock_sub = MagicMock()
    mock_sub.unsubscribe = AsyncMock()

    mock_nc.jetstream.return_value = mock_js
    mock_nc.jsm.return_value = mock_jsm
    mock_nc.drain = AsyncMock()

    mock_jsm.find_stream_name_by_subject = AsyncMock(side_effect=nats.js.errors.NotFoundError)
    mock_jsm.add_stream = AsyncMock()
    mock_js.subscribe = AsyncMock(return_value=mock_sub)

    with patch("ledger.management.commands.run_bookkeeping_session_worker.nats.connect", AsyncMock(return_value=mock_nc)):
        with patch("asyncio.Event.wait", AsyncMock()):
            await cmd._run_worker(
                nats_url="nats://localhost:4222",
                subject="custom.incoming.trigger",
                queue_group="test_group",
            )

    mock_jsm.add_stream.assert_awaited_once_with(
        name="BOOKKEEPING_EVENTS",
        subjects=["events.bookkeeping.>", "custom.incoming.trigger"],
    )
    mock_js.subscribe.assert_awaited_once()


@pytest.mark.asyncio
async def test_worker_uses_existing_stream():
    """Verify that _run_worker does not call add_stream if the stream is already found."""
    cmd = Command()

    mock_nc = MagicMock()
    mock_js = MagicMock()
    mock_jsm = MagicMock()
    mock_sub = MagicMock()
    mock_sub.unsubscribe = AsyncMock()

    mock_nc.jetstream.return_value = mock_js
    mock_nc.jsm.return_value = mock_jsm
    mock_nc.drain = AsyncMock()

    # find_stream_name_by_subject succeeds
    mock_jsm.find_stream_name_by_subject = AsyncMock(return_value="BOOKKEEPING_EVENTS")
    mock_jsm.add_stream = AsyncMock()
    mock_js.subscribe = AsyncMock(return_value=mock_sub)

    with patch("ledger.management.commands.run_bookkeeping_session_worker.nats.connect", AsyncMock(return_value=mock_nc)):
        with patch("asyncio.Event.wait", AsyncMock()):  # Instant shutdown
            await cmd._run_worker(
                nats_url="nats://localhost:4222",
                subject="events.bookkeeping.trigger",
                queue_group="test_group",
            )

    mock_jsm.add_stream.assert_not_awaited()
    mock_js.subscribe.assert_awaited_once()

"""Tests for database module functions.

Uses unittest.mock to avoid requiring a real PostgreSQL connection.
"""
from unittest.mock import AsyncMock, MagicMock, patch

import asyncpg
import pytest

from app.database import (
    close_pool,
    get_pool,
    init_pool,
    insert_checkout_session,
    insert_fc_attempt,
    update_checkout_session_status,
    upsert_linked_bank_account,
)


# ---------------------------------------------------------------------------
# init_pool / close_pool / get_pool
# ---------------------------------------------------------------------------

class TestPoolLifecycle:
    @pytest.mark.asyncio
    async def test_init_pool_creates_pool(self):
        mock_pool = MagicMock(spec=asyncpg.Pool)
        with patch("app.database.asyncpg.create_pool", new_callable=AsyncMock, return_value=mock_pool):
            await init_pool("postgresql://test:test@localhost/testdb")
            pool = get_pool()
            assert pool is mock_pool

    @pytest.mark.asyncio
    async def test_get_pool_raises_when_not_initialized(self):
        # Reset the pool singleton for this test
        with patch("app.database._pool", None):
            with pytest.raises(RuntimeError, match="Database pool not initialized"):
                get_pool()

    @pytest.mark.asyncio
    async def test_close_pool_closes_when_initialized(self):
        mock_pool = MagicMock(spec=asyncpg.Pool)
        mock_pool.close = AsyncMock()
        with patch("app.database._pool", mock_pool):
            await close_pool()
            mock_pool.close.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_close_pool_noop_when_not_initialized(self):
        with patch("app.database._pool", None):
            # Should not raise
            await close_pool()

    @pytest.mark.asyncio
    async def test_init_pool_sets_global(self):
        mock_pool = MagicMock(spec=asyncpg.Pool)
        with patch("app.database.asyncpg.create_pool", new_callable=AsyncMock, return_value=mock_pool):
            with patch("app.database._pool", None):
                await init_pool("postgresql://x")
                assert get_pool() is mock_pool


# ---------------------------------------------------------------------------
# insert_checkout_session
# ---------------------------------------------------------------------------

class TestInsertCheckoutSession:
    @pytest.mark.asyncio
    async def test_executes_insert_with_correct_params(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await insert_checkout_session(
                entity_id="eid-1",
                stripe_session_id="cs_test",
                amount_cents=2500,
                product_name="Premium",
                metadata={"user_id": "eid-1"},
            )

        mock_conn.execute.assert_awaited_once()
        call_args = mock_conn.execute.call_args
        assert call_args.args[1] == "eid-1"      # entity_id
        assert call_args.args[2] == "cs_test"     # stripe_session_id
        assert call_args.args[3] == 2500          # amount_cents
        assert call_args.args[4] == "Premium"     # product_name
        assert call_args.args[5] == {"user_id": "eid-1"}  # metadata

    @pytest.mark.asyncio
    async def test_uses_jsonb_cast(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await insert_checkout_session(
                entity_id="e1",
                stripe_session_id="cs1",
                amount_cents=1000,
                product_name="Test",
                metadata={"key": "value"},
            )

        sql = mock_conn.execute.call_args.args[0]
        assert "::jsonb" in sql

    @pytest.mark.asyncio
    async def test_propagates_unique_violation(self):
        mock_conn = AsyncMock()
        mock_conn.execute.side_effect = asyncpg.UniqueViolationError("duplicate key")
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            with pytest.raises(asyncpg.UniqueViolationError):
                await insert_checkout_session(
                    entity_id="e1",
                    stripe_session_id="dup",
                    amount_cents=1000,
                    product_name="Test",
                    metadata={},
                )


# ---------------------------------------------------------------------------
# update_checkout_session_status
# ---------------------------------------------------------------------------

class TestUpdateCheckoutSessionStatus:
    @pytest.mark.asyncio
    async def test_updates_status_to_completed(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await update_checkout_session_status(
                stripe_session_id="cs_abc",
                status="COMPLETED",
            )

        mock_conn.execute.assert_awaited_once()
        args = mock_conn.execute.call_args.args
        assert args[1] == "cs_abc"
        assert args[2] == "COMPLETED"

    @pytest.mark.asyncio
    async def test_updates_status_to_expired(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await update_checkout_session_status(
                stripe_session_id="cs_exp",
                status="EXPIRED",
            )

        mock_conn.execute.assert_awaited_once()
        args = mock_conn.execute.call_args.args
        assert args[1] == "cs_exp"
        assert args[2] == "EXPIRED"

    @pytest.mark.asyncio
    async def test_set_updated_at_to_now(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await update_checkout_session_status(
                stripe_session_id="cs_test",
                status="COMPLETED",
            )

        sql = mock_conn.execute.call_args.args[0]
        assert "updated_at = NOW()" in sql


# ---------------------------------------------------------------------------
# insert_fc_attempt
# ---------------------------------------------------------------------------

class TestInsertFCAttempt:
    @pytest.mark.asyncio
    async def test_inserts_with_correct_params(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await insert_fc_attempt(
                entity_id="eid-1",
                fc_session_id="fcsess_123",
                stripe_customer_id="cus_456",
            )

        mock_conn.execute.assert_awaited_once()
        args = mock_conn.execute.call_args.args
        assert args[1] == "eid-1"
        assert args[2] == "fcsess_123"
        assert args[3] == "cus_456"

    @pytest.mark.asyncio
    async def test_propagates_unique_violation(self):
        mock_conn = AsyncMock()
        mock_conn.execute.side_effect = asyncpg.UniqueViolationError("duplicate")
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            with pytest.raises(asyncpg.UniqueViolationError):
                await insert_fc_attempt(
                    entity_id="e1",
                    fc_session_id="dup",
                    stripe_customer_id="cus_x",
                )


# ---------------------------------------------------------------------------
# upsert_linked_bank_account
# ---------------------------------------------------------------------------

class TestUpsertLinkedBankAccount:
    @pytest.mark.asyncio
    async def test_upserts_with_all_fields(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await upsert_linked_bank_account(
                entity_id="eid-1",
                fc_session_id="fcsess_1",
                stripe_account_id="fca_1",
                institution_name="Chase",
                last4="6789",
                subcategory="checking",
                status="active",
            )

        mock_conn.execute.assert_awaited_once()
        args = mock_conn.execute.call_args.args
        assert args[1] == "eid-1"
        assert args[2] == "fcsess_1"
        assert args[3] == "fca_1"
        assert args[4] == "Chase"
        assert args[5] == "6789"
        assert args[6] == "checking"
        assert args[7] == "active"

    @pytest.mark.asyncio
    async def test_upserts_with_null_last4_and_subcategory(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await upsert_linked_bank_account(
                entity_id="eid-2",
                fc_session_id="fcsess_2",
                stripe_account_id="fca_2",
                institution_name="Unknown",
                last4=None,
                subcategory=None,
                status="pending",
            )

        mock_conn.execute.assert_awaited_once()
        args = mock_conn.execute.call_args.args
        assert args[5] is None  # last4
        assert args[6] is None  # subcategory

    @pytest.mark.asyncio
    async def test_uses_on_conflict_clause(self):
        mock_conn = AsyncMock()
        mock_pool = MagicMock()
        mock_pool.acquire.return_value.__aenter__.return_value = mock_conn

        with patch("app.database.get_pool", return_value=mock_pool):
            await upsert_linked_bank_account(
                entity_id="eid-3",
                fc_session_id="fcsess_3",
                stripe_account_id="fca_3",
                institution_name="Wells Fargo",
                last4="0000",
                subcategory="checking",
                status="active",
            )

        sql = mock_conn.execute.call_args.args[0]
        assert "ON CONFLICT (stripe_account_id) DO UPDATE" in sql
        assert "updated_at = NOW()" in sql

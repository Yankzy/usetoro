import json
import asyncpg

_pool: asyncpg.Pool | None = None


async def init_pool(dsn: str) -> None:
    global _pool
    _pool = await asyncpg.create_pool(dsn=dsn, min_size=2, max_size=10)


async def close_pool() -> None:
    global _pool
    if _pool:
        await _pool.close()


def get_pool() -> asyncpg.Pool:
    if _pool is None:
        raise RuntimeError("Database pool not initialized")
    return _pool


async def get_entity_id_for_user(user_id: str) -> str:
    pool = get_pool()
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT entity_id FROM toro_core.users WHERE id = $1::uuid OR entity_id = $1::uuid
            LIMIT 1
            """,
            user_id,
        )
        if not row:
            raise ValueError(f"User {user_id} not found")
        return str(row["entity_id"])


async def insert_checkout_session(
    entity_id: str,
    stripe_session_id: str,
    amount_cents: int,
    product_name: str,
    metadata: dict,
) -> None:
    pool = get_pool()
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO toro_core.stripe_checkout_sessions
                (entity_id, stripe_session_id, amount_cents, product_name, metadata)
            VALUES ($1, $2, $3, $4, $5::jsonb)
            """,
            entity_id,
            stripe_session_id,
            amount_cents,
            product_name,
            json.dumps(metadata),
        )


async def update_checkout_session_status(
    stripe_session_id: str,
    status: str,
) -> None:
    pool = get_pool()
    async with pool.acquire() as conn:
        await conn.execute(
            """
            UPDATE toro_core.stripe_checkout_sessions
            SET status = $2, updated_at = NOW()
            WHERE stripe_session_id = $1
            """,
            stripe_session_id,
            status,
        )


async def insert_fc_attempt(
    entity_id: str,
    fc_session_id: str,
    stripe_customer_id: str,
) -> None:
    pool = get_pool()
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO toro_core.financial_connection_attempts
                (entity_id, fc_session_id, stripe_customer_id)
            VALUES ($1, $2, $3)
            """,
            entity_id,
            fc_session_id,
            stripe_customer_id,
        )


async def upsert_linked_bank_account(
    entity_id: str,
    fc_session_id: str,
    stripe_account_id: str,
    institution_name: str,
    last4: str | None,
    subcategory: str | None,
    status: str,
) -> None:
    pool = get_pool()
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO toro_core.linked_bank_accounts
                (entity_id, fc_session_id, stripe_account_id, institution_name, last4, subcategory, status)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            ON CONFLICT (stripe_account_id) DO UPDATE SET
                fc_session_id = EXCLUDED.fc_session_id,
                institution_name = EXCLUDED.institution_name,
                last4 = EXCLUDED.last4,
                subcategory = EXCLUDED.subcategory,
                status = EXCLUDED.status,
                updated_at = NOW()
            """,
            entity_id,
            fc_session_id,
            stripe_account_id,
            institution_name,
            last4,
            subcategory,
            status,
        )

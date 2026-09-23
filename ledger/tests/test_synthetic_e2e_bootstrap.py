from __future__ import annotations

from typing import Any, cast
import uuid
from django.contrib.auth.models import User
from django.core.management import call_command
from django.core.management.base import CommandError
from django.db import connection
from django.test import TestCase

from bookkeeping_state_eval.lab import BookkeepingLab
from ledger.models.entity import EntityModel
from ledger.models.data_import import StagedTransactionModel


class SyntheticBookkeepingBootstrapAndE2ETest(TestCase):
    """
    Unit and integration tests for the production-like synthetic company bootstrap,
    fail-closed security invariants, and BookkeepingState REPL loader.
    """

    databases = {"default"}

    @classmethod
    def setUpTestData(cls) -> None:
        super().setUpTestData()
        cls.canonical_email = "yankz@fignode.com"
        cls.synthetic_slug = "toro-synthetic-bookkeeping"
        cls.synthetic_uuid = uuid.UUID("d0e1a1a0-7080-4500-a000-000070705001")
        cls.canonical_user, _ = User.objects.get_or_create(
            username=cls.canonical_email,
            email=cls.canonical_email,
            defaults={"first_name": "Yankz"},
        )

        # Ensure toro_core schema and tables exist in test database
        with connection.cursor() as cur:
            cur.execute("CREATE SCHEMA IF NOT EXISTS toro_core;")
            cur.execute("""
                CREATE TABLE IF NOT EXISTS toro_core.entities (
                    id UUID PRIMARY KEY,
                    parent_id UUID,
                    name VARCHAR,
                    entity_type VARCHAR,
                    plan_tier VARCHAR,
                    status VARCHAR,
                    created_at TIMESTAMPTZ DEFAULT NOW(),
                    updated_at TIMESTAMPTZ DEFAULT NOW()
                );
            """)
            cur.execute("""
                CREATE TABLE IF NOT EXISTS toro_core.users (
                    id UUID PRIMARY KEY,
                    entity_id UUID,
                    email VARCHAR UNIQUE,
                    full_name VARCHAR,
                    role VARCHAR,
                    created_at TIMESTAMPTZ DEFAULT NOW(),
                    updated_at TIMESTAMPTZ DEFAULT NOW()
                );
            """)
            cur.execute("""
                INSERT INTO toro_core.users (id, entity_id, email, full_name, role)
                VALUES (%s, %s, %s, 'Yankz', 'owner')
                ON CONFLICT (email) DO NOTHING;
            """, [uuid.uuid4(), uuid.uuid4(), cls.canonical_email])

    def test_seed_fails_closed_when_canonical_user_missing(self) -> None:
        """
        Bootstrap MUST fail-closed if the canonical user does not exist in auth_user.
        """
        with self.assertRaises(CommandError) as ctx:
            call_command("seed_production_like_bookkeeping_company", user_email="unknown_ghost_user@fignode.com")
        self.assertIn("not found in Django auth_user table", str(ctx.exception))

    def test_e2e_runner_fails_closed_when_canonical_user_missing(self) -> None:
        """
        E2E runner MUST fail-closed if the canonical user does not exist in auth_user.
        """
        with self.assertRaises(CommandError) as ctx:
            call_command("run_synthetic_bookkeeping_e2e", user_email="unknown_ghost_user@fignode.com")
        self.assertIn("not found in Django auth_user table", str(ctx.exception))

    def test_e2e_runner_fails_closed_on_ownership_mismatch(self) -> None:
        """
        E2E runner MUST fail-closed if the synthetic company is not owned by the resolved user.
        """
        other_user = User.objects.create_user(
            username="other_owner@fignode.com",
            email="other_owner@fignode.com",
            password="test_password_123",
        )
        entity = EntityModel.objects.filter(slug=self.synthetic_slug).first()
        if not entity:
            entity = EntityModel.add_root(
                name="Toro Synthetic Trading SARL",
                slug=self.synthetic_slug,
                uuid=self.synthetic_uuid,
                admin=cast(Any, self.canonical_user),
            )
        else:
            entity.admin = cast(Any, self.canonical_user)
            entity.save()

        # other_user does NOT own this company
        with self.assertRaises(CommandError) as ctx:
            call_command("run_synthetic_bookkeeping_e2e", user_email=other_user.email)
        self.assertIn("Entity ownership mismatch", str(ctx.exception))

    def test_seed_idempotency_and_safe_reset(self) -> None:
        """
        Verifies that:
        1. Seeding creates exactly 7 staged bank transactions.
        2. Re-seeding without reset does not duplicate transactions.
        3. Seeding with --reset cleanly cleans up previous transactions and decisions.
        """
        # 1. Initial seed
        call_command("seed_production_like_bookkeeping_company", user_email=self.canonical_email)
        entity = EntityModel.objects.get(uuid=self.synthetic_uuid)
        initial_count = StagedTransactionModel.objects.filter(import_job__bank_account_model__entity_model=entity).count()
        self.assertEqual(initial_count, 7)

        # 2. Re-seed without reset (must remain exactly 7)
        call_command("seed_production_like_bookkeeping_company", user_email=self.canonical_email)
        after_reseed_count = StagedTransactionModel.objects.filter(import_job__bank_account_model__entity_model=entity).count()
        self.assertEqual(after_reseed_count, 7)

        # 3. Seed with reset (purges & re-creates exactly 7)
        call_command("seed_production_like_bookkeeping_company", user_email=self.canonical_email, reset=True)
        after_reset_count = StagedTransactionModel.objects.filter(import_job__bank_account_model__entity_model=entity).count()
        self.assertEqual(after_reset_count, 7)

    def test_repl_hydrates_persisted_production_state(self) -> None:
        """
        Verifies that BookkeepingLab(production_state=True) properly hydrates the live
        state from PostgreSQL models, resolving the company slug to UUID and reading items.
        """
        call_command("seed_production_like_bookkeeping_company", user_email=self.canonical_email)

        lab = BookkeepingLab(
            production_state=True,
            company_id=self.synthetic_slug,
        )
        self.assertIsNotNone(lab.state)
        state = lab.state
        assert state is not None
        self.assertFalse(state.is_closed)

        # Check Bank Items (7 staged transactions hydrated)
        self.assertEqual(len(state.bank_items), 7)

        # Check that company_id resolves to the entity UUID
        self.assertEqual(lab.company_id, str(self.synthetic_uuid))

        # Check base currency
        self.assertEqual(state.context.base_currency, "MAD")

        # Cleanly close state
        state.close()

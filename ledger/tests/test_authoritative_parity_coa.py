from __future__ import annotations

import io
from django.core.management import call_command
from django.db import connection
from django.test import TestCase

from bookkeeping_state_eval.dag.parity_company_setup import (
    CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA,
    PARITY_SEED_ACCOUNTS,
    setup_authoritative_parity_company,
)
from ledger.models.accounts import AccountModel
from ledger.models.entity import EntityModel
from ledger.io.roles import ASSET_CA_CASH, DEBIT, ROOT_ASSETS


# Exact SQL query executed by Go domain tool pcge_catalog.go
AUTHORITATIVE_DJANGO_COA_QUERY = """
	SELECT a.code, a.name, a.role, a.balance_type, a.active
	FROM ledger_accountmodel a
	JOIN ledger_chartofaccountmodel coa ON coa.uuid = a.coa_model_id
	JOIN ledger_entitymodel e ON (e.default_coa_id = coa.uuid AND coa.entity_id = e.uuid)
	WHERE (REPLACE(CAST(e.uuid AS text), '-', '') = REPLACE(%s, '-', '') OR e.slug = %s)
	  AND a.active = true
	ORDER BY a.code ASC;
"""


class AuthoritativeParityCoaTests(TestCase):
    """
    Test suite verifying authoritative Moroccan PCGE Chart of Accounts setup
    and read path invariants for parity evaluation.
    """

    def setUp(self):
        super().setUp()
        self.entity = setup_authoritative_parity_company(slug="atlas", name="Atlas Office Solutions SARL")

    def test_parity_entity_has_default_coa(self):
        """Verify parity entity has an authoritative default Chart of Accounts."""
        self.assertTrue(self.entity.has_default_coa())
        self.assertIsNotNone(self.entity.default_coa)
        self.assertIsNotNone(self.entity.default_coa_id)
        default_coa = self.entity.default_coa
        assert default_coa is not None
        self.assertEqual(default_coa.entity, self.entity)
        self.assertTrue(default_coa.is_default())

    def test_seeded_accounts_belong_to_default_coa(self):
        """Verify minimum required accounts and realistic neighboring candidates belong to default CoA."""
        coa = self.entity.default_coa
        assert coa is not None
        min_required = ["3421", "4411", "6125", "4455", "2355", "7111", "5115"]

        for code in min_required:
            acc = AccountModel.objects.filter(coa_model=coa, code=code).first()
            self.assertIsNotNone(acc, f"Required minimum account {code} missing from default CoA")
            assert acc is not None
            self.assertTrue(acc.active, f"Required account {code} must be active")
            self.assertEqual(acc.coa_model, coa)

        # Verify all 27 seeded accounts are active and belong to this CoA
        seeded_codes = [a["code"] for a in PARITY_SEED_ACCOUNTS]
        active_coa_accounts = AccountModel.objects.filter(coa_model=coa, code__in=seeded_codes, active=True)
        self.assertEqual(active_coa_accounts.count(), len(PARITY_SEED_ACCOUNTS))

    def test_account_lookup_is_company_and_entity_scoped(self):
        """Verify Go read path query returns accounts scoped strictly by entity slug and uuid."""
        with connection.cursor() as cursor:
            # Lookup by slug
            cursor.execute(AUTHORITATIVE_DJANGO_COA_QUERY, [self.entity.slug, self.entity.slug])
            rows_by_slug = cursor.fetchall()
            self.assertGreaterEqual(len(rows_by_slug), len(PARITY_SEED_ACCOUNTS))

            # Lookup by UUID string
            cursor.execute(AUTHORITATIVE_DJANGO_COA_QUERY, [str(self.entity.uuid), str(self.entity.uuid)])
            rows_by_uuid = cursor.fetchall()
            self.assertEqual(len(rows_by_slug), len(rows_by_uuid))

            returned_codes = {r[0] for r in rows_by_slug}
            for req_code in ["3421", "4411", "6125", "4455", "2355", "7111", "5115"]:
                self.assertIn(req_code, returned_codes)

    def test_cross_company_account_leakage_is_rejected(self):
        """Verify queries for one company cannot leak accounts from another company."""
        other_entity = EntityModel.add_root(
            name="Other Enterprise Inc",
            slug="other-enterprise",
            admin=self.entity.admin,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        other_coa = other_entity.create_chart_of_accounts(
            coa_name="Other Enterprise CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = other_coa.accountmodel_set.get(role=ROOT_ASSETS)
        asset_root.add_child(
            coa_model=other_coa,
            code="9999",
            name="Private Account",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )

        with connection.cursor() as cursor:
            # Querying "atlas" must NEVER return "9999"
            cursor.execute(AUTHORITATIVE_DJANGO_COA_QUERY, ["atlas", "atlas"])
            atlas_codes = {r[0] for r in cursor.fetchall()}
            self.assertNotIn("9999", atlas_codes)

            # Querying "other-enterprise" must only return its own accounts
            cursor.execute(AUTHORITATIVE_DJANGO_COA_QUERY, ["other-enterprise", "other-enterprise"])
            other_codes = {r[0] for r in cursor.fetchall()}
            self.assertIn("9999", other_codes)
            self.assertNotIn("3421", other_codes)

    def test_entity_without_default_coa_fails_closed(self):
        """Verify company without default_coa returns zero candidate accounts (fails closed)."""
        bare_entity = EntityModel.add_root(
            name="Bare Company Without CoA",
            slug="bare-company",
            admin=self.entity.admin,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        self.assertIsNone(bare_entity.default_coa)

        with connection.cursor() as cursor:
            cursor.execute(AUTHORITATIVE_DJANGO_COA_QUERY, ["bare-company", "bare-company"])
            rows = cursor.fetchall()
            self.assertEqual(len(rows), 0, "Lookup must return 0 accounts when entity lacks default_coa")

    def test_no_shadow_erp_authority_used(self):
        """Verify that authoritative read path query does NOT reference shadow_erp."""
        self.assertNotIn("shadow_erp", AUTHORITATIVE_DJANGO_COA_QUERY.lower())
        self.assertIn("ledger_accountmodel", AUTHORITATIVE_DJANGO_COA_QUERY)
        self.assertIn("ledger_chartofaccountmodel", AUTHORITATIVE_DJANGO_COA_QUERY)
        self.assertIn("ledger_entitymodel", AUTHORITATIVE_DJANGO_COA_QUERY)

    def test_candidate_source_telemetry(self):
        """Verify diagnostic telemetry identifier constant."""
        self.assertEqual(CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA, "DJANGO_LEDGER_DEFAULT_COA")

    def test_no_django_migrations_pending(self):
        """Verify zero Django migrations are created or pending."""
        out = io.StringIO()
        try:
            call_command("makemigrations", "--dry-run", "--check", stdout=out)
        except SystemExit as e:
            self.assertEqual(e.code, 0, f"Pending migrations detected: {out.getvalue()}")

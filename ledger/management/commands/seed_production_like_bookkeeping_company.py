from __future__ import annotations

import uuid
from datetime import date, datetime, timezone as dt_timezone
from decimal import Decimal
from typing import Any, cast

from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError
from django.db import connection, transaction

from bookkeeping_state_eval.dag.parity_company_setup import (
    setup_authoritative_parity_company,
    verify_authoritative_coa_parity,
)
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    CREDIT,
    DEBIT,
    LIABILITY_CL_DEFERRED_REVENUE,
    ROOT_ASSETS,
    ROOT_LIABILITIES,
)
from ledger.models.accounts import AccountModel
from ledger.models.bank_account import BankAccountModel
from ledger.models.bill import BillModel
from ledger.models.customer import CustomerModel
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.invoice import InvoiceModel
from ledger.models.items import ItemModel, ItemTransactionModel, UnitOfMeasureModel
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.models.transactions import TransactionModel
from ledger.models.vendor import VendorModel

# Canonical deterministic synthetic company identity
DEFAULT_USER_EMAIL = "yankz@fignode.com"
DEFAULT_ENTITY_SLUG = "toro-synthetic-bookkeeping"
DEFAULT_ENTITY_NAME = "Toro Synthetic Trading SARL"
DEFAULT_ENTITY_UUID = uuid.UUID("d0e1a1a0-7080-4500-a000-000070705001")
DEFAULT_CURRENCY = "MAD"


class Command(BaseCommand):
    help = (
        "Idempotently seeds one production-like synthetic company into real database tables "
        "using real production models and services, owned by an existing user."
    )

    def add_arguments(self, parser):
        parser.add_argument(
            "--user-email",
            type=str,
            default=DEFAULT_USER_EMAIL,
            help=f"Existing owner user email (default: {DEFAULT_USER_EMAIL})",
        )
        parser.add_argument(
            "--entity-slug",
            type=str,
            default=DEFAULT_ENTITY_SLUG,
            help=f"Synthetic company slug (default: {DEFAULT_ENTITY_SLUG})",
        )
        parser.add_argument(
            "--entity-name",
            type=str,
            default=DEFAULT_ENTITY_NAME,
            help=f"Synthetic company name (default: {DEFAULT_ENTITY_NAME})",
        )
        parser.add_argument(
            "--currency",
            type=str,
            default=DEFAULT_CURRENCY,
            help=f"Company currency (default: {DEFAULT_CURRENCY})",
        )
        parser.add_argument(
            "--reset",
            action="store_true",
            help="Safely clear seeded transactional data for THIS dedicated synthetic entity only.",
        )
        parser.add_argument(
            "--run",
            action="store_true",
            help="Run the production bookkeeping session end-to-end immediately after seeding.",
        )

    def handle(self, *args, **options):
        user_email = options["user_email"].strip().lower()
        entity_slug = options["entity_slug"].strip()
        entity_name = options["entity_name"].strip()
        currency = options["currency"].strip().upper()
        is_reset = options["reset"]
        should_run = options["run"]

        self.stdout.write(f"=== Synthetic Bookkeeping Bootstrap for user: {user_email} ===")

        # ------------------------------------------------------------------
        # 1. Resolve EXISTING user via canonical Go/SQL + Django ownership
        # ------------------------------------------------------------------
        UserModel = get_user_model()
        auth_user = UserModel.objects.filter(email=user_email).first()
        if auth_user is None:
            raise CommandError(
                f"Fail-closed: User '{user_email}' not found in Django auth_user table.\n"
                f"Refusing to silently invent an unverified replacement user."
            )

        # Check Go/SQL user in toro_core.users
        toro_user_row = None
        try:
            with connection.cursor() as cur:
                cur.execute(
                    "SELECT id, entity_id, email, full_name, role FROM toro_core.users WHERE email = %s",
                    [user_email],
                )
                toro_user_row = cur.fetchone()
        except Exception:
            toro_user_row = None

        if toro_user_row is None:
            raise CommandError(
                f"Fail-closed: User '{user_email}' does not exist in canonical toro_core.users store.\n"
                f"Django auth_user: {auth_user}\n"
                f"toro_core.users: {toro_user_row}\n"
                f"Refusing to silently invent an unverified replacement user."
            )

        toro_user_id, toro_parent_entity_id, _, full_name, role = toro_user_row
        self.stdout.write(
            self.style.SUCCESS(
                f"Resolved existing canonical user: {user_email} "
                f"(auth_user_id={auth_user.id}, toro_user_id={toro_user_id}, org_entity_id={toro_parent_entity_id})"
            )
        )

        # ------------------------------------------------------------------
        # 2. Reset dedicated synthetic company if requested
        # ------------------------------------------------------------------
        if is_reset:
            self._reset_synthetic_company(entity_slug)

        # ------------------------------------------------------------------
        # 3. Resolve or create dedicated synthetic company in Go/SQL + Django
        # ------------------------------------------------------------------
        synthetic_uuid = DEFAULT_ENTITY_UUID

        # Establish Go/SQL entity node with parent_id linking to user's org
        try:
            with connection.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO toro_core.entities (id, parent_id, name, entity_type, plan_tier, status)
                    VALUES (%s, %s, %s, 'client', 'pro', 'active')
                    ON CONFLICT (id) DO UPDATE SET
                        name = EXCLUDED.name,
                        status = 'active',
                        updated_at = NOW();
                    """,
                    [synthetic_uuid, toro_parent_entity_id, entity_name],
                )
        except Exception:
            pass

        # Establish Django EntityModel
        entity = EntityModel.objects.filter(slug=entity_slug).first()
        if entity is None:
            entity = EntityModel.objects.filter(uuid=synthetic_uuid).first()

        if entity is None:
            entity = EntityModel.add_root(
                uuid=synthetic_uuid,
                name=entity_name,
                slug=entity_slug,
                admin=cast(Any, auth_user),
                currency=currency,
                fy_start_month=1,
                accrual_method=False,
            )
            self.stdout.write(f"Created dedicated EntityModel '{entity.name}' ({entity.uuid})")
        else:
            updated = False
            if entity.admin_id != auth_user.id:
                entity.admin = cast(Any, auth_user)
                updated = True
            if entity.name != entity_name:
                entity.name = entity_name
                updated = True
            if entity.currency != currency:
                entity.currency = currency
                updated = True
            if updated:
                entity.save(update_fields=["admin", "name", "currency", "updated"])
            self.stdout.write(f"Resolved existing dedicated EntityModel '{entity.name}' ({entity.uuid})")

        # ------------------------------------------------------------------
        # 4. Authoritative Moroccan PCGE Chart of Accounts
        # ------------------------------------------------------------------
        entity = setup_authoritative_parity_company(
            slug=entity_slug,
            name=entity_name,
            admin_email=user_email,
        )

        parity_check = verify_authoritative_coa_parity(
            entity,
            expected_codes=["5141", "3421", "4411", "4456", "6147", "7127", "7111", "6125"],
        )
        self.stdout.write(
            self.style.SUCCESS(
                f"Authoritative Moroccan CoA verified: {parity_check['account_count']} active accounts "
                f"matching Go SQL catalog exactly."
            )
        )

        coa = entity.default_coa
        assert coa is not None

        acc_5141 = AccountModel.objects.get(coa_model=coa, code="5141", active=True)
        acc_3421 = AccountModel.objects.get(coa_model=coa, code="3421", active=True)
        acc_4411 = AccountModel.objects.get(coa_model=coa, code="4411", active=True)
        acc_4456 = AccountModel.objects.get(coa_model=coa, code="4456", active=True)
        acc_6147 = AccountModel.objects.get(coa_model=coa, code="6147", active=True)
        acc_7127 = AccountModel.objects.get(coa_model=coa, code="7127", active=True)
        acc_7111 = AccountModel.objects.get(coa_model=coa, code="7111", active=True)
        acc_6125 = AccountModel.objects.get(coa_model=coa, code="6125", active=True)

        # Ensure required supporting accounts exist for InvoiceModel (deferred revenue) and BillModel (prepaid expense)
        root_assets = AccountModel.objects.filter(coa_model=coa, role=ROOT_ASSETS).first()
        root_liabilities = AccountModel.objects.filter(coa_model=coa, role=ROOT_LIABILITIES).first()

        acc_3491 = AccountModel.objects.filter(coa_model=coa, code="3491").first()
        if not acc_3491 and root_assets:
            acc_3491 = root_assets.add_child(
                coa_model=coa,
                code="3491",
                name="Fournisseurs - Avances et acomptes versés",
                role=ASSET_CA_PREPAID,
                balance_type=DEBIT,
                active=True,
            )
        elif acc_3491 and (not acc_3491.active or acc_3491.role != ASSET_CA_PREPAID):
            acc_3491.active = True
            acc_3491.role = ASSET_CA_PREPAID
            acc_3491.save(update_fields=["active", "role"])

        acc_4491 = AccountModel.objects.filter(coa_model=coa, code="4491").first()
        if not acc_4491 and root_liabilities:
            acc_4491 = root_liabilities.add_child(
                coa_model=coa,
                code="4491",
                name="Clients - Avances et acomptes reçus",
                role=LIABILITY_CL_DEFERRED_REVENUE,
                balance_type=CREDIT,
                active=True,
            )
        elif acc_4491 and (not acc_4491.active or acc_4491.role != LIABILITY_CL_DEFERRED_REVENUE):
            acc_4491.active = True
            acc_4491.role = LIABILITY_CL_DEFERRED_REVENUE
            acc_4491.save(update_fields=["active", "role"])

        assert acc_3491 is not None
        assert acc_4491 is not None

        # ------------------------------------------------------------------
        # 5. Core Ledger & Counterparties
        # ------------------------------------------------------------------
        ledger, _ = LedgerModel.objects.get_or_create(
            entity=entity,
            name=f"{entity_name} General Ledger",
            defaults={"posted": True},
        )

        customer, _ = CustomerModel.objects.get_or_create(
            entity_model=entity,
            customer_number="CLI-ATLAS-01",
            defaults={"customer_name": "Atlas Client SARL"},
        )

        vendor, _ = VendorModel.objects.get_or_create(
            entity_model=entity,
            vendor_number="VEND-EQUIP-01",
            defaults={"vendor_name": "Fournisseur Equipement SARL"},
        )

        uom, _ = UnitOfMeasureModel.objects.get_or_create(
            entity=entity,
            name="Unit",
            unit_abbr="U",
        )

        service_item, _ = ItemModel.objects.get_or_create(
            entity=entity,
            sku="SRV-CONSULTING-MAD",
            defaults={
                "name": "Prestation de Conseil",
                "item_number": "ITEM-SRV-01",
                "uom": uom,
                "item_role": ItemModel.ITEM_ROLE_SERVICE,
                "item_type": ItemModel.ITEM_TYPE_LABOR,
                "earnings_account": acc_7111,
                "cogs_account": acc_6125,
                "for_inventory": False,
                "is_product_or_service": True,
            },
        )

        expense_item, _ = ItemModel.objects.get_or_create(
            entity=entity,
            sku="EXP-OFFICE-SUPPLIES",
            defaults={
                "name": "Fournitures et Equipement",
                "item_number": "ITEM-EXP-01",
                "uom": uom,
                "item_role": ItemModel.ITEM_ROLE_EXPENSE,
                "item_type": ItemModel.ITEM_TYPE_OTHER,
                "expense_account": acc_6125,
                "for_inventory": False,
                "is_product_or_service": False,
            },
        )

        # ------------------------------------------------------------------
        # 6. Dedicated Bank Account & Import Job
        # ------------------------------------------------------------------
        bank_account, _ = BankAccountModel.objects.get_or_create(
            entity_model=entity,
            account_model=acc_5141,
            defaults={
                "name": "Attijariwafa Bank Principal",
                "account_number": "007780000123456789012345",
                "routing_number": "007780",
                "active": True,
                "metadata": {
                    "bank_name": "Attijariwafa Bank",
                    "account_number": "007780000123456789012345",
                    "rib": "007780000123456789012345",
                    "iban": "MA64007780000123456789012345",
                },
            },
        )
        bank_account.active = True
        if not isinstance(bank_account.metadata, dict):
            bank_account.metadata = {}
        bank_account.metadata["rib"] = "007780000123456789012345"
        bank_account.metadata["iban"] = "MA64007780000123456789012345"
        bank_account.metadata["account_number"] = "007780000123456789012345"
        bank_account.save(update_fields=["active", "metadata"])

        import_job, _ = ImportJobModel.objects.get_or_create(
            bank_account_model=bank_account,
            ledger_model=ledger,
            description="Relevé Bancaire Avril 2026",
        )

        # ------------------------------------------------------------------
        # 7. Seed Production-Like Accounting Datasets (Cases 1 - 7)
        # ------------------------------------------------------------------
        self._seed_case1_customer_invoice_and_deposit(
            entity=entity,
            ledger=ledger,
            customer=customer,
            service_item=service_item,
            cash_account=acc_5141,
            ar_account=acc_3421,
            unearned_account=acc_4491,
            import_job=import_job,
            user=auth_user,
        )

        self._seed_case2_supplier_bill_and_withdrawal(
            entity=entity,
            ledger=ledger,
            vendor=vendor,
            expense_item=expense_item,
            cash_account=acc_5141,
            ap_account=acc_4411,
            prepaid_account=acc_3491,
            import_job=import_job,
            user=auth_user,
        )

        self._seed_case3_stage2_cash_gl_and_deposit(
            entity=entity,
            ledger=ledger,
            cash_account=acc_5141,
            revenue_account=acc_7111,
            import_job=import_job,
        )

        self._seed_case4_residual_dgi_vat(import_job=import_job)
        self._seed_case5_residual_bank_fee(import_job=import_job)
        self._seed_case6_residual_misc_income(import_job=import_job)
        self._seed_case7_residual_hold(import_job=import_job)

        total_staged = StagedTransactionModel.objects.filter(import_job=import_job).count()
        self.stdout.write(
            self.style.SUCCESS(
                f"Synthetic company successfully prepared with {total_staged} staged bank transactions "
                f"covering all 7 lifecycle cases."
            )
        )

        if should_run:
            self.stdout.write("\nTriggering production bookkeeping session...")
            from django.core.management import call_command
            call_command(
                "run_synthetic_bookkeeping_e2e",
                user_email=user_email,
                entity_slug=entity_slug,
            )

    # ======================================================================
    # Case Implementations
    # ======================================================================

    def _seed_case1_customer_invoice_and_deposit(
        self,
        *,
        entity: EntityModel,
        ledger: LedgerModel,
        customer: CustomerModel,
        service_item: ItemModel,
        cash_account: AccountModel,
        ar_account: AccountModel,
        unearned_account: AccountModel,
        import_job: ImportJobModel,
        user: Any,
    ) -> None:
        """Case 1: Open approved invoice (10,000 MAD) + matching bank inflow -> Stage 1 applies payment."""
        inv_amount = Decimal("10000.00")
        inv_date = date(2026, 4, 5)

        inv = InvoiceModel.objects.filter(
            ledger__entity=entity,
            invoice_number="INV-SYNTH-2026-001",
        ).first()

        if inv is None:
            inv = InvoiceModel(
                cash_account=cash_account,
                prepaid_account=ar_account,
                unearned_account=unearned_account,
                accrue=False,
            )
            inv.invoice_number = "INV-SYNTH-2026-001"
            _, inv = inv.configure(entity_slug=entity, user_model=user, commit_ledger=True)
            inv.date_draft = inv_date
            inv.customer = customer
            inv.save()

            itm = ItemTransactionModel(
                invoice_model=inv,
                item_model=service_item,
                quantity=1.0,
                unit_cost=float(inv_amount),
                total_amount=inv_amount,
            )
            itm.full_clean()
            itm.save()

            inv.update_amount_due()
            inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
            inv.date_approved = inv_date
            inv.clean()
            inv.save()
            self.stdout.write(f"  [Case 1] Seeded approved Invoice {inv.invoice_number} ({inv_amount} MAD)")

        # Staged Bank Deposit: 10,000 MAD
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE1-INVOICE",
            defaults={
                "amount": inv_amount,  # Positive = Inflow
                "date_posted": inv_date,
                "name": "VIREMENT CLIENT ATLAS SARL REF INV-SYNTH-2026-001",
                "memo": "Paiement facture client",
            },
        )

    def _seed_case2_supplier_bill_and_withdrawal(
        self,
        *,
        entity: EntityModel,
        ledger: LedgerModel,
        vendor: VendorModel,
        expense_item: ItemModel,
        cash_account: AccountModel,
        ap_account: AccountModel,
        prepaid_account: AccountModel,
        import_job: ImportJobModel,
        user: Any,
    ) -> None:
        """Case 2: Open approved bill (4,500 MAD) + matching bank outflow -> Stage 1 applies payment."""
        bill_amount = Decimal("4500.00")
        bill_date = date(2026, 4, 8)

        bill = BillModel.objects.filter(
            ledger__entity=entity,
            bill_number="BILL-SYNTH-2026-001",
        ).first()

        if bill is None:
            bill = BillModel(
                cash_account=cash_account,
                prepaid_account=prepaid_account,
                unearned_account=ap_account,
                accrue=False,
            )
            bill.bill_number = "BILL-SYNTH-2026-001"
            _, bill = bill.configure(entity_slug=entity, user_model=user, commit_ledger=True)
            bill.date_draft = bill_date
            bill.vendor = vendor
            bill.save()

            itm = ItemTransactionModel(
                bill_model=bill,
                item_model=expense_item,
                quantity=1.0,
                unit_cost=float(bill_amount),
                total_amount=bill_amount,
            )
            itm.full_clean()
            itm.save()

            bill.update_amount_due()
            bill.bill_status = BillModel.BILL_STATUS_APPROVED
            bill.date_approved = bill_date
            bill.clean()
            bill.save()
            self.stdout.write(f"  [Case 2] Seeded approved Bill {bill.bill_number} ({bill_amount} MAD)")

        # Staged Bank Withdrawal: -4,500 MAD
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE2-BILL",
            defaults={
                "amount": -bill_amount,  # Negative = Outflow
                "date_posted": bill_date,
                "name": "VIREMENT FOURNISSEUR EQUIPEMENT SARL BILL-SYNTH-2026-001",
                "memo": "Reglement facture fournisseur",
            },
        )

    def _seed_case3_stage2_cash_gl_and_deposit(
        self,
        *,
        entity: EntityModel,
        ledger: LedgerModel,
        cash_account: AccountModel,
        revenue_account: AccountModel,
        import_job: ImportJobModel,
    ) -> None:
        """Case 3: Pre-posted cash GL transaction (2,500 MAD) + matching bank deposit -> Stage 2 reconciles."""
        amount = Decimal("2500.00")
        tx_date = date(2026, 4, 12)

        je = JournalEntryModel.objects.filter(
            ledger=ledger,
            description="Encaissement direct en agence bancaire - Vente comptoir",
        ).first()

        if je is None:
            je = JournalEntryModel.objects.create(
                ledger=ledger,
                je_number="FIT-SYNTH-CASE3-CASHGL",
                description="Encaissement direct en agence bancaire - Vente comptoir",
                posted=False,
                timestamp=datetime(tx_date.year, tx_date.month, tx_date.day, 12, 0, 0, tzinfo=dt_timezone.utc),
            )
            TransactionModel.objects.create(
                journal_entry=je,
                account=cash_account,
                amount=amount,
                tx_type=DEBIT,  # Bank debit increases cash asset
                reconciled=False,
                description="Encaissement direct agence",
            )
            TransactionModel.objects.create(
                journal_entry=je,
                account=revenue_account,
                amount=amount,
                tx_type=CREDIT,
                reconciled=False,
                description="Ventes comptoir agence",
            )
            je.posted = True
            je.save(update_fields=["posted", "je_number"], verify=False)
            self.stdout.write(f"  [Case 3] Seeded posted cash GL Journal Entry ({amount} MAD)")
        elif je.je_number != "FIT-SYNTH-CASE3-CASHGL":
            je.je_number = "FIT-SYNTH-CASE3-CASHGL"
            je.save(update_fields=["je_number"], verify=False)

        # Staged Bank Deposit: 2,500 MAD
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE3-CASHGL",
            defaults={
                "amount": amount,  # Positive = Inflow
                "date_posted": tx_date,
                "name": "VERSEMENT ESPECES AGENCE BANCAIRE REF DEP-4412",
                "memo": "Versement direct especes",
            },
        )

    def _seed_case4_residual_dgi_vat(self, *, import_job: ImportJobModel) -> None:
        """Case 4: Bank outflow 3,400 MAD -> Residual ASE classifies to 4456 (DGI TVA)."""
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE4-DGIVAT",
            defaults={
                "amount": Decimal("-3400.00"),  # Outflow
                "date_posted": date(2026, 4, 15),
                "name": "TELEPAIEMENT DIRECTION GENERALE DES IMPOTS TVA DGI REF 202604",
                "memo": "TVA Due Avril 2026",
            },
        )

    def _seed_case5_residual_bank_fee(self, *, import_job: ImportJobModel) -> None:
        """Case 5: Bank outflow 150 MAD -> Residual ASE classifies to 6147 (Bank fee)."""
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE5-BANKFEE",
            defaults={
                "amount": Decimal("-150.00"),  # Outflow
                "date_posted": date(2026, 4, 20),
                "name": "COMMISSION BANCAIRE ET FRAIS TENUE DE COMPTE ATT-0426",
                "memo": "Frais de gestion bancaire",
            },
        )

    def _seed_case6_residual_misc_income(self, *, import_job: ImportJobModel) -> None:
        """Case 6: Bank inflow 1,200 MAD -> Residual ASE classifies to 7127 (Misc revenue)."""
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE6-MISCINC",
            defaults={
                "amount": Decimal("1200.00"),  # Inflow
                "date_posted": date(2026, 4, 22),
                "name": "VENTE DE PRODUITS ACCESSOIRES MATERIEL REBUT",
                "memo": "Produits accessoires hors exploitation",
            },
        )

    def _seed_case7_residual_hold(self, *, import_job: ImportJobModel) -> None:
        """Case 7: Ambiguous bank movement -> Residual ASE returns HOLD under 0.98 policy."""
        StagedTransactionModel.objects.get_or_create(
            import_job=import_job,
            fit_id="FIT-SYNTH-CASE7-HOLD",
            defaults={
                "amount": Decimal("7850.00"),  # Ambiguous Inflow
                "date_posted": date(2026, 4, 25),
                "name": "VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS",
                "memo": "Ecriture non documentee",
            },
        )

    # ======================================================================
    # Safe Teardown Logic
    # ======================================================================

    def _reset_synthetic_company(self, entity_slug: str) -> None:
        """Safely cleans transactional data for THIS dedicated synthetic entity only."""
        entity = EntityModel.objects.filter(slug=entity_slug).first()
        if not entity:
            self.stdout.write(f"No existing entity '{entity_slug}' found to reset.")
            return

        self.stdout.write(self.style.WARNING(f"Resetting synthetic company '{entity.slug}' ({entity.uuid})..."))

        with transaction.atomic():
            # 1. Clean bookkeeping decisions and postings (protecting StagedTransactionModel)
            from ledger.models.bookkeeping import (
                BookkeepingClassificationDecision,
                BookkeepingClassificationInvalidation,
                BookkeepingEvidenceAssertion,
                BookkeepingEvidenceInvalidation,
                BookkeepingPaymentApplication,
                BookkeepingPaymentApplicationAllocation,
                BookkeepingReconciliation,
                BookkeepingReconciliationBankAllocation,
                BookkeepingReconciliationBookAllocation,
                BookkeepingReconciliationInvalidation,
                BookkeepingResidualBankClassificationDecision,
                BookkeepingResidualBankClassificationInvalidation,
                BookkeepingResidualBankPosting,
                BookkeepingRevision,
                BookkeepingRoutingDecision,
                BookkeepingRoutingInvalidation,
            )

            BookkeepingResidualBankPosting.objects.filter(entity=entity).delete()
            BookkeepingResidualBankClassificationInvalidation.objects.filter(classification__entity=entity).delete()
            BookkeepingResidualBankClassificationDecision.objects.filter(entity=entity).delete()
            BookkeepingReconciliationInvalidation.objects.filter(reconciliation__entity=entity).delete()
            BookkeepingReconciliationBookAllocation.objects.filter(reconciliation__entity=entity).delete()
            BookkeepingReconciliationBankAllocation.objects.filter(reconciliation__entity=entity).delete()
            BookkeepingReconciliation.objects.filter(entity=entity).delete()
            BookkeepingPaymentApplicationAllocation.objects.filter(payment_application__entity=entity).delete()
            BookkeepingPaymentApplication.objects.filter(entity=entity).delete()
            BookkeepingClassificationInvalidation.objects.filter(classification__entity=entity).delete()
            BookkeepingClassificationDecision.objects.filter(entity=entity).delete()
            BookkeepingRoutingInvalidation.objects.filter(routing_decision__entity=entity).delete()
            BookkeepingRoutingDecision.objects.filter(entity=entity).delete()
            BookkeepingEvidenceInvalidation.objects.filter(assertion__entity=entity).delete()
            BookkeepingEvidenceAssertion.objects.filter(entity=entity).delete()
            BookkeepingRevision.objects.filter(entity=entity).delete()

            # 2. Clean staged transactions and import jobs
            from ledger.models.data_import import StagedTransactionModel, ImportJobModel
            staged_qs = StagedTransactionModel.objects.filter(import_job__bank_account_model__entity_model=entity)
            staged_count = staged_qs.count()
            staged_qs.delete()

            ImportJobModel.objects.filter(bank_account_model__entity_model=entity).delete()

            # 3. Clean invoices, bills, items, and journal entries
            ItemTransactionModel.objects.filter(
                invoice_model__ledger__entity=entity
            ).delete()
            ItemTransactionModel.objects.filter(
                bill_model__ledger__entity=entity
            ).delete()

            InvoiceModel.objects.filter(ledger__entity=entity).delete()
            BillModel.objects.filter(ledger__entity=entity).delete()

            TransactionModel.objects.filter(journal_entry__ledger__entity=entity).delete()
            JournalEntryModel.objects.filter(ledger__entity=entity).delete()

            self.stdout.write(
                self.style.SUCCESS(
                    f"Safely purged {staged_count} staged transactions and all associated bookkeeping artifacts for {entity_slug}."
                )
            )

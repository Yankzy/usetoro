from __future__ import annotations

from typing import Any, Sequence
from django.contrib.auth import get_user_model

from ledger.io.roles import (
    DEBIT,
    CREDIT,
    ASSET_PPE_EQUIPMENT,
    ASSET_CA_RECEIVABLES,
    ASSET_CA_OTHER,
    ASSET_CA_CASH,
    LIABILITY_CL_ACC_PAYABLE,
    LIABILITY_CL_WAGES_PAYABLE,
    LIABILITY_CL_TAXES_PAYABLE,
    LIABILITY_LTL_NOTES_PAYABLE,
    EQUITY_CAPITAL,
    COGS,
    EXPENSE_OPERATIONAL,
    INCOME_OPERATIONAL,
    ROOT_ASSETS,
    ROOT_LIABILITIES,
    ROOT_CAPITAL,
    ROOT_EXPENSES,
    ROOT_COGS,
    ROOT_INCOME,
)
from ledger.models.entity import EntityModel
from ledger.models.chart_of_accounts import ChartOfAccountModel
from ledger.models.accounts import AccountModel

# Telemetry tag identifying candidate accounts sourced from authoritative Django ledger default CoA
CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA = "DJANGO_LEDGER_DEFAULT_COA"

# Minimum required accounts + realistic neighbors for non-trivial ranking
PARITY_SEED_ACCOUNTS: tuple[dict[str, Any], ...] = (
    # --- Minimum Required Accounts ---
    {"code": "2355", "name": "Matériel informatique", "role": ASSET_PPE_EQUIPMENT, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "3421", "name": "Clients", "role": ASSET_CA_RECEIVABLES, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "4411", "name": "Fournisseurs", "role": LIABILITY_CL_ACC_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},
    {"code": "4455", "name": "État, TVA facturée", "role": LIABILITY_CL_TAXES_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},
    {"code": "5115", "name": "Virements de fonds", "role": ASSET_CA_CASH, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "6125", "name": "Achats de fournitures de bureau", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},
    {"code": "7111", "name": "Ventes de marchandises au Maroc", "role": INCOME_OPERATIONAL, "balance_type": CREDIT, "root_role": ROOT_INCOME},

    # --- Realistic Neighboring Candidates (Assets) ---
    {"code": "2351", "name": "Mobilier de bureau", "role": ASSET_PPE_EQUIPMENT, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "2352", "name": "Matériel de bureau", "role": ASSET_PPE_EQUIPMENT, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "3425", "name": "Clients - Effets à recevoir", "role": ASSET_CA_RECEIVABLES, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "3455", "name": "État, TVA récupérable sur charges", "role": ASSET_CA_OTHER, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "3456", "name": "État, crédit de TVA", "role": ASSET_CA_OTHER, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "5141", "name": "Banques", "role": ASSET_CA_CASH, "balance_type": DEBIT, "root_role": ROOT_ASSETS},
    {"code": "5161", "name": "Caisses", "role": ASSET_CA_CASH, "balance_type": DEBIT, "root_role": ROOT_ASSETS},

    # --- Realistic Neighboring Candidates (Liabilities) ---
    {"code": "4417", "name": "Fournisseurs - Factures non parvenues", "role": LIABILITY_CL_ACC_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},
    {"code": "4432", "name": "Rémunérations dues au personnel", "role": LIABILITY_CL_WAGES_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},
    {"code": "4456", "name": "État, TVA due", "role": LIABILITY_CL_TAXES_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},

    # --- Realistic Neighboring Candidates (Expenses & COGS) ---
    {"code": "6111", "name": "Achats de marchandises", "role": COGS, "balance_type": DEBIT, "root_role": ROOT_COGS},
    {"code": "6121", "name": "Achats de matières premières", "role": COGS, "balance_type": DEBIT, "root_role": ROOT_COGS},
    {"code": "6131", "name": "Locations et charges locatives", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},
    {"code": "6134", "name": "Services informatiques et télécommunications", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},
    {"code": "6136", "name": "Rémunérations d'intermédiaires et honoraires", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},
    {"code": "6147", "name": "Services bancaires et commissions", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},
    {"code": "6171", "name": "Rémunérations du personnel", "role": EXPENSE_OPERATIONAL, "balance_type": DEBIT, "root_role": ROOT_EXPENSES},

    # --- Realistic Neighboring Candidates (Revenues) ---
    {"code": "7121", "name": "Ventes de biens produits au Maroc", "role": INCOME_OPERATIONAL, "balance_type": CREDIT, "root_role": ROOT_INCOME},
    {"code": "7124", "name": "Ventes de services produits au Maroc", "role": INCOME_OPERATIONAL, "balance_type": CREDIT, "root_role": ROOT_INCOME},
    {"code": "7127", "name": "Ventes de produits accessoires", "role": INCOME_OPERATIONAL, "balance_type": CREDIT, "root_role": ROOT_INCOME},

    # --- Realistic Candidates (Financial Liabilities & Equity) ---
    {"code": "1481", "name": "Emprunts auprès des établissements de crédit", "role": LIABILITY_LTL_NOTES_PAYABLE, "balance_type": CREDIT, "root_role": ROOT_LIABILITIES},
    {"code": "1111", "name": "Capital social ou personnel", "role": EQUITY_CAPITAL, "balance_type": CREDIT, "root_role": ROOT_CAPITAL},
)


def setup_authoritative_parity_company(
    slug: str = "atlas",
    name: str = "Atlas Office Solutions SARL",
    admin_email: str = "admin@atlas.ma",
    seed_accounts: Sequence[dict[str, Any]] = PARITY_SEED_ACCOUNTS,
) -> EntityModel:
    """
    Builds or updates the authoritative parity test company with real Django ledger objects:
    - EntityModel (scoped by slug/UUID)
    - ChartOfAccountModel
    - EntityModel.default_coa strictly configured
    - AccountModel rows seeded and active under proper root hierarchy
    """
    UserModel = get_user_model()
    admin_user, _ = UserModel.objects.get_or_create(
        email=admin_email,
        defaults={
            "username": admin_email.split("@")[0],
            "first_name": "Atlas",
            "last_name": "Admin",
        },
    )

    entity = EntityModel.objects.filter(slug=slug).first()
    if not entity:
        entity = EntityModel.add_root(
            name=name,
            slug=slug,
            admin=admin_user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )

    # Ensure default_coa exists
    if not entity.default_coa:
        coa = entity.create_chart_of_accounts(
            coa_name=f"{entity.name} Moroccan PCGE CoA",
            assign_as_default=True,
            commit=True,
        )
    else:
        coa = entity.default_coa

    # Verify default_coa assignment is bidirectional and committed
    if entity.default_coa_id != coa.uuid:
        entity.default_coa = coa
        entity.save(update_fields=["default_coa", "updated"])

    # Root hierarchy mapping
    root_map = {
        ROOT_ASSETS: AccountModel.objects.filter(coa_model=coa, role=ROOT_ASSETS).first(),
        ROOT_LIABILITIES: AccountModel.objects.filter(coa_model=coa, role=ROOT_LIABILITIES).first(),
        ROOT_CAPITAL: AccountModel.objects.filter(coa_model=coa, role=ROOT_CAPITAL).first(),
        ROOT_EXPENSES: AccountModel.objects.filter(coa_model=coa, role=ROOT_EXPENSES).first(),
        ROOT_COGS: AccountModel.objects.filter(coa_model=coa, role=ROOT_COGS).first() or AccountModel.objects.filter(coa_model=coa, role=ROOT_EXPENSES).first(),
        ROOT_INCOME: AccountModel.objects.filter(coa_model=coa, role=ROOT_INCOME).first(),
    }

    for acc_def in seed_accounts:
        code = acc_def["code"]
        existing = AccountModel.objects.filter(coa_model=coa, code=code).first()
        if existing:
            updated = False
            if not existing.active:
                existing.active = True
                updated = True
            if existing.name != acc_def["name"]:
                existing.name = acc_def["name"]
                updated = True
            if existing.role != acc_def["role"]:
                existing.role = acc_def["role"]
                updated = True
            if existing.balance_type != acc_def["balance_type"]:
                existing.balance_type = acc_def["balance_type"]
                updated = True
            if updated:
                existing.save(update_fields=["active", "name", "role", "balance_type"])
        else:
            parent_root = root_map[acc_def["root_role"]]
            if parent_root is None:
                raise ValueError(f"Root node for role {acc_def['root_role']} not found in CoA {coa.uuid}")
            parent_root.add_child(
                coa_model=coa,
                code=code,
                name=acc_def["name"],
                role=acc_def["role"],
                balance_type=acc_def["balance_type"],
                active=True,
            )

    entity.refresh_from_db()
    return entity


# Authoritative SQL query matching Go pcge_catalog.go AuthoritativeDjangoCoaQuery exactly
AUTHORITATIVE_DJANGO_COA_QUERY = """
	SELECT a.code, a.name, a.role, a.balance_type, a.active
	FROM ledger_accountmodel a
	JOIN ledger_chartofaccountmodel coa ON coa.uuid = a.coa_model_id
	JOIN ledger_entitymodel e ON (e.default_coa_id = coa.uuid AND coa.entity_id = e.uuid)
	WHERE (REPLACE(CAST(e.uuid AS text), '-', '') = REPLACE(%s, '-', '') OR e.slug = %s)
	  AND a.active = true
	ORDER BY a.code ASC;
"""


def cleanup_authoritative_parity_company(slug: str = "eval-residual-bank") -> None:
    """Explicit and safe teardown of an authoritative parity/eval entity."""
    entity = EntityModel.objects.filter(slug=slug).first()
    if entity:
        coa = entity.default_coa
        entity.default_coa = None
        entity.save(update_fields=["default_coa"])
        if coa:
            coa.accountmodel_set.all().delete()
            coa.delete()
        entity.delete()


def verify_authoritative_coa_parity(
    entity_or_slug: str | EntityModel,
    expected_codes: Sequence[str] | set[str] | None = None,
) -> dict[str, Any]:
    """
    Direct pre-flight parity assertion proving that:
    1. Python Django ORM observes the authoritative entity and default CoA.
    2. Go AuthoritativeDjangoCoaQuery raw SQL observes the exact same accounts.
    3. Account counts and code sets match with zero divergence.
    """
    from django.db import connection

    slug = entity_or_slug.slug if isinstance(entity_or_slug, EntityModel) else str(entity_or_slug)
    entity = EntityModel.objects.filter(slug=slug).first()
    if not entity:
        raise ValueError(f"Entity with slug '{slug}' not found in authoritative DB")
    if not entity.default_coa:
        raise ValueError(f"Entity '{slug}' has no default_coa configured")

    orm_accounts = list(
        entity.default_coa.accountmodel_set.filter(active=True)
        .not_coa_root()
        .values("code", "name", "role", "balance_type", "active")
    )
    orm_codes = sorted([a["code"] for a in orm_accounts])

    with connection.cursor() as cur:
        cur.execute(AUTHORITATIVE_DJANGO_COA_QUERY, [str(entity.uuid), slug])
        sql_rows = cur.fetchall()
    sql_codes = sorted([r[0] for r in sql_rows])

    if orm_codes != sql_codes:
        raise AssertionError(
            f"Parity mismatch for entity '{slug}': ORM codes ({len(orm_codes)}) != SQL codes ({len(sql_codes)}).\n"
            f"ORM: {orm_codes}\nSQL: {sql_codes}"
        )

    if expected_codes is not None:
        missing = set(expected_codes) - set(orm_codes)
        if missing:
            raise AssertionError(
                f"Expected codes missing from authoritative CoA for entity '{slug}': {missing}"
            )

    return {
        "slug": entity.slug,
        "uuid": str(entity.uuid),
        "default_coa_uuid": str(entity.default_coa.uuid),
        "account_count": len(orm_codes),
        "account_codes": orm_codes,
        "raw_sql_matched": True,
    }


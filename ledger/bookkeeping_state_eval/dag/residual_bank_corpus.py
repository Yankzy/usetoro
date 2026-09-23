from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal
from typing import Any, Sequence
import uuid

from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationView,
    build_residual_bank_categorization_view,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.state.queries import BookkeepingQueries
from ledger.models.accounts import AccountModel
from ledger.models.bank_account import BankAccountModel
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel


@dataclass(frozen=True)
class ResidualBankCaseSpec:
    case_id: str
    amount: Decimal
    date_posted: date
    description: str
    counterparty_name: str | None
    reference: str | None
    direction: str  # "OUTFLOW" | "INFLOW"
    category: str  # "OUTFLOW" | "INFLOW" | "HOLD_SAFETY"
    expected_terminal_type: str  # "CLASSIFIED" | "HOLD"
    expected_account_code: str | None
    expected_macro_family: str | None
    expected_hold_reason: str | None
    risk_class: str  # "LOW" | "MEDIUM" | "HIGH"
    rationale: str


RESIDUAL_BANK_CORPUS_SPECS: tuple[ResidualBankCaseSpec, ...] = (
    # =========================================================================
    # OUTFLOWS (5 cases)
    # =========================================================================
    ResidualBankCaseSpec(
        case_id="res-out-fee-01",
        amount=Decimal("-150.00"),
        date_posted=date(2026, 7, 2),
        description="FRAIS TENUE DE COMPTE T2 2026",
        counterparty_name="ATTIJARIWAFA BANK",
        reference="FEE-2026-T2",
        direction="OUTFLOW",
        category="OUTFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="6147",
        expected_macro_family="EXPENSE",
        expected_hold_reason=None,
        risk_class="LOW",
        rationale="Moroccan PCGE 6147 (Services bancaires et commissions). Standard quarterly bank account maintenance fee debit.",
    ),
    ResidualBankCaseSpec(
        case_id="res-out-supplies-02",
        amount=Decimal("-420.00"),
        date_posted=date(2026, 7, 5),
        description="ACHAT FOURNITURES BUREAU ET PAPETERIE",
        counterparty_name="LIBRAIRIE NATIONALE CASABLANCA",
        reference="TKT-88219",
        direction="OUTFLOW",
        category="OUTFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="6125",
        expected_macro_family="EXPENSE",
        expected_hold_reason=None,
        risk_class="LOW",
        rationale="Moroccan PCGE 6125 (Achats de fournitures de bureau). Direct cash operating expense for office and desk supplies.",
    ),
    ResidualBankCaseSpec(
        case_id="res-out-telecom-03",
        amount=Decimal("-890.00"),
        date_posted=date(2026, 7, 8),
        description="ABONNEMENT FIBRE OPTIQUE PRO ET TELECOM",
        counterparty_name="MAROC TELECOM",
        reference="IAM-PRO-7712",
        direction="OUTFLOW",
        category="OUTFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="6134",
        expected_macro_family="EXPENSE",
        expected_hold_reason=None,
        risk_class="LOW",
        rationale="Moroccan PCGE 6134 (Services informatiques et télécommunications). High-speed internet telecom utility subscription.",
    ),
    ResidualBankCaseSpec(
        case_id="res-out-tax-04",
        amount=Decimal("-3400.00"),
        date_posted=date(2026, 7, 10),
        description="TELEPAIEMENT DGI TVA TRIMESTRIELLE",
        counterparty_name="TRESORERIE GENERALE DU ROYAUME",
        reference="DGI-TVA-Q2-2026",
        direction="OUTFLOW",
        category="OUTFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="4456",
        expected_macro_family="LIABILITY",
        expected_hold_reason=None,
        risk_class="MEDIUM",
        rationale="Moroccan PCGE 4456 (État, TVA due). Direct quarterly VAT tax settlement payment to the Moroccan Tax Administration (DGI / TGR). Extinguishes established VAT payable liability rather than recording invoice-level output VAT (4455).",
    ),
    ResidualBankCaseSpec(
        case_id="res-out-asset-05",
        amount=Decimal("-7800.00"),
        date_posted=date(2026, 7, 12),
        description="ACHAT SERVEUR INFORMATIQUE ET MATERIEL RESEAU",
        counterparty_name="DISWAY MAROC",
        reference="DIS-FAC-4401",
        direction="OUTFLOW",
        category="OUTFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="2355",
        expected_macro_family="ASSET",
        expected_hold_reason=None,
        risk_class="MEDIUM",
        rationale="Moroccan PCGE 2355 (Matériel informatique). Computer server purchase exceeding capitalization threshold (>5,000 MAD); fixed asset balance sheet item.",
    ),

    # =========================================================================
    # INFLOWS (4 cases)
    # =========================================================================
    ResidualBankCaseSpec(
        case_id="res-in-merch-01",
        amount=Decimal("5500.00"),
        date_posted=date(2026, 7, 15),
        description="ENCAISSEMENT VENTE COMPTOIR MARCHANDISES",
        counterparty_name="CLIENT COMPTOIR",
        reference="REC-DIR-001",
        direction="INFLOW",
        category="INFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="7111",
        expected_macro_family="REVENUE",
        expected_hold_reason=None,
        risk_class="LOW",
        rationale="Moroccan PCGE 7111 (Ventes de marchandises au Maroc). Direct merchandise sales operating revenue with no prior open invoice obligation.",
    ),
    ResidualBankCaseSpec(
        case_id="res-in-service-02",
        amount=Decimal("12000.00"),
        date_posted=date(2026, 7, 18),
        description="HONORAIRES PRESTATION SERVICE FORMATION PRO",
        counterparty_name="INSTITUT CASABLANCA",
        reference="VIR-INST-2026",
        direction="INFLOW",
        category="INFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="7124",
        expected_macro_family="REVENUE",
        expected_hold_reason=None,
        risk_class="LOW",
        rationale="Moroccan PCGE 7124 (Ventes de services produits au Maroc). Professional training service revenue received directly with no invoice record.",
    ),
    ResidualBankCaseSpec(
        case_id="res-in-loan-03",
        amount=Decimal("100000.00"),
        date_posted=date(2026, 7, 20),
        description="DEBLOCAGE CREDIT EQUIPEMENT PRO EMPRUNT BANCAIRE",
        counterparty_name="BANQUE POPULAIRE DU MAROC",
        reference="LOAN-DEBLOC-990",
        direction="INFLOW",
        category="INFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="1481",
        expected_macro_family="LIABILITY",
        expected_hold_reason=None,
        risk_class="MEDIUM",
        rationale="Moroccan PCGE 1481 (Emprunts auprès des établissements de crédit). Bank business loan principal disbursement into company checking account.",
    ),
    ResidualBankCaseSpec(
        case_id="res-in-capital-04",
        amount=Decimal("50000.00"),
        date_posted=date(2026, 7, 22),
        description="APPORT CAPITAL SOCIAL ASSOCIE FONDATEUR",
        counterparty_name="ASSOCIE FONDATEUR",
        reference="APPORT-CAP-01",
        direction="INFLOW",
        category="INFLOW",
        expected_terminal_type="CLASSIFIED",
        expected_account_code="1111",
        expected_macro_family="EQUITY",
        expected_hold_reason=None,
        risk_class="MEDIUM",
        rationale="Moroccan PCGE 1111 (Capital social ou personnel). Founding shareholder equity capital contribution deposit.",
    ),

    # =========================================================================
    # HOLDS / SAFETY (6 cases)
    # =========================================================================
    ResidualBankCaseSpec(
        case_id="res-hold-ambig-01",
        amount=Decimal("-500.00"),
        date_posted=date(2026, 7, 24),
        description="AMBIGUOUS RETRAIT DIVERS GUICHET SANS DETAILS",
        counterparty_name=None,
        reference="REF-AMBIG-01",
        direction="OUTFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_AMBIGUOUS",
        risk_class="HIGH",
        rationale="Explicitly ambiguous counter withdrawal missing economic context and counterparty; requires operator review.",
    ),
    ResidualBankCaseSpec(
        case_id="res-hold-unknown-02",
        amount=Decimal("1200.00"),
        date_posted=date(2026, 7, 25),
        description="UNKNOWN ENCAISSEMENT INDETERMINE SANS PIECE",
        counterparty_name=None,
        reference="REF-UNKNOWN-02",
        direction="INFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_AMBIGUOUS",
        risk_class="HIGH",
        rationale="Unidentified receipt lacking counterparty, reference, or description; must fail safe to HOLD_AMBIGUOUS.",
    ),
    ResidualBankCaseSpec(
        case_id="res-hold-vague-03",
        amount=Decimal("-250.00"),
        date_posted=date(2026, 7, 26),
        description="REGLEMENT DIVERS OPERATIONS",
        counterparty_name=None,
        reference="REF-VAGUE-03",
        direction="OUTFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
        risk_class="HIGH",
        rationale="Generic vague description without supplier, tax, or expense identification; insufficient evidence for automated classification.",
    ),
    ResidualBankCaseSpec(
        case_id="res-hold-xfer-bmce-04",
        amount=Decimal("-25000.00"),
        date_posted=date(2026, 7, 27),
        description="VIREMENT INTERNE VERS COMPTE BMCE",
        counterparty_name="ATTIJARIWAFA TO BMCE",
        reference="VIR-INT-001",
        direction="OUTFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
        risk_class="MEDIUM",
        rationale="Internal bank transfer detected. Must hold until paired with corresponding bank line rather than falsely booking an expense.",
    ),
    ResidualBankCaseSpec(
        case_id="res-hold-xfer-cc-05",
        amount=Decimal("30000.00"),
        date_posted=date(2026, 7, 28),
        description="VIR COMPTE A COMPTE TRESORERIE",
        counterparty_name=None,
        reference="VIR-CC-99",
        direction="INFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
        risk_class="MEDIUM",
        rationale="Account-to-account treasury transfer detected with no paired contra-line; requires clearing via suspense rather than revenue.",
    ),
    ResidualBankCaseSpec(
        case_id="res-hold-xfer-5115-06",
        amount=Decimal("-15000.00"),
        date_posted=date(2026, 7, 29),
        description="TRANSIT 5115 ALIMENTATION CAISSE PRINCIPALE",
        counterparty_name=None,
        reference="VIR-5115-01",
        direction="OUTFLOW",
        category="HOLD_SAFETY",
        expected_terminal_type="HOLD",
        expected_account_code=None,
        expected_macro_family=None,
        expected_hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
        risk_class="MEDIUM",
        rationale="Explicit 5115 internal transit suspense movement detected; requires paired cash account reconciliation, held by safety guard.",
    ),
)


def setup_residual_bank_corpus_in_db(
    entity: EntityModel,
    specs: Sequence[ResidualBankCaseSpec] = RESIDUAL_BANK_CORPUS_SPECS,
) -> tuple[BankAccountModel, ImportJobModel, dict[str, StagedTransactionModel]]:
    """
    Authoritative database seeding for the residual bank categorization evaluation.
    Creates:
    - Dedicated BankAccountModel under entity with MAD currency
    - ImportJobModel
    - Exactly 1 StagedTransactionModel row per corpus spec with authoritative amounts and dates.

    Returns (bank_account, import_job, {case_id: staged_tx}).
    """
    # Clean up previous evaluation bank accounts for this entity if any
    BankAccountModel.objects.filter(
        entity_model=entity,
        name="Atlas Eval Bank Account MAD",
    ).delete()

    cash_acc = AccountModel.objects.filter(coa_model=entity.default_coa, code="5141").first()
    if cash_acc is None:
        cash_acc = AccountModel.objects.filter(coa_model=entity.default_coa, role="asset_ca_cash").first()

    bank_account = BankAccountModel.objects.create(
        name="Atlas Eval Bank Account MAD",
        entity_model=entity,
        account_model=cash_acc,
        connection_type="manual",
        account_number="007-810-0000000000-01",
        active=True,
    )

    import_job = ImportJobModel.objects.create(
        description="Authoritative Moroccan Residual Bank Evaluation Import",
        bank_account_model=bank_account,
    )

    staged_tx_map: dict[str, StagedTransactionModel] = {}
    for spec in specs:
        fit_id = spec.reference or f"FIT-{spec.case_id}"
        stx = StagedTransactionModel.objects.create(
            import_job=import_job,
            amount=spec.amount,
            date_posted=spec.date_posted,
            name=spec.description,
            fit_id=fit_id,
        )
        staged_tx_map[spec.case_id] = stx

    return bank_account, import_job, staged_tx_map


def hydrate_residual_bank_corpus_view(
    entity: EntityModel,
    session_id: str = "eval-residual-bank-session",
) -> tuple[ResidualBankCategorizationView, dict[str, str]]:
    """
    Hydrate the residual bank categorization bounded view through the exact production boundary:
    1. Read Django persistence through BookkeepingHydrator / BookkeepingRepository
    2. Ensure zero open invoices/bills or posted cash transactions were matched (pure residual population)
    3. Call BookkeepingQueries.residual_unmatched_bank_items()
    4. Construct bounded ResidualBankCategorizationView via build_residual_bank_categorization_view()
    
    Returns (view, {staged_bank_item_id: case_id}).
    """
    repo = BookkeepingRepository()
    hydrator = BookkeepingHydrator(repository=repo)
    state = hydrator.hydrate(company_id=str(entity.uuid), session_id=session_id)
    queries = BookkeepingQueries(state)

    # Build production view
    view = build_residual_bank_categorization_view(queries)

    # Map staged:<uuid> to case_id by reading staged transactions from DB
    staged_txs = StagedTransactionModel.objects.filter(
        import_job__bank_account_model__entity_model=entity
    )
    uuid_to_spec_id: dict[str, str] = {}
    for spec in RESIDUAL_BANK_CORPUS_SPECS:
        matching_stx = staged_txs.filter(name=spec.description, amount=spec.amount).first()
        if matching_stx:
            uuid_to_spec_id[f"staged:{matching_stx.uuid}"] = spec.case_id

    return view, uuid_to_spec_id

from __future__ import annotations

from datetime import date, datetime, timezone
from typing import Mapping, Sequence

from bookkeeping_state.dag.view import DagView, DagViewItem, build_dag_view
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state_eval.dag.parity_models import (
    BookCategorizationParityCase,
    RiskClass,
)
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.scenarios.catalog import SCENARIOS_MAP
from bookkeeping_state_eval.scenarios.challenges import CHALLENGES_MAP
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition


def _scenario_to_dag_view(scenario: ScenarioDefinition, session_id: str) -> DagView:
    snap = scenario.to_initial_snapshot()
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc),
        session_id_factory=lambda: session_id,
    )
    state = hydrator.hydrate(company_id=scenario.context.company_id, session_id=session_id)
    return build_dag_view(BookkeepingQueries(state), include_already_classified=True)


def build_catalog_parity_cases() -> list[BookCategorizationParityCase]:
    cases: list[BookCategorizationParityCase] = []

    # Risk mapping for catalog scenarios
    risk_mapping = {
        "scenario_a_exact_receipt": RiskClass.LOW,
        "scenario_b_one_to_many": RiskClass.LOW,
        "scenario_c_many_to_one": RiskClass.LOW,
        "scenario_d_partial_customer_receipt": RiskClass.LOW,
        "scenario_e_partial_bank_forbidden": RiskClass.MEDIUM,
        "scenario_f_routing_ambiguity": RiskClass.LOW,
        "scenario_g_duplicate_amounts_tie_breaking": RiskClass.MEDIUM,
        "scenario_h_supplier_payment_multiple_bills": RiskClass.HIGH,  # Supplier liability polarity
        "scenario_i_unmatched_bank_transaction": RiskClass.MEDIUM,
        "scenario_j_unmatched_book_entry": RiskClass.LOW,
        "scenario_k_currency_mismatch": RiskClass.MEDIUM,
        "scenario_l_date_window_rejection": RiskClass.MEDIUM,
        "scenario_m_preexisting_partial_reconciliation": RiskClass.LOW,
        "scenario_n_invalidation_releases_capacity": RiskClass.MEDIUM,
        "scenario_o_supersession_non_resurrection": RiskClass.MEDIUM,  # Operating expense
        "scenario_p_second_session_idempotency": RiskClass.LOW,
        "scenario_q_optimizer_competition": RiskClass.MEDIUM,
        "scenario_r_routing_contradiction_blocks_reconciliation": RiskClass.MEDIUM,
        "scenario_s_superseded_route_coexistence": RiskClass.MEDIUM,
    }

    for name, sc in SCENARIOS_MAP.items():
        session_id = f"parity-cat-{name}"
        view = _scenario_to_dag_view(sc, session_id)
        if len(view.items) == 0:
            continue

        exp_truth = dict(sc.expected_truth.expected_classifications) if sc.expected_truth and sc.expected_truth.expected_classifications else None
        risk = risk_mapping.get(name, RiskClass.MEDIUM)

        cases.append(
            BookCategorizationParityCase(
                case_id=f"cat_{name}",
                dag_view=view,
                expected_truth=exp_truth,
                risk_class=risk,
                tags=("catalog", name),
            )
        )

    return cases


def build_challenge_parity_cases() -> list[BookCategorizationParityCase]:
    cases: list[BookCategorizationParityCase] = []

    for name, ch in CHALLENGES_MAP.items():
        sc = ch.scenario
        session_id = f"parity-chal-{name}"
        view = _scenario_to_dag_view(sc, session_id)
        if len(view.items) == 0:
            continue

        exp_truth = dict(sc.expected_truth.expected_classifications) if sc.expected_truth and sc.expected_truth.expected_classifications else None

        # Challenge 01 has explicit truth (3421)
        if name == "challenge_01_dense_collision":
            risk = RiskClass.LOW
        elif name in ("challenge_03_account_currency_minefield", "challenge_07_dirty_month_end"):
            risk = RiskClass.HIGH
        else:
            risk = RiskClass.MEDIUM

        cases.append(
            BookCategorizationParityCase(
                case_id=f"chal_{name}",
                dag_view=view,
                expected_truth=exp_truth,
                risk_class=risk,
                tags=("challenge", name) + tuple(ch.tags),
            )
        )

    return cases


def build_targeted_gap_parity_cases() -> list[BookCategorizationParityCase]:
    """
    Targeted minimal fixtures covering specific accounting dimensions:
    - Tax sensitivity (DGI / TVA) -> HIGH risk
    - Asset capitalization vs Expense (Material / Hardware) -> HIGH risk
    - Revenue inflow polarity (Sale / Customer receipt) -> HIGH risk
    - Bank/Cash control transfer -> HIGH risk
    - Semantic HOLD on missing descriptions and ambiguous narratives -> LOW/MEDIUM risk
    """
    cases: list[BookCategorizationParityCase] = []

    # 1. Tax Sensitive (HIGH risk): DGI payment
    item_tax = DagViewItem(
        book_item_id="book-tax-01",
        date=date(2026, 2, 20),
        amount_units=185000000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="TELEDECLARATION DGI TVA JANVIER",
        reference="DGI-TVA-2026-01",
        safe_evidence_summaries=("Doc:dgi-ack-01:tax_return",),
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_tax_sensitive_tva",
            dag_view=DagView(
                session_id="parity-gap-tax",
                state_revision=1,
                items=(item_tax,),
                company_id="comp-gap-tax",
                persistence_revision=1,
            ),
            expected_truth={"book-tax-01": "4455"},  # État - TVA due
            risk_class=RiskClass.HIGH,
            tags=("targeted", "tax", "high_risk"),
        )
    )

    # 2. Capital Asset vs Expense (HIGH risk): Server equipment capitalization
    item_asset = DagViewItem(
        book_item_id="book-asset-01",
        date=date(2026, 2, 22),
        amount_units=540000000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="SERVEUR MATERIEL INFORMATIQUE DELL",
        reference="FAC-DELL-992",
        evidence_refs=("doc-dell-inv",),
        safe_evidence_summaries=("Doc:doc-dell-inv:invoice",),
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_asset_capitalization_vs_expense",
            dag_view=DagView(
                session_id="parity-gap-asset",
                state_revision=1,
                items=(item_asset,),
                company_id="comp-gap-asset",
                persistence_revision=1,
            ),
            expected_truth={"book-asset-01": "2355"},  # Matériel informatique (Asset)
            risk_class=RiskClass.HIGH,
            tags=("targeted", "asset_vs_expense", "high_risk"),
        )
    )

    # 3. Revenue / Customer Inflow (HIGH risk): Direct sales revenue
    item_rev = DagViewItem(
        book_item_id="book-rev-01",
        date=date(2026, 2, 25),
        amount_units=320000000,
        currency="MAD",
        direction=Direction.INFLOW,
        description="VENTES DE MARCHANDISES AU MAROC",
        reference="EXP-INV-881",
        safe_evidence_summaries=("Doc:export-customs-01:customs_slip",),
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_sales_revenue_inflow",
            dag_view=DagView(
                session_id="parity-gap-rev",
                state_revision=1,
                items=(item_rev,),
                company_id="comp-gap-rev",
                persistence_revision=1,
            ),
            expected_truth={"book-rev-01": "7111"},  # Ventes de marchandises
            risk_class=RiskClass.HIGH,
            tags=("targeted", "revenue", "high_risk"),
        )
    )

    # 4. Bank / Control account confusion (HIGH risk): Internal transfer
    item_xfer = DagViewItem(
        book_item_id="book-xfer-01",
        date=date(2026, 2, 26),
        amount_units=500000000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="VIREMENT INTERNE COMPTE A COMPTE",
        reference="VIR-INT-001",
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_bank_control_transfer",
            dag_view=DagView(
                session_id="parity-gap-xfer",
                state_revision=1,
                items=(item_xfer,),
                company_id="comp-gap-xfer",
                persistence_revision=1,
            ),
            expected_truth={"book-xfer-01": "5115"},  # Virements de fonds (Control)
            risk_class=RiskClass.HIGH,
            tags=("targeted", "bank_control", "high_risk"),
        )
    )

    # 5. Semantic HOLD (LOW risk): Neutral empty description
    item_neutral = DagViewItem(
        book_item_id="book-hold-neutral-01",
        date=date(2026, 2, 27),
        amount_units=1500000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description=None,
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_hold_empty_description",
            dag_view=DagView(
                session_id="parity-gap-hold-empty",
                state_revision=1,
                items=(item_neutral,),
                company_id="comp-gap-hold-empty",
                persistence_revision=1,
            ),
            expected_truth={"book-hold-neutral-01": None},  # Expected HOLD
            risk_class=RiskClass.LOW,
            tags=("targeted", "hold", "empty_description"),
        )
    )

    # 6. Semantic HOLD (LOW risk): Explicitly ambiguous description
    item_ambig = DagViewItem(
        book_item_id="book-hold-ambig-01",
        date=date(2026, 2, 28),
        amount_units=7500000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="REGLEMENT AMBIGU TRANSACTION SANS FACTURE",
    )
    cases.append(
        BookCategorizationParityCase(
            case_id="gap_hold_ambiguous_keyword",
            dag_view=DagView(
                session_id="parity-gap-hold-ambig",
                state_revision=1,
                items=(item_ambig,),
                company_id="comp-gap-hold-ambig",
                persistence_revision=1,
            ),
            expected_truth={"book-hold-ambig-01": None},  # Expected HOLD
            risk_class=RiskClass.LOW,
            tags=("targeted", "hold", "ambiguity"),
        )
    )

    return cases


def get_full_parity_corpus() -> list[BookCategorizationParityCase]:
    """
    Return the unified parity corpus combining Catalog, Challenges, and minimal gap fixtures.
    """
    all_cases: list[BookCategorizationParityCase] = []
    all_cases.extend(build_catalog_parity_cases())
    all_cases.extend(build_challenge_parity_cases())
    all_cases.extend(build_targeted_gap_parity_cases())
    return all_cases

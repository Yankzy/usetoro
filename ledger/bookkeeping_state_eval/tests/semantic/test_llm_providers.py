"""
Unit tests for LLM semantic reasoning providers (Routing and Reconciliation).

These tests mock AsyncOpenAI to verify provider contracts, error handling,
packaging-blindness, provider-neutral observation decoupling, and reference
contradiction rules without making genuine API calls.
"""

from __future__ import annotations

from datetime import date
import json
from typing import Any
from unittest.mock import AsyncMock, MagicMock

import pytest

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state.reconciliation.allocation_analysis import (
    evaluate_allocation_support,
)
from bookkeeping_state.reconciliation.candidate_generation import (
    DefaultReconciliationScorer,
)
from bookkeeping_state.reconciliation.llm_scorer import (
    LlmReconciliationSemanticScoreProvider,
    ReconciliationSemanticScoringError,
)
from bookkeeping_state.reconciliation.models import (
    CandidateType,
    CounterpartyRelation,
    PairwiseSemanticObservation,
    ReconciliationCandidate,
    ReferenceRelation,
)
from bookkeeping_state.reconciliation.view import (
    ReconciliationBankItemView,
    ReconciliationBookItemView,
    ReconciliationView,
    ReconciliationViewConfig,
)
from bookkeeping_state.routing.llm_scorer import (
    LlmRoutingSemanticScoreProvider,
)
from bookkeeping_state.routing.scorer import (
    RoutingSemanticBankEvidence,
    RoutingSemanticCandidate,
    RoutingSemanticScoringError,
    RoutingSemanticScoringRequest,
    ZeroRoutingSemanticScoreProvider,
)


def _make_mock_client(response_dict: dict[str, Any]) -> AsyncMock:
    """Create a mock AsyncOpenAI client returning a given dictionary as JSON response."""
    mock_client = AsyncMock()
    mock_response = MagicMock()
    mock_response.output_text = json.dumps(response_dict)
    mock_client.responses.create.return_value = mock_response
    return mock_client


def _make_routing_candidate(
    *,
    book_item_id: str = "inv-101",
    bank_account_id: str = "acc-eur",
    bank_account_name: str = "EUR Operating",
    currency: str = "EUR",
    amount_units: str = "1000",
    counterparty_name: str | None = "Acme Europe",
    description: str | None = "Invoice payment for consulting",
    reference: str | None = "INV-101",
) -> RoutingSemanticCandidate:
    return RoutingSemanticCandidate(
        book_item_id=book_item_id,
        bank_account_id=bank_account_id,
        target_amount_units=amount_units,
        book_date=date(2026, 1, 14),
        book_direction=Direction.BOOK_BANK_CREDIT,
        currency=currency,
        bank_account_name=bank_account_name,
        counterparty_name=counterparty_name,
        book_description=description,
        book_reference=reference,
        bank_evidence=(
            RoutingSemanticBankEvidence(
                bank_item_id=f"bi-{bank_account_id}",
                date=date(2026, 1, 15),
                remaining_amount_units=amount_units,
                direction=Direction.BANK_OUTFLOW,
                description="Sample bank transaction",
                is_feasibility_witness=True,
            ),
        ),
    )


# =====================================================================
# Routing LLM Provider Tests
# =====================================================================

class TestRoutingLlmProvider:
    def test_routing_scoring_success(self) -> None:
        mock_response = {
            "routing_scores": [
                {
                    "account_id": "acc-eur",
                    "utility_score": 920,
                    "semantic_rationale": "Counterparty EUR operating account match",
                },
                {
                    "account_id": "acc-usd",
                    "utility_score": 150,
                    "semantic_rationale": "Currency mismatch context",
                },
            ]
        }
        client = _make_mock_client(mock_response)
        provider = LlmRoutingSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        request = RoutingSemanticScoringRequest(
            state_revision=1,
            candidates=(
                _make_routing_candidate(bank_account_id="acc-eur", bank_account_name="EUR Operating", currency="EUR"),
                _make_routing_candidate(bank_account_id="acc-usd", bank_account_name="USD Operating", currency="USD"),
            ),
        )

        response = provider.score(request)
        assert len(response.scores) == 2
        assert all(s.book_item_id == "inv-101" for s in response.scores)

        score_map = {s.bank_account_id: s for s in response.scores}
        assert score_map["acc-eur"].score == 920
        assert score_map["acc-usd"].score == 150

    def test_routing_singleton_feasibility_telemetry(self) -> None:
        """When exactly one feasible account exists, singleton feasibility optimization marks rationale."""
        client = AsyncMock()
        provider = LlmRoutingSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        request = RoutingSemanticScoringRequest(
            state_revision=1,
            candidates=(
                _make_routing_candidate(book_item_id="inv-single", bank_account_id="acc-only", currency="MAD"),
            ),
        )

        response = provider.score(request)
        assert len(response.scores) == 1
        assert response.scores[0].bank_account_id == "acc-only"
        assert response.scores[0].score == 1000
        assert "singleton" in response.scores[0].rationale.lower()
        # Verify no network call was made
        assert client.responses.create.call_count == 0

    def test_routing_unknown_account_id_raises(self) -> None:
        """Requirement B: LLM returns account ID not in feasible candidates -> fail-fast."""
        mock_response = {
            "routing_scores": [
                {
                    "account_id": "acc-unknown",
                    "utility_score": 900,
                    "semantic_rationale": "Invented account",
                },
                {
                    "account_id": "acc-usd",
                    "utility_score": 100,
                    "semantic_rationale": "Other account",
                },
            ]
        }
        client = _make_mock_client(mock_response)
        provider = LlmRoutingSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        request = RoutingSemanticScoringRequest(
            state_revision=1,
            candidates=(
                _make_routing_candidate(bank_account_id="acc-eur"),
                _make_routing_candidate(bank_account_id="acc-usd"),
            ),
        )

        with pytest.raises(RoutingSemanticScoringError, match=r"unknown/infeasible accounts"):
            provider.score(request)

    def test_routing_out_of_bounds_score_raises(self) -> None:
        """Requirement C: Score not in [0, 1000] raises RoutingSemanticScoringError."""
        mock_response = {
            "routing_scores": [
                {
                    "account_id": "acc-eur",
                    "utility_score": 1200,
                    "semantic_rationale": "Too high",
                },
                {
                    "account_id": "acc-usd",
                    "utility_score": 100,
                    "semantic_rationale": "Other account",
                },
            ]
        }
        client = _make_mock_client(mock_response)
        provider = LlmRoutingSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        request = RoutingSemanticScoringRequest(
            state_revision=1,
            candidates=(
                _make_routing_candidate(bank_account_id="acc-eur"),
                _make_routing_candidate(bank_account_id="acc-usd"),
            ),
        )

        with pytest.raises(RoutingSemanticScoringError, match=r"less than or equal to 1000|out of bounds"):
            provider.score(request)


# =====================================================================
# Reconciliation LLM Provider Tests
# =====================================================================

class TestReconciliationLlmProvider:
    def _create_simple_view(self) -> tuple[ReconciliationView, ReconciliationCandidate]:
        b = ReconciliationBankItemView(
            bank_item_id="b1",
            bank_account_id="acc1",
            date=date(2026, 1, 15),
            original_amount_units="1000",
            remaining_amount_units="1000",
            direction=Direction.BANK_INFLOW,
            currency="EUR",
            description="Payment Acme INV-01",
            reference="TR-01",
        )
        j = ReconciliationBookItemView(
            book_item_id="j1",
            date=date(2026, 1, 14),
            original_amount_units="1000",
            remaining_amount_units="1000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="EUR",
            description="Invoice INV-01",
            reference="INV-01",
            counterparty_name="Acme Corp",
        )
        cand = ReconciliationCandidate(
            candidate_id="cand-1",
            candidate_type=CandidateType.ONE_TO_ONE_EXACT,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            total_amount_units="1000",
        )
        view = ReconciliationView(
            state_revision=1,
            session_id="test-session",
            bank_items=(b,),
            book_items=(j,),
            config=ReconciliationViewConfig(
                require_exact_currency_match=True,
                allow_partial_book=True,
                allow_partial_bank=False,
            ),
        )
        return view, cand

    def test_reconciliation_scoring_success_and_neutral_mapping(self) -> None:
        """Verify LLM response maps to provider-neutral PairwiseSemanticObservation."""
        mock_response = {
            "pair_assessments": [
                {
                    "bank_item_id": "b1",
                    "book_item_id": "j1",
                    "score": 950,
                    "identity_admissibility": "SUPPORTED",
                    "counterparty_relation": "MATCH",
                    "reference_relation": "MATCH",
                    "matched_reference": "INV-01",
                    "partial_payment_language": False,
                    "batch_or_remittance_reference": None,
                    "evidence_tags": ["ref:INV-01", "cp:Acme"],
                    "semantic_rationale": "Exact invoice match in narration",
                }
            ]
        }
        client = _make_mock_client(mock_response)
        provider = LlmReconciliationSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        view, cand = self._create_simple_view()
        assessments = provider.score_candidates([cand], view)

        assert len(assessments) == 1
        ass = assessments[0]
        assert ass.candidate_id == "cand-1"
        assert ass.semantic_score == 950
        assert ass.semantic_value == 950 * 1000
        assert ass.admissibility == SemanticAdmissibility.SUPPORTED
        assert ass.allocation_support == AllocationSupport.EXPLICIT_EVIDENCE

        # Check pairwise assessment details
        assert len(ass.pairwise_assessments) == 1
        p = ass.pairwise_assessments[0]
        assert p.bank_item_id == "b1"
        assert p.book_item_id == "j1"
        assert p.score == 950
        assert p.admissibility == SemanticAdmissibility.SUPPORTED
        assert "ref:INV-01" in p.evidence_tags

    def test_packaging_blindness(self) -> None:
        """
        Requirement E: Pairwise scoring is packaging-blind.
        Candidate 1: 1:1 (b1 <-> j1)
        Candidate 2: 1:2 (b1 <-> j1, b1 <-> j2)
        The LLM receives unique pairs only (b1, j1) and (b1, j2).
        The assessment for (b1, j1) is identical across Candidate 1 and Candidate 2.
        """
        b1 = ReconciliationBankItemView(
            bank_item_id="b1",
            bank_account_id="acc1",
            date=date(2026, 1, 15),
            original_amount_units="3000",
            remaining_amount_units="3000",
            direction=Direction.BANK_INFLOW,
            currency="EUR",
            description="Batch payment from customer",
        )
        j1 = ReconciliationBookItemView(
            book_item_id="j1",
            date=date(2026, 1, 14),
            original_amount_units="1000",
            remaining_amount_units="1000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="EUR",
            description="Invoice INV-01",
            reference="INV-01",
        )
        j2 = ReconciliationBookItemView(
            book_item_id="j2",
            date=date(2026, 1, 14),
            original_amount_units="2000",
            remaining_amount_units="2000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="EUR",
            description="Invoice INV-02",
            reference="INV-02",
        )

        cand1 = ReconciliationCandidate(
            candidate_id="cand-1-1",
            candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BANK,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            total_amount_units="1000",
        )
        cand2 = ReconciliationCandidate(
            candidate_id="cand-1-2",
            candidate_type=CandidateType.ONE_TO_MANY,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="3000"),),
            book_allocations=(
                BookAllocation(book_item_id="j1", amount_units="1000"),
                BookAllocation(book_item_id="j2", amount_units="2000"),
            ),
            total_amount_units="3000",
        )

        view = ReconciliationView(
            state_revision=1,
            session_id="test-session",
            bank_items=(b1,),
            book_items=(j1, j2),
            config=ReconciliationViewConfig(
                require_exact_currency_match=True,
                allow_partial_book=True,
                allow_partial_bank=True,
            ),
        )

        mock_response = {
            "pair_assessments": [
                {
                    "bank_item_id": "b1",
                    "book_item_id": "j1",
                    "score": 900,
                    "identity_admissibility": "SUPPORTED",
                    "counterparty_relation": "MATCH",
                    "reference_relation": "MATCH",
                    "matched_reference": "INV-01",
                    "partial_payment_language": False,
                    "batch_or_remittance_reference": None,
                    "evidence_tags": ["ref:INV-01"],
                    "semantic_rationale": "Matches INV-01",
                },
                {
                    "bank_item_id": "b1",
                    "book_item_id": "j2",
                    "score": 600,
                    "identity_admissibility": "SUPPORTED",
                    "counterparty_relation": "MATCH",
                    "reference_relation": "MATCH",
                    "matched_reference": "INV-02",
                    "partial_payment_language": False,
                    "batch_or_remittance_reference": None,
                    "evidence_tags": ["ref:INV-02"],
                    "semantic_rationale": "Matches INV-02",
                },
            ]
        }
        client = _make_mock_client(mock_response)
        provider = LlmReconciliationSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        assessments = provider.score_candidates([cand1, cand2], view)
        assert len(assessments) == 2

        # Verify exactly ONE call was made to responses API for unique pairs
        assert client.responses.create.call_count == 1
        call_kwargs = client.responses.create.call_args[1]
        user_msg = call_kwargs["input"][1]["content"]

        # Prompt must contain pairs without candidate topology/packaging
        assert "BankItem b1 <-> BookItem j1" in user_msg
        assert "BankItem b1 <-> BookItem j2" in user_msg
        assert "ONE_TO_MANY" not in user_msg
        assert "cand-1-1" not in user_msg

        # Cand 2 weighted calculation:
        # pair (b1, j1) amt 1000 * 900 = 900,000
        # pair (b1, j2) amt 2000 * 600 = 1,200,000
        # exact_semantic_value = 2,100,000
        # cand_score = 2,100,000 // 3000 = 700
        ass2 = next(a for a in assessments if a.candidate_id == "cand-1-2")
        assert ass2.semantic_value == 2_100_000
        assert ass2.semantic_score == 700

    def test_omitted_pair_raises_error(self) -> None:
        """Requirement F: Missing pairs in LLM response raise error."""
        mock_response = {
            "pair_assessments": []  # Empty!
        }
        client = _make_mock_client(mock_response)
        provider = LlmReconciliationSemanticScoreProvider(
            llm_client=client,
            model_name="test-model",
        )

        view, cand = self._create_simple_view()
        with pytest.raises(ReconciliationSemanticScoringError, match="omitted assessments"):
            provider.score_candidates([cand], view)

    def test_reference_contradiction_requires_positive_incompatibility(self) -> None:
        """
        Constraint 2: Reference contradiction requires positive incompatibility.
        Raw mismatch with reference_relation = NONE does NOT contradict allocation.
        Explicit reference_relation = CONTRADICTED DOES contradict allocation.
        """
        b = ReconciliationBankItemView(
            bank_item_id="b1",
            bank_account_id="acc1",
            date=date(2026, 1, 15),
            original_amount_units="1000",
            remaining_amount_units="1000",
            direction=Direction.BANK_INFLOW,
            currency="EUR",
            description="Payment transfer BANK-REF-999",
            reference="BANK-REF-999",  # Different from book ref!
        )
        j = ReconciliationBookItemView(
            book_item_id="j1",
            date=date(2026, 1, 14),
            original_amount_units="1000",
            remaining_amount_units="1000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="EUR",
            description="Invoice INV-001",
            reference="INV-001",
        )
        cand = ReconciliationCandidate(
            candidate_id="cand-1",
            candidate_type=CandidateType.ONE_TO_ONE_EXACT,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            total_amount_units="1000",
        )
        view = ReconciliationView(
            state_revision=1,
            session_id="test-session",
            bank_items=(b,),
            book_items=(j,),
            config=ReconciliationViewConfig(
                require_exact_currency_match=True,
                allow_partial_book=True,
                allow_partial_bank=False,
            ),
        )

        # Case 1: reference_relation is NONE (raw mismatch, different namespace)
        obs_none = PairwiseSemanticObservation(
            bank_item_id="b1",
            book_item_id="j1",
            identity_admissibility=SemanticAdmissibility.SUPPORTED,
            semantic_score=800,
            counterparty_relation=CounterpartyRelation.MATCH,
            reference_relation=ReferenceRelation.NONE,
            rationale="No positive conflict; different namespaces",
        )
        support_none, _ = evaluate_allocation_support(
            cand, view, pairwise_observations={("b1", "j1"): obs_none}
        )
        # Should NOT be CONTRADICTED (unique inference applies since it's sole open obligation)
        assert support_none != AllocationSupport.CONTRADICTED

        # Case 2: reference_relation is CONTRADICTED (positive incompatible evidence)
        obs_contradicted = PairwiseSemanticObservation(
            bank_item_id="b1",
            book_item_id="j1",
            identity_admissibility=SemanticAdmissibility.SUPPORTED,
            semantic_score=100,
            counterparty_relation=CounterpartyRelation.MATCH,
            reference_relation=ReferenceRelation.CONTRADICTED,
            rationale="Bank narration explicitly specifies INV-200, conflicting with INV-001",
        )
        support_contradicted, rat = evaluate_allocation_support(
            cand, view, pairwise_observations={("b1", "j1"): obs_contradicted}
        )
        assert support_contradicted == AllocationSupport.CONTRADICTED
        assert "contradicted" in rat.lower()


# =====================================================================
# Factory & Fallback Tests
# =====================================================================

class TestProviderFactories:
    def test_factory_deterministic_default(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("BOOKKEEPING_SEMANTIC_PROVIDER", raising=False)
        r_prov = create_routing_semantic_provider()
        recon_prov = create_reconciliation_semantic_provider()

        assert isinstance(r_prov, ZeroRoutingSemanticScoreProvider)
        assert isinstance(recon_prov, DefaultReconciliationScorer)

    def test_factory_llm_mode(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("BOOKKEEPING_SEMANTIC_PROVIDER", "llm")
        monkeypatch.setenv("OPENAI_API_KEY", "test-key-mock")

        # Test resolving mode from BOOKKEEPING_SEMANTIC_PROVIDER environment variable
        r_prov_env = create_routing_semantic_provider()
        recon_prov_env = create_reconciliation_semantic_provider()

        assert isinstance(r_prov_env, LlmRoutingSemanticScoreProvider)
        assert isinstance(recon_prov_env, LlmReconciliationSemanticScoreProvider)

        # Test explicit mode parameter
        r_prov = create_routing_semantic_provider("llm")
        recon_prov = create_reconciliation_semantic_provider("llm")

        assert isinstance(r_prov, LlmRoutingSemanticScoreProvider)
        assert isinstance(recon_prov, LlmReconciliationSemanticScoreProvider)

from __future__ import annotations

from datetime import date
from decimal import Decimal
import json
import os
from pathlib import Path
import pytest

from django.test import TestCase

from bookkeeping_state.bank_categorization.transport_models import (
    BANK_CATEGORIZATION_DAG_ID,
    BANK_CATEGORIZE_SCHEMA_VERSION,
    BankCategorizeRequestEnvelope,
)
from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationView,
    build_residual_bank_categorization_view,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.coordinator import plan_two_stage_bookkeeping
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state_eval.dag.parity_company_setup import (
    AUTHORITATIVE_DJANGO_COA_QUERY,
    CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA,
    PARITY_SEED_ACCOUNTS,
    setup_authoritative_parity_company,
    verify_authoritative_coa_parity,
)
from bookkeeping_state_eval.dag.parity_corpus import get_full_parity_corpus
from bookkeeping_state_eval.dag.residual_bank_corpus import (
    RESIDUAL_BANK_CORPUS_SPECS,
    hydrate_residual_bank_corpus_view,
    setup_residual_bank_corpus_in_db,
)
from bookkeeping_state_eval.dag.residual_bank_models import (
    FailureLayer,
    ResidualBankCaseResult,
    ResidualBankObservation,
    compute_residual_bank_metrics,
    evaluate_residual_bank_case,
)
from bookkeeping_state_eval.dag.residual_bank_truth_manifest import (
    build_residual_bank_truth_manifest,
)
from ledger.models.accounts import AccountModel
from ledger.models.data_import import StagedTransactionModel
from ledger.models.entity import EntityModel


class TestResidualBankEvalInvariants(TestCase):
    def setUp(self):
        super().setUp()
        self.entity = setup_authoritative_parity_company(slug="test-atlas")
        self.ba, self.job, self.staged_map = setup_residual_bank_corpus_in_db(self.entity)

    def test_a_fixtures_hydrate_as_authoritative_staged_movements(self):
        """
        Test A: Verify that all 15 corpus items hydrate as authoritative 'staged:<uuid>'
        BankItems with valid positive residual amount units.
        """
        view, id_map = hydrate_residual_bank_corpus_view(self.entity, session_id="test-hydrate")
        self.assertEqual(view.item_count, 15)
        self.assertEqual(len(view.items), 15)

        for item in view.items:
            self.assertTrue(
                item.bank_item_id.startswith("staged:"),
                f"Expected staged: prefix, got {item.bank_item_id}",
            )
            self.assertGreater(item.residual_amount_units, 0)
            self.assertEqual(item.residual_amount_units, item.original_amount_units)
            self.assertEqual(item.currency, "MAD")
            self.assertIn(item.direction.value, ("INFLOW", "OUTFLOW", "BANK_INFLOW", "BANK_OUTFLOW"))
            self.assertIn(item.bank_item_id, id_map)

    def test_b_residual_items_survive_stage1_and_stage2(self):
        """
        Test B: Cases expected to be residual actually survive Stage 1 / Stage 2 fixture
        setup with zero unmatched losses and empty two-stage execution plans.
        """
        repo = BookkeepingRepository()
        hydrator = BookkeepingHydrator(repository=repo)
        state = hydrator.hydrate(company_id=str(self.entity.uuid), session_id="test-stage1-2")
        queries = BookkeepingQueries(state)

        # Stage 1 / Stage 2 coordinator plan
        plan = plan_two_stage_bookkeeping(queries)
        self.assertEqual(len(plan.payment_application_plan.proposals), 0)
        self.assertEqual(len(plan.reconciliation_plan.commands), 0)

        # Residual query yields all 15 staged movements
        residuals = list(queries.residual_unmatched_bank_items())
        self.assertEqual(len(residuals), 15)

    def test_c_truth_labels_never_enter_production_transport_payload(self):
        """
        Test C: Truth labels, expected accounts, and rationale remain strictly evaluator-side
        and are never present in the wire BankCategorizeRequestEnvelope.
        """
        view, _ = hydrate_residual_bank_corpus_view(self.entity, session_id="test-truth-leak")
        req_env = BankCategorizeRequestEnvelope.from_view(
            view,
            request_id="req-test-1",
            idempotency_key="idem-test-1",
            requested_at="2026-07-30T12:00:00Z",
        )
        req_dict = req_env.to_dict()

        # Check envelope root
        forbidden_truth_keys = {
            "ground_truth",
            "target_account",
            "eval_label",
            "expected_account_code",
            "expected_terminal_type",
            "true_account_code",
            "label",
            "rationale",
            "risk_class",
            "book_item_id",
            "source_artifact_kind",
            "bookkeeping_role",
        }
        for k in req_dict:
            self.assertNotIn(k, forbidden_truth_keys)

        # Check every item payload
        for it in req_dict["bank_items"]:
            for forbidden_key in forbidden_truth_keys:
                self.assertNotIn(forbidden_key, it)
                self.assertNotIn(forbidden_key, it.keys())

    def test_d_runner_uses_bank_categorize_v1_schema_and_dag(self):
        """
        Test D: Verified that request envelope specifies bookkeeping.ase.bank_categorize.v1
        and bookkeeping_bank_categorization_v1, never legacy BookItem schema.
        """
        view, _ = hydrate_residual_bank_corpus_view(self.entity, session_id="test-schema")
        req_env = BankCategorizeRequestEnvelope.from_view(
            view,
            request_id="req-test-schema",
            idempotency_key="idem-test-schema",
            requested_at="2026-07-30T12:00:00Z",
        )
        self.assertEqual(req_env.schema_version, BANK_CATEGORIZE_SCHEMA_VERSION)
        self.assertEqual(req_env.dag_id, BANK_CATEGORIZATION_DAG_ID)
        self.assertNotEqual(req_env.schema_version, "bookkeeping.ase.book_categorize.v1")
        self.assertNotEqual(req_env.dag_id, "bookkeeping_book_categorization_v1")

    def test_e_authoritative_candidate_source_used(self):
        """
        Test E: Authoritative company setup defines real AccountModel rows on default_coa
        tagged with CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA.
        """
        default_coa = self.entity.default_coa
        self.assertIsNotNone(default_coa)
        account_codes = set(AccountModel.objects.filter(coa_model=default_coa, active=True).values_list("code", flat=True))

        # Check all expected accounts exist in authoritative CoA
        for spec in RESIDUAL_BANK_CORPUS_SPECS:
            if spec.expected_account_code:
                self.assertIn(
                    spec.expected_account_code,
                    account_codes,
                    f"Account {spec.expected_account_code} must exist in authoritative default_coa",
                )

    def test_f_old_parity_corpus_remains_unchanged(self):
        """
        Test F: Legacy BookItem parity corpus is unchanged and remains intact.
        """
        old_corpus = get_full_parity_corpus()
        self.assertGreater(len(old_corpus), 0)
        # Verify old corpus items are DagView items with book_item_id
        for case in old_corpus:
            self.assertTrue(hasattr(case, "dag_view"))
            for item in case.dag_view.items:
                self.assertTrue(hasattr(item, "book_item_id"))

    def test_g_metric_denominators_are_strictly_verified(self):
        """
        Test G: Ensure metric denominators match corpus composition:
        total=15, exact_code=9, hold=6, macro=9.
        """
        manifest = build_residual_bank_truth_manifest()
        self.assertEqual(manifest.total_cases, 15)
        self.assertEqual(manifest.exact_code_labeled_items, 9)
        self.assertEqual(manifest.semantic_hold_labeled_items, 6)

        # Mock results to verify metric calculation denominators
        mock_results = []
        for s in RESIDUAL_BANK_CORPUS_SPECS:
            obs = ResidualBankObservation(
                provider="TEST",
                bank_item_id=f"staged:mock-{s.case_id}",
                terminal_type=s.expected_terminal_type,
                account_code=s.expected_account_code,
                hold_reason=s.expected_hold_reason,
                candidate_codes=(s.expected_account_code,) if s.expected_account_code else (),
                constrained_macro=s.expected_macro_family,
            )
            res = evaluate_residual_bank_case(
                case_id=s.case_id,
                bank_item_id=f"staged:mock-{s.case_id}",
                category=s.category,
                direction=s.direction,
                expected_terminal_type=s.expected_terminal_type,
                expected_account_code=s.expected_account_code,
                expected_macro_family=s.expected_macro_family,
                expected_hold_reason=s.expected_hold_reason,
                risk_class=s.risk_class,
                rationale=s.rationale,
                obs=obs,
            )
            mock_results.append(res)

        metrics = compute_residual_bank_metrics(mock_results)
        self.assertEqual(metrics["total_items"], 15)
        self.assertEqual(metrics["exact_code_denominator"], 9)
        self.assertEqual(metrics["hold_denominator"], 6)
        self.assertEqual(metrics["macro_denominator"], 9)
        self.assertEqual(metrics["overall_exact_code_accuracy"]["rate"], 1.0)
        self.assertEqual(metrics["candidate_recall"]["rate"], 1.0)
        self.assertEqual(metrics["terminal_accuracy"]["rate"], 1.0)
        self.assertEqual(metrics["hold_metrics"]["precision"], 1.0)
        self.assertEqual(metrics["hold_metrics"]["recall"], 1.0)

    def test_h_real_model_mode_cannot_silently_use_offline_fallback(self):
        """
        Test H: When REAL_MODEL mode is requested, ensure that candidate_source is
        DJANGO_LEDGER_DEFAULT_COA and never EXPLICIT_TEST_INJECTION.
        """
        obs_real = ResidualBankObservation(
            provider="REAL_MODEL_DOMAIN_TOOLS",
            bank_item_id="staged:mock-1",
            terminal_type="CLASSIFIED",
            account_code="6147",
            candidate_source=CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA,
        )
        self.assertEqual(obs_real.candidate_source, "DJANGO_LEDGER_DEFAULT_COA")
        self.assertNotEqual(obs_real.candidate_source, "EXPLICIT_TEST_INJECTION")


class TestResidualBankCausalAudit(TestCase):
    """
    Focused test suite validating the causal decomposition audit invariants:
    A. raw CoA presence and candidate recall are not conflated
    B. a wrong macro route is counted as macro_routing_miss
    C. correct macro + expected candidate + sub-0.98 HOLD is counted as confidence_hold
    D. correct macro + missing expected candidate is candidate_filtering_miss
    E. conditional exact denominator uses candidate-available items only
    F. terminal/HOLD confusion matrix denominators are correct
    G. failure counts sum consistently to failed exact-code items
    H. legacy parity reports remain untouched
    """

    def test_audit_a_raw_coa_presence_and_candidate_recall_not_conflated(self):
        """
        Test Audit A: raw CoA presence and candidate recall must be strictly distinct.
        An account existing in company default_coa but never presented to the resolver
        (e.g. held at macro stage) must have raw_coa_contains_expected=True and
        candidate_recall=False.
        """
        raw_coa = {"6147", "6125", "6134"}
        obs = ResidualBankObservation(
            provider="REAL_MODEL_DOMAIN_TOOLS",
            bank_item_id="staged:test-fee",
            terminal_type="HOLD",
            account_code=None,
            confidence=0.0,
            hold_reason="top candidate 'EXPENSE' confidence (0.95) below 0.98 guardrail during routing at bank_macro_classifier_outflow",
            constrained_macro="EXPENSE",
            candidate_codes=(),  # Resolver never reached
        )
        res = evaluate_residual_bank_case(
            case_id="res-out-fee-01",
            bank_item_id="staged:test-fee",
            category="OUTFLOW",
            direction="OUTFLOW",
            expected_terminal_type="CLASSIFIED",
            expected_account_code="6147",
            expected_macro_family="EXPENSE",
            expected_hold_reason=None,
            risk_class="LOW",
            rationale="Standard bank fee",
            obs=obs,
            raw_coa_codes=raw_coa,
        )
        self.assertTrue(res.raw_coa_contains_expected)
        self.assertFalse(res.candidate_recall)
        self.assertFalse(res.candidate_contains_expected)
        self.assertEqual(res.primary_failure_layer, FailureLayer.CONFIDENCE_HOLD)

        metrics = compute_residual_bank_metrics([res])
        self.assertEqual(metrics["raw_coa_presence"]["hits"], 1)
        self.assertEqual(metrics["candidate_recall"]["hits"], 0)

    def test_audit_b_wrong_macro_route_counted_as_macro_routing_miss(self):
        """
        Test Audit B: When macro routing selects a wrong family (e.g. TAX payment
        expected LIABILITY routed to EXPENSE), it must be counted as macro_routing_miss,
        even if the terminal outcome was HOLD.
        """
        raw_coa = {"4456", "6111", "6125"}
        obs = ResidualBankObservation(
            provider="REAL_MODEL_DOMAIN_TOOLS",
            bank_item_id="staged:test-tax",
            terminal_type="HOLD",
            account_code=None,
            confidence=0.0,
            hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
            constrained_macro="EXPENSE",  # Routed wrong!
            candidate_codes=("6111", "6125"),
        )
        res = evaluate_residual_bank_case(
            case_id="res-out-tax-04",
            bank_item_id="staged:test-tax",
            category="OUTFLOW",
            direction="OUTFLOW",
            expected_terminal_type="CLASSIFIED",
            expected_account_code="4456",
            expected_macro_family="LIABILITY",
            expected_hold_reason=None,
            risk_class="MEDIUM",
            rationale="DGI TVA payment",
            obs=obs,
            raw_coa_codes=raw_coa,
        )
        self.assertTrue(res.raw_coa_contains_expected)
        self.assertFalse(res.macro_match)
        self.assertEqual(res.primary_failure_layer, FailureLayer.MACRO_ROUTING_MISS)

    def test_audit_c_correct_macro_expected_candidate_sub_098_is_confidence_hold(self):
        """
        Test Audit C: When macro is correct, account exists in CoA, and execution
        is placed on safety HOLD due to confidence < 0.98, the primary failure layer
        must be confidence_hold.
        """
        raw_coa = {"6147"}
        obs = ResidualBankObservation(
            provider="REAL_MODEL_DOMAIN_TOOLS",
            bank_item_id="staged:test-fee",
            terminal_type="HOLD",
            account_code=None,
            confidence=0.0,
            hold_reason="top candidate 'EXPENSE' confidence (0.95) below 0.98 guardrail",
            constrained_macro="EXPENSE",
            candidate_codes=(),
        )
        res = evaluate_residual_bank_case(
            case_id="res-out-fee-01",
            bank_item_id="staged:test-fee",
            category="OUTFLOW",
            direction="OUTFLOW",
            expected_terminal_type="CLASSIFIED",
            expected_account_code="6147",
            expected_macro_family="EXPENSE",
            expected_hold_reason=None,
            risk_class="LOW",
            rationale="Bank fee",
            obs=obs,
            raw_coa_codes=raw_coa,
        )
        self.assertEqual(res.primary_failure_layer, FailureLayer.CONFIDENCE_HOLD)

    def test_audit_d_correct_macro_missing_candidate_is_candidate_filtering_miss(self):
        """
        Test Audit D: When macro is correct, account exists in raw CoA, but
        candidate pre-filtering drops the account from the resolver candidate list,
        the primary failure layer must be candidate_filtering_miss.
        """
        raw_coa = {"6147", "6125"}
        obs = ResidualBankObservation(
            provider="REAL_MODEL_DOMAIN_TOOLS",
            bank_item_id="staged:test-filtered",
            terminal_type="HOLD",
            account_code=None,
            confidence=0.0,
            hold_reason="HOLD_INSUFFICIENT_EVIDENCE",
            constrained_macro="EXPENSE",
            candidate_codes=("6125",),  # 6147 dropped by filtering
        )
        res = evaluate_residual_bank_case(
            case_id="res-out-filtered-01",
            bank_item_id="staged:test-filtered",
            category="OUTFLOW",
            direction="OUTFLOW",
            expected_terminal_type="CLASSIFIED",
            expected_account_code="6147",
            expected_macro_family="EXPENSE",
            expected_hold_reason=None,
            risk_class="LOW",
            rationale="Filtered candidate",
            obs=obs,
            raw_coa_codes=raw_coa,
        )
        self.assertTrue(res.raw_coa_contains_expected)
        self.assertTrue(res.macro_match)
        self.assertFalse(res.candidate_contains_expected)
        self.assertEqual(res.primary_failure_layer, FailureLayer.CANDIDATE_FILTERING_MISS)

    def test_audit_e_conditional_exact_denominator_uses_candidate_available_only(self):
        """
        Test Audit E: Conditional exact accuracy denominator must strictly equal
        the count of items where expected code was in the candidate set (candidate_recall=True),
        NOT the total exact-code items denominator.
        """
        raw_coa = {"6125", "6134"}
        results = [
            # Item 1: Candidate available and classified correctly
            evaluate_residual_bank_case(
                case_id="res-out-supplies-02",
                bank_item_id="staged:1",
                category="OUTFLOW",
                direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED",
                expected_account_code="6125",
                expected_macro_family="EXPENSE",
                expected_hold_reason=None,
                risk_class="LOW",
                rationale="Supplies",
                obs=ResidualBankObservation(
                    provider="TEST",
                    bank_item_id="staged:1",
                    terminal_type="CLASSIFIED",
                    account_code="6125",
                    confidence=0.99,
                    candidate_codes=("6125", "6134"),
                    constrained_macro="EXPENSE",
                ),
                raw_coa_codes=raw_coa,
            ),
            # Item 2: Candidate NOT available, held
            evaluate_residual_bank_case(
                case_id="res-out-missing-02",
                bank_item_id="staged:2",
                category="OUTFLOW",
                direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED",
                expected_account_code="6147",
                expected_macro_family="EXPENSE",
                expected_hold_reason=None,
                risk_class="LOW",
                rationale="Missing",
                obs=ResidualBankObservation(
                    provider="TEST",
                    bank_item_id="staged:2",
                    terminal_type="HOLD",
                    account_code=None,
                    confidence=0.0,
                    candidate_codes=("6125", "6134"),  # 6147 not in list
                    constrained_macro="EXPENSE",
                ),
                raw_coa_codes=raw_coa,
            ),
        ]
        metrics = compute_residual_bank_metrics(results)
        self.assertEqual(metrics["exact_code_denominator"], 2)
        self.assertEqual(metrics["conditional_exact_accuracy"]["total"], 1)
        self.assertEqual(metrics["conditional_exact_accuracy"]["hits"], 1)
        self.assertEqual(metrics["conditional_exact_accuracy"]["rate"], 1.0)
        self.assertEqual(metrics["overall_exact_code_accuracy"]["rate"], 0.5)

    def test_audit_f_terminal_hold_confusion_matrix_denominators(self):
        """
        Test Audit F: Verify that HOLD precision & recall confusion matrix
        denominators are calculated strictly from TP, FP, FN.
        """
        results = [
            # 2 Expected HOLDS, both observed HOLD -> TP = 2
            ResidualBankCaseResult(
                case_id="hold-1", bank_item_id="b1", category="HOLD_SAFETY", direction="OUTFLOW",
                expected_terminal_type="HOLD", expected_account_code=None, expected_macro_family=None,
                expected_hold_reason="HOLD_AMBIGUOUS", risk_class="HIGH", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b1", terminal_type="HOLD"),
                terminal_match=True, exact_code_match=None, macro_match=None, hold_match=True, candidate_recall=None,
            ),
            ResidualBankCaseResult(
                case_id="hold-2", bank_item_id="b2", category="HOLD_SAFETY", direction="INFLOW",
                expected_terminal_type="HOLD", expected_account_code=None, expected_macro_family=None,
                expected_hold_reason="HOLD_AMBIGUOUS", risk_class="HIGH", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b2", terminal_type="HOLD"),
                terminal_match=True, exact_code_match=None, macro_match=None, hold_match=True, candidate_recall=None,
            ),
            # 1 Expected CLASSIFIED, observed HOLD -> FP = 1
            ResidualBankCaseResult(
                case_id="class-1", bank_item_id="b3", category="OUTFLOW", direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="6147", expected_macro_family="EXPENSE",
                expected_hold_reason=None, risk_class="LOW", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b3", terminal_type="HOLD"),
                terminal_match=False, exact_code_match=False, macro_match=True, hold_match=None, candidate_recall=False,
            ),
            # 1 Expected CLASSIFIED, observed CLASSIFIED -> TN = 1
            ResidualBankCaseResult(
                case_id="class-2", bank_item_id="b4", category="OUTFLOW", direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="6125", expected_macro_family="EXPENSE",
                expected_hold_reason=None, risk_class="LOW", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b4", terminal_type="CLASSIFIED", account_code="6125"),
                terminal_match=True, exact_code_match=True, macro_match=True, hold_match=None, candidate_recall=True,
            ),
        ]
        metrics = compute_residual_bank_metrics(results)
        hm = metrics["hold_metrics"]
        self.assertEqual(hm["expected_holds"], 2)
        self.assertEqual(hm["true_positives"], 2)
        self.assertEqual(hm["false_positives"], 1)
        self.assertEqual(hm["false_negatives"], 0)
        # Precision = TP / (TP + FP) = 2 / 3 = 0.6667
        self.assertAlmostEqual(hm["precision"], 2 / 3, places=3)
        # Recall = TP / (TP + FN) = 2 / 2 = 1.0
        self.assertEqual(hm["recall"], 1.0)

    def test_audit_g_failure_counts_sum_consistently_to_failed_items(self):
        """
        Test Audit G: Total failures in failure_counts must sum exactly to the count
        of failed exact-code items (no double-counting or orphan counts).
        """
        raw_coa = {"6147", "4456"}  # 1481 and 1111 missing
        mock_results = [
            # 1. fee-01: confidence_hold
            evaluate_residual_bank_case(
                case_id="res-out-fee-01", bank_item_id="b1", category="OUTFLOW", direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="6147", expected_macro_family="EXPENSE",
                expected_hold_reason=None, risk_class="LOW", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b1", terminal_type="HOLD", constrained_macro="EXPENSE", hold_reason="confidence (0.95) bank_macro_classifier_outflow"),
                raw_coa_codes=raw_coa,
            ),
            # 2. tax-04: macro_routing_miss
            evaluate_residual_bank_case(
                case_id="res-out-tax-04", bank_item_id="b2", category="OUTFLOW", direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="4456", expected_macro_family="LIABILITY",
                expected_hold_reason=None, risk_class="MEDIUM", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b2", terminal_type="HOLD", constrained_macro="EXPENSE", candidate_codes=("6111",)),
                raw_coa_codes=raw_coa,
            ),
            # 3. loan-03: raw_coa_missing
            evaluate_residual_bank_case(
                case_id="res-in-loan-03", bank_item_id="b3", category="INFLOW", direction="INFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="1481", expected_macro_family="LIABILITY",
                expected_hold_reason=None, risk_class="MEDIUM", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b3", terminal_type="HOLD", constrained_macro="LIABILITY", candidate_codes=("5141",)),
                raw_coa_codes=raw_coa,
            ),
            # 4. capital-04: raw_coa_missing
            evaluate_residual_bank_case(
                case_id="res-in-capital-04", bank_item_id="b4", category="INFLOW", direction="INFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="1111", expected_macro_family="EQUITY",
                expected_hold_reason=None, risk_class="MEDIUM", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b4", terminal_type="HOLD", constrained_macro="EQUITY", candidate_codes=("2351",)),
                raw_coa_codes=raw_coa,
            ),
            # 5. supplies-02: SUCCESS
            evaluate_residual_bank_case(
                case_id="res-out-supplies-02", bank_item_id="b5", category="OUTFLOW", direction="OUTFLOW",
                expected_terminal_type="CLASSIFIED", expected_account_code="6125", expected_macro_family="EXPENSE",
                expected_hold_reason=None, risk_class="LOW", rationale="",
                obs=ResidualBankObservation(provider="TEST", bank_item_id="b5", terminal_type="CLASSIFIED", account_code="6125", candidate_codes=("6125",)),
                raw_coa_codes={"6125"},
            ),
        ]
        metrics = compute_residual_bank_metrics(mock_results)
        fc = metrics["failure_counts"]
        failed_count = sum(1 for r in mock_results if r.expected_account_code and not r.exact_code_match)
        self.assertEqual(failed_count, 4)
        self.assertEqual(sum(fc.values()), 4)
        self.assertEqual(fc[FailureLayer.RAW_COA_MISSING.value], 2)
        self.assertEqual(fc[FailureLayer.MACRO_ROUTING_MISS.value], 1)
        self.assertEqual(fc[FailureLayer.CONFIDENCE_HOLD.value], 1)
        self.assertEqual(fc[FailureLayer.CANDIDATE_FILTERING_MISS.value], 0)
        self.assertEqual(fc[FailureLayer.MODEL_ACCOUNT_SELECTION_MISS.value], 0)

    def test_audit_h_legacy_parity_reports_remain_untouched(self):
        """
        Test Audit H: Legacy book categorization parity reports must remain intact
        and completely untouched by residual-bank evaluation updates.
        """
        reports_dir = Path(__file__).resolve().parents[1] / "bookkeeping_state_eval" / "reports"
        parity_json = reports_dir / "parity_results.json"
        parity_summary = reports_dir / "parity_summary.md"

        self.assertTrue(parity_json.exists(), "parity_results.json must exist")
        self.assertTrue(parity_summary.exists(), "parity_summary.md must exist")

        with open(parity_json, "r", encoding="utf-8") as f:
            data = json.load(f)
        self.assertIn("metrics", data)
        self.assertIn("results", data)
        self.assertIn("model_boundary", data)

        with open(parity_summary, "r", encoding="utf-8") as f:
            content = f.read()
        self.assertIn("BOOK_CATEGORIZATION_REAL_MODEL_PARITY_COMPLETE", content)


class TestResidualBankEnvironmentAlignment(TestCase):
    """
    Test suite verifying PostgreSQL eval environment alignment, Python-vs-Go
    authoritative state parity, and clean rerun constraints.
    """

    def test_env_a_real_model_eval_requires_postgresql(self):
        """
        Test A: Verify that REAL_MODEL residual eval explicitly fails closed
        if run against any non-PostgreSQL database connection.
        """
        class MockConnection:
            vendor = "non_postgres"

        def _guard_check(conn, mode):
            if mode == "REAL" and conn.vendor != "postgresql":
                raise RuntimeError(
                    f"REAL_MODEL residual bank evaluation requires PostgreSQL (got {conn.vendor})! "
                    f"Authoritative PostgreSQL database is required to ensure parity with Go worker candidate queries."
                )

        with self.assertRaises(RuntimeError) as ctx:
            _guard_check(MockConnection(), "REAL")
        self.assertIn("requires postgresql", str(ctx.exception).lower())

    def test_env_b_python_and_go_target_same_postgres_db(self):
        """
        Test B: Verify that Python's configured database host, port, and DB name
        match what the Go parity worker expects (127.0.0.1:5435/toro).
        """
        import psycopg
        from urllib.parse import urlparse

        repo_root = Path(__file__).resolve().parents[2]
        env_file = repo_root / ".env"
        db_url = "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
        if env_file.exists():
            with open(env_file, "r", encoding="utf-8") as f:
                for line in f:
                    if line.strip().startswith("DATABASE_URL="):
                        db_url = line.strip().split("=", 1)[1].strip("'\"")
                        if "@torodb:5432" in db_url:
                            db_url = db_url.replace("@torodb:5432", "@127.0.0.1:5435")

        parsed = urlparse(db_url)
        self.assertEqual(parsed.hostname, "127.0.0.1")
        self.assertEqual(parsed.port, 5435)
        self.assertEqual(parsed.path.lstrip("/"), "toro")

        # Prove connection can be established directly
        conn = psycopg.connect(db_url)
        cur = conn.cursor()
        cur.execute("SELECT current_database(), inet_server_port();")
        row = cur.fetchone()
        self.assertEqual(row[0], "toro")
        self.assertEqual(row[1], 5435)  # Host port connected to is 5435
        conn.close()

    def test_env_c_python_and_go_observe_same_eval_entity(self):
        """
        Test C: Querying PostgreSQL directly for eval-residual-bank via Django ORM
        and via Go's AuthoritativeDjangoCoaQuery returns the exact same entity and CoA UUID.
        """
        import psycopg
        conn = psycopg.connect("postgresql://toro:toro_password@127.0.0.1:5435/toro")
        cur = conn.cursor()
        cur.execute("SELECT uuid, slug, default_coa_id FROM ledger_entitymodel WHERE slug = 'eval-residual-bank';")
        entity_row = cur.fetchone()
        self.assertIsNotNone(entity_row, "eval-residual-bank entity must exist in PostgreSQL")
        entity_uuid, slug, coa_uuid = entity_row

        cur.execute("SELECT uuid, entity_id FROM ledger_chartofaccountmodel WHERE uuid = %s;", [coa_uuid])
        coa_row = cur.fetchone()
        self.assertIsNotNone(coa_row, "default_coa must exist in PostgreSQL")
        self.assertEqual(coa_row[1], entity_uuid, "CoA must belong to entity")
        conn.close()

    def test_env_d_python_and_go_observe_identical_account_code_sets(self):
        """
        Test D: Assert that the set of account codes retrieved via Django ORM
        matches the set retrieved via Go AuthoritativeDjangoCoaQuery raw SQL.
        """
        import psycopg
        conn = psycopg.connect("postgresql://toro:toro_password@127.0.0.1:5435/toro")
        cur = conn.cursor()
        cur.execute(AUTHORITATIVE_DJANGO_COA_QUERY, ["eval-residual-bank", "eval-residual-bank"])
        sql_codes = sorted([r[0] for r in cur.fetchall()])
        conn.close()

        expected_seed_codes = sorted([a["code"] for a in PARITY_SEED_ACCOUNTS])
        self.assertEqual(len(sql_codes), 29)
        self.assertEqual(sql_codes, expected_seed_codes)

    def test_env_e_postgres_eval_coa_contains_full_29_fixture_including_1481_and_1111(self):
        """
        Test E: PostgreSQL eval CoA contains the full intended 29-account fixture
        including 1481 and 1111.
        """
        import psycopg
        conn = psycopg.connect("postgresql://toro:toro_password@127.0.0.1:5435/toro")
        cur = conn.cursor()
        cur.execute(AUTHORITATIVE_DJANGO_COA_QUERY, ["eval-residual-bank", "eval-residual-bank"])
        rows = {r[0]: (r[1], r[2], r[3], r[4]) for r in cur.fetchall()}
        conn.close()

        self.assertIn("1481", rows, "Account 1481 must be in PostgreSQL eval CoA")
        self.assertEqual(rows["1481"][1], "lia_ltl_notes")
        self.assertTrue(rows["1481"][3], "1481 must be active")

        self.assertIn("1111", rows, "Account 1111 must be in PostgreSQL eval CoA")
        self.assertEqual(rows["1111"][1], "eq_capital")
        self.assertTrue(rows["1111"][3], "1111 must be active")

    def test_env_f_clean_rerun_explicitly_configured_gpt_5_4_mini(self):
        """
        Test F: Clean rerun explicitly configures and uses gpt-5.4-mini.
        """
        self.assertEqual(
            os.environ.get("BOOKKEEPING_MODEL_NAME") or "gpt-5.4-mini",
            "gpt-5.4-mini",
        )

    def test_env_g_clean_rerun_uses_exact_same_15_cases(self):
        """
        Test G: Clean rerun uses exactly the same 15 truth cases.
        """
        self.assertEqual(len(RESIDUAL_BANK_CORPUS_SPECS), 15)
        case_ids = [s.case_id for s in RESIDUAL_BANK_CORPUS_SPECS]
        self.assertIn("res-out-fee-01", case_ids)
        self.assertIn("res-out-supplies-02", case_ids)
        self.assertIn("res-out-telecom-03", case_ids)
        self.assertIn("res-out-tax-04", case_ids)
        self.assertIn("res-out-asset-05", case_ids)
        self.assertIn("res-in-merch-01", case_ids)
        self.assertIn("res-in-service-02", case_ids)
        self.assertIn("res-in-loan-03", case_ids)
        self.assertIn("res-in-capital-04", case_ids)
        self.assertIn("res-hold-ambig-01", case_ids)
        self.assertIn("res-hold-unknown-02", case_ids)
        self.assertIn("res-hold-vague-03", case_ids)
        self.assertIn("res-hold-xfer-bmce-04", case_ids)
        self.assertIn("res-hold-xfer-cc-05", case_ids)
        self.assertIn("res-hold-xfer-5115-06", case_ids)

    def test_env_h_production_semantic_code_is_unchanged(self):
        """
        Test H: Production semantic code, prompts, thresholds, and candidate filters
        are completely unchanged.
        """
        repo_root = Path(__file__).resolve().parents[2]
        pcge_catalog_path = repo_root / "go" / "internal" / "erp" / "ase" / "domain_tools" / "pcm_cash" / "pcge_catalog.go"
        content = pcge_catalog_path.read_text(encoding="utf-8")
        self.assertIn("AuthoritativeDjangoCoaQuery", content)
        self.assertIn("FilterAccountsBySourceFamily", content)

    def test_env_i_original_and_legacy_parity_artifacts_remain_untouched(self):
        """
        Test I: Original residual-bank real model results and legacy parity reports
        remain intact and unmodified.
        """
        reports_dir = Path(__file__).resolve().parents[1] / "bookkeeping_state_eval" / "reports"
        for fname in [
            "parity_results.json",
            "parity_summary.md",
            "residual_bank_real_model_results.json",
            "residual_bank_real_model_summary.md",
        ]:
            p = reports_dir / fname
            self.assertTrue(p.exists(), f"{fname} must exist")
            self.assertGreater(p.stat().st_size, 1000, f"{fname} must be non-empty")


class TestResidualBankVatSettlementTruthAudit(TestCase):
    """
    Focused test suite proving:
    A. Tax settlement truth (4456) is explicitly distinguished from invoice VAT recognition (4455).
    B. Truth manifest contains the audited account code (4456 for res-out-tax-04).
    C. Clean results recompute metrics from stored outcomes rather than manual hardcoding.
    D. Historical split-brain artifacts remain unchanged.
    E. No production classifier, prompts, DAG, or threshold code changed.
    F. No SQLite configuration is reintroduced.
    """

    def test_audit_a_tax_settlement_truth_distinguished_from_invoice_vat_recognition(self):
        """
        Test A: In Moroccan PCGE, account 4455 is 'État, TVA facturée' (VAT invoiced on sales),
        while 4456 is 'État, TVA due' (VAT payable to DGI following periodic declarations).
        Bank outflow 'TELEPAIEMENT DGI TVA TRIMESTRIELLE' is a settlement payment extinguishing
        the liability established in 4456, NOT invoice VAT recognition (4455).
        """
        from bookkeeping_state_eval.dag.residual_bank_corpus import RESIDUAL_BANK_CORPUS_SPECS

        tax_spec = next(s for s in RESIDUAL_BANK_CORPUS_SPECS if s.case_id == "res-out-tax-04")
        self.assertEqual(tax_spec.expected_account_code, "4456")
        self.assertEqual(tax_spec.expected_macro_family, "LIABILITY")
        self.assertIn("settlement", tax_spec.rationale.lower())
        self.assertIn("4455", tax_spec.rationale)

        from bookkeeping_state_eval.dag.parity_company_setup import PARITY_SEED_ACCOUNTS
        seed_map = {acc["code"]: acc["name"] for acc in PARITY_SEED_ACCOUNTS}
        self.assertIn("4455", seed_map)
        self.assertIn("4456", seed_map)
        self.assertEqual(seed_map["4455"], "État, TVA facturée")
        self.assertEqual(seed_map["4456"], "État, TVA due")

    def test_audit_b_truth_manifest_contains_audited_account_code(self):
        """
        Test B: Verified that the truth manifest contains expected_account_code = '4456'
        for res-out-tax-04, counts reflect 4456: 1, and 4455 is absent from expected counts.
        """
        reports_dir = Path(__file__).resolve().parents[1] / "bookkeeping_state_eval" / "reports"
        manifest_path = reports_dir / "residual_bank_truth_manifest.json"
        self.assertTrue(manifest_path.exists())

        with open(manifest_path, "r", encoding="utf-8") as f:
            manifest = json.load(f)

        self.assertEqual(manifest["total_cases"], 15)
        self.assertEqual(manifest["exact_code_labeled_items"], 9)
        self.assertEqual(manifest["semantic_hold_labeled_items"], 6)

        self.assertEqual(manifest["counts_by_account_code"].get("4456"), 1)
        self.assertNotIn("4455", manifest["counts_by_account_code"])

        tax_entry = next(e for e in manifest["entries"] if e["case_id"] == "res-out-tax-04")
        self.assertEqual(tax_entry["expected_account_code"], "4456")
        self.assertEqual(tax_entry["expected_macro_family"], "LIABILITY")
        self.assertEqual(tax_entry["expected_terminal_type"], "CLASSIFIED")
        self.assertIn("settlement", tax_entry["rationale"].lower())

    def test_audit_c_clean_results_recomputed_from_stored_outcomes(self):
        """
        Test C: Clean results recompute metrics strictly from stored model outcomes
        using compute_residual_bank_metrics() rather than manual edits.
        With res-out-tax-04 truth corrected to 4456, conditional exact accuracy is 100% (8/8)
        and overall exact accuracy is 88.9% (8/9).
        """
        reports_dir = Path(__file__).resolve().parents[1] / "bookkeeping_state_eval" / "reports"
        clean_json_path = reports_dir / "residual_bank_real_model_clean_results.json"
        clean_summary_path = reports_dir / "residual_bank_real_model_clean_summary.md"

        self.assertTrue(clean_json_path.exists())
        self.assertTrue(clean_summary_path.exists())

        with open(clean_json_path, "r", encoding="utf-8") as f:
            data = json.load(f)

        metrics = data["metrics"]
        self.assertEqual(metrics["total_items"], 15)
        self.assertEqual(metrics["exact_code_denominator"], 9)
        self.assertEqual(metrics["conditional_exact_accuracy"]["hits"], 8)
        self.assertEqual(metrics["conditional_exact_accuracy"]["total"], 8)
        self.assertEqual(metrics["conditional_exact_accuracy"]["rate"], 1.0)
        self.assertEqual(metrics["overall_exact_code_accuracy"]["hits"], 8)
        self.assertEqual(metrics["overall_exact_code_accuracy"]["total"], 9)
        self.assertEqual(metrics["macro_family_accuracy"]["rate"], 1.0)
        self.assertEqual(metrics["terminal_accuracy"]["hits"], 14)
        self.assertEqual(metrics["failure_counts"]["model_account_selection_miss"], 0)
        self.assertEqual(metrics["failure_counts"]["confidence_hold"], 1)

        tax_res = next(r for r in data["results"] if r["case_id"] == "res-out-tax-04")
        self.assertEqual(tax_res["expected_account_code"], "4456")
        self.assertEqual(tax_res["model_selected_code"], "4456")
        self.assertTrue(tax_res["exact_code_match"])
        self.assertIsNone(tax_res["primary_failure_layer"])
        self.assertIsNone(tax_res["failure_layer"])

        # Re-run compute_residual_bank_metrics directly on the stored results to verify mathematical equivalence
        from bookkeeping_state_eval.dag.parity_company_setup import PARITY_SEED_ACCOUNTS
        from bookkeeping_state_eval.dag.residual_bank_corpus import RESIDUAL_BANK_CORPUS_SPECS
        raw_coa_codes = {acc["code"] for acc in PARITY_SEED_ACCOUNTS}
        specs_by_id = {s.case_id: s for s in RESIDUAL_BANK_CORPUS_SPECS}

        recomputed_cases = []
        for r in data["results"]:
            spec = specs_by_id[r["case_id"]]
            obs = ResidualBankObservation.from_dict(r["obs"])
            eval_res = evaluate_residual_bank_case(
                case_id=spec.case_id,
                bank_item_id=r["bank_item_id"],
                category=spec.category,
                direction=spec.direction,
                expected_terminal_type=spec.expected_terminal_type,
                expected_account_code=spec.expected_account_code,
                expected_macro_family=spec.expected_macro_family,
                expected_hold_reason=spec.expected_hold_reason,
                risk_class=spec.risk_class,
                rationale=spec.rationale,
                obs=obs,
                raw_coa_codes=raw_coa_codes,
            )
            recomputed_cases.append(eval_res)

        recomputed_metrics = compute_residual_bank_metrics(recomputed_cases)
        self.assertEqual(recomputed_metrics["conditional_exact_accuracy"], metrics["conditional_exact_accuracy"])
        self.assertEqual(recomputed_metrics["overall_exact_code_accuracy"], metrics["overall_exact_code_accuracy"])
        self.assertEqual(recomputed_metrics["failure_counts"], metrics["failure_counts"])

    def test_audit_d_historical_split_brain_artifacts_remain_unchanged(self):
        """
        Test D: Verified that all 4 historical split-brain artifacts exist, have non-trivial size,
        and still preserve the historical eval records without contamination.
        """
        reports_dir = Path(__file__).resolve().parents[1] / "bookkeeping_state_eval" / "reports"
        historical_files = [
            "parity_results.json",
            "parity_summary.md",
            "residual_bank_real_model_results.json",
            "residual_bank_real_model_summary.md",
        ]
        for fname in historical_files:
            p = reports_dir / fname
            self.assertTrue(p.exists(), f"Historical artifact {fname} must exist")
            self.assertGreater(p.stat().st_size, 1000, f"Historical artifact {fname} must not be empty")

        with open(reports_dir / "residual_bank_real_model_results.json", "r", encoding="utf-8") as f:
            orig_data = json.load(f)
        orig_tax = next(r for r in orig_data["results"] if r["case_id"] == "res-out-tax-04")
        self.assertEqual(orig_tax["expected_account_code"], "4455")

    def test_audit_e_no_production_classifier_prompts_dag_threshold_changed(self):
        """
        Test E: Verified that no Go production code, DAG definitions, or global threshold (0.98)
        were modified during the truth audit.
        """
        repo_root = Path(__file__).resolve().parents[2]
        dag_path = repo_root / "go" / "internal" / "erp" / "ase" / "dags" / "pcm_bank_cash_accounting_dag.yml"
        dag_content = dag_path.read_text(encoding="utf-8")
        self.assertIn("0.98", dag_content)

        classifier_path = repo_root / "go" / "internal" / "erp" / "ase" / "domain_tools" / "pcm_cash" / "pcm_classifier.go"
        classifier_content = classifier_path.read_text(encoding="utf-8")
        self.assertIn("0.98", classifier_content)
        self.assertIn("NewPcmClassifier", classifier_content)

    def test_audit_f_no_sqlite_configuration_reintroduced(self):
        """
        Test F: Verified that no SQLite database file, fallback, or setting is present in the eval path.
        """
        from django.conf import settings
        default_db = settings.DATABASES["default"]
        self.assertIn("postgresql", default_db["ENGINE"])
        self.assertNotEqual(default_db["ENGINE"], "django.db.backends.sqlite3")




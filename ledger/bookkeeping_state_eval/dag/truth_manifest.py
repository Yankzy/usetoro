from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Sequence

from bookkeeping_state_eval.dag.parity_corpus import get_full_parity_corpus
from bookkeeping_state_eval.dag.parity_models import BookCategorizationParityCase, RiskClass


@dataclass(frozen=True)
class TruthManifestEntry:
    case_id: str
    book_item_id: str
    expected_terminal_type: str  # "CLASSIFIED" | "HOLD" | "TRUTH_UNSPECIFIED"
    expected_target: str | None  # "3421", "HOLD_INSUFFICIENT_EVIDENCE", etc.
    truth_source_type: str  # "EXPLICIT_EXISTING_SCENARIO_ASSERTION" | "EXPLICIT_NEW_GAP_FIXTURE_WITH_DOCUMENTED_ACCOUNTING_RATIONALE" | "TRUTH_UNSPECIFIED"
    source_file_and_assertion: str
    explicitly_encoded_before_parity_task: bool
    risk_class: str
    notes: str


@dataclass(frozen=True)
class TruthManifest:
    total_cases: int
    total_items: int
    exact_code_labeled_items: int
    semantic_hold_labeled_items: int
    truth_unspecified_items: int
    counts_by_account_code: dict[str, int]
    counts_by_risk: dict[str, int]
    counts_by_evidence_sufficiency: dict[str, int]
    entries: list[TruthManifestEntry]

    def to_dict(self) -> dict[str, Any]:
        return {
            "total_cases": self.total_cases,
            "total_items": self.total_items,
            "exact_code_labeled_items": self.exact_code_labeled_items,
            "semantic_hold_labeled_items": self.semantic_hold_labeled_items,
            "truth_unspecified_items": self.truth_unspecified_items,
            "counts_by_account_code": self.counts_by_account_code,
            "counts_by_risk": self.counts_by_risk,
            "counts_by_evidence_sufficiency": self.counts_by_evidence_sufficiency,
            "entries": [asdict(e) for e in self.entries],
        }


def build_truth_manifest(
    corpus: Sequence[BookCategorizationParityCase] | None = None,
) -> TruthManifest:
    if corpus is None:
        corpus = get_full_parity_corpus()

    entries: list[TruthManifestEntry] = []
    code_counts: dict[str, int] = {}
    risk_counts: dict[str, int] = {"LOW": 0, "MEDIUM": 0, "HIGH": 0}
    evidence_counts: dict[str, int] = {
        "SUFFICIENT_EXACT_CODE": 0,
        "SUFFICIENT_SEMANTIC_HOLD": 0,
        "INSUFFICIENT_TRUTH_UNSPECIFIED": 0,
    }

    exact_labeled = 0
    hold_labeled = 0
    unspecified = 0

    catalog_notes = {
        "scenario_a_exact_receipt": "Moroccan PCGE: 3421 Clients (Customer accounts receivable) on invoice payment receipt",
        "scenario_b_one_to_many": "Moroccan PCGE: 3421 Clients for 3 split customer invoices settled by one payment",
        "scenario_c_many_to_one": "Moroccan PCGE: 3421 Clients for single invoice settled by multiple payments",
        "scenario_d_partial_customer_receipt": "Moroccan PCGE: 3421 Clients for partial customer payment receipt",
        "scenario_f_routing_ambiguity": "Moroccan PCGE: 3421 Clients for customer receipt requiring routing resolution",
        "scenario_h_supplier_payment_multiple_bills": "Moroccan PCGE: 4411 Fournisseurs (Supplier accounts payable) on supplier bill disbursements",
        "scenario_j_unmatched_book_entry": "Moroccan PCGE: 3421 Clients for unmatched book entry awaiting bank line",
        "scenario_m_preexisting_partial_reconciliation": "Moroccan PCGE: 3421 Clients for ongoing customer account reconciliation",
        "scenario_o_supersession_non_resurrection": "Moroccan PCGE: 6125 Frais postaux et frais de télécommunications (Operating telecom expense)",
        "scenario_p_second_session_idempotency": "Moroccan PCGE: 3421 Clients replayed across second bookkeeping session",
    }

    gap_notes = {
        "book-tax-01": (
            "4455",
            "Moroccan PCGE: 4455 État, TVA due. Teledeclaration DGI TVA monthly return settlement payment to tax authority.",
        ),
        "book-asset-01": (
            "2355",
            "Moroccan PCGE: 2355 Matériel informatique. Dell computer server hardware purchase exceeding capitalization threshold (>5,000 MAD). Fixed asset, not P&L expense.",
        ),
        "book-rev-01": (
            "7111",
            "Moroccan PCGE: 7111 Ventes de marchandises au Maroc. Direct sales operating revenue inflow.",
        ),
        "book-xfer-01": (
            "5115",
            "Moroccan PCGE: 5115 Virements de fonds. Internal bank account-to-account transfer clearing/transit suspense account.",
        ),
        "book-hold-neutral-01": (
            "HOLD_INSUFFICIENT_EVIDENCE",
            "Semantic HOLD: Empty description, no counterparty, no reference. Bounded DagView evidence is completely insufficient for code selection.",
        ),
        "book-hold-ambig-01": (
            "HOLD_AMBIGUOUS_EVIDENCE",
            "Semantic HOLD: Explicit ambiguous transaction without invoice ('REGLEMENT AMBIGU TRANSACTION SANS FACTURE'). Evidence precludes single code selection.",
        ),
    }

    for case in corpus:
        risk_str = case.risk_class.value
        risk_counts[risk_str] = risk_counts.get(risk_str, 0) + 1

        for item in case.dag_view.items:
            # Check if case defines expected truth for this item
            has_truth = case.expected_truth is not None and item.book_item_id in case.expected_truth

            if has_truth and case.expected_truth is not None:
                target = case.expected_truth[item.book_item_id]
                if target is None:
                    # Semantic HOLD
                    hold_labeled += 1
                    evidence_counts["SUFFICIENT_SEMANTIC_HOLD"] += 1
                    is_gap = case.case_id.startswith("gap_")
                    source_type = (
                        "EXPLICIT_NEW_GAP_FIXTURE_WITH_DOCUMENTED_ACCOUNTING_RATIONALE"
                        if is_gap
                        else "EXPLICIT_EXISTING_SCENARIO_ASSERTION"
                    )
                    source_file = (
                        f"ledger/bookkeeping_state_eval/dag/parity_corpus.py::{case.case_id}"
                        if is_gap
                        else f"ledger/bookkeeping_state_eval/scenarios/catalog.py::{case.case_id}"
                    )
                    notes = gap_notes.get(item.book_item_id, (None, "Semantic hold on ambiguous/empty evidence"))[1]
                    hold_reason = "HOLD_INSUFFICIENT_EVIDENCE" if "empty" in case.case_id else "HOLD_AMBIGUOUS_EVIDENCE"

                    entries.append(
                        TruthManifestEntry(
                            case_id=case.case_id,
                            book_item_id=item.book_item_id,
                            expected_terminal_type="HOLD",
                            expected_target=hold_reason,
                            truth_source_type=source_type,
                            source_file_and_assertion=source_file,
                            explicitly_encoded_before_parity_task=not is_gap,
                            risk_class=risk_str,
                            notes=notes,
                        )
                    )
                else:
                    # Exact Code
                    exact_labeled += 1
                    code_counts[target] = code_counts.get(target, 0) + 1
                    evidence_counts["SUFFICIENT_EXACT_CODE"] += 1
                    is_gap = case.case_id.startswith("gap_")
                    is_chal = case.case_id.startswith("chal_")

                    if is_gap:
                        source_type = "EXPLICIT_NEW_GAP_FIXTURE_WITH_DOCUMENTED_ACCOUNTING_RATIONALE"
                        source_file = f"ledger/bookkeeping_state_eval/dag/parity_corpus.py::{case.case_id}"
                        encoded_prior = False
                        notes = gap_notes.get(item.book_item_id, (target, f"Targeted accounting fixture for {target}"))[1]
                    elif is_chal:
                        source_type = "EXPLICIT_EXISTING_SCENARIO_ASSERTION"
                        source_file = f"ledger/bookkeeping_state_eval/scenarios/challenges.py::{case.case_id}::expected_classifications"
                        encoded_prior = True
                        notes = "Moroccan PCGE: 3421 Clients in challenge_01 dense collision scenario"
                    else:
                        clean_name = case.case_id.removeprefix("cat_")
                        source_type = "EXPLICIT_EXISTING_SCENARIO_ASSERTION"
                        source_file = f"ledger/bookkeeping_state_eval/scenarios/catalog.py::{clean_name}::expected_classifications"
                        encoded_prior = True
                        notes = catalog_notes.get(clean_name, f"Catalog scenario assertion for {target}")

                    entries.append(
                        TruthManifestEntry(
                            case_id=case.case_id,
                            book_item_id=item.book_item_id,
                            expected_terminal_type="CLASSIFIED",
                            expected_target=target,
                            truth_source_type=source_type,
                            source_file_and_assertion=source_file,
                            explicitly_encoded_before_parity_task=encoded_prior,
                            risk_class=risk_str,
                            notes=notes,
                        )
                    )
            else:
                # TRUTH_UNSPECIFIED
                unspecified += 1
                evidence_counts["INSUFFICIENT_TRUTH_UNSPECIFIED"] += 1
                clean_name = case.case_id.removeprefix("cat_").removeprefix("chal_")
                source_file = (
                    f"ledger/bookkeeping_state_eval/scenarios/challenges.py::{clean_name}"
                    if case.case_id.startswith("chal_")
                    else f"ledger/bookkeeping_state_eval/scenarios/catalog.py::{clean_name}"
                )

                entries.append(
                    TruthManifestEntry(
                        case_id=case.case_id,
                        book_item_id=item.book_item_id,
                        expected_terminal_type="TRUTH_UNSPECIFIED",
                        expected_target=None,
                        truth_source_type="TRUTH_UNSPECIFIED",
                        source_file_and_assertion=source_file,
                        explicitly_encoded_before_parity_task=False,
                        risk_class=risk_str,
                        notes="No explicit classification assertion in source scenario; evaluated as comparator only",
                    )
                )

    return TruthManifest(
        total_cases=len(corpus),
        total_items=len(entries),
        exact_code_labeled_items=exact_labeled,
        semantic_hold_labeled_items=hold_labeled,
        truth_unspecified_items=unspecified,
        counts_by_account_code=code_counts,
        counts_by_risk=risk_counts,
        counts_by_evidence_sufficiency=evidence_counts,
        entries=entries,
    )


def write_truth_manifest_json(manifest: TruthManifest, output_path: Path | str) -> None:
    p = Path(output_path)
    p.parent.mkdir(parents=True, exist_ok=True)
    with open(p, "w", encoding="utf-8") as f:
        json.dump(manifest.to_dict(), f, indent=2)

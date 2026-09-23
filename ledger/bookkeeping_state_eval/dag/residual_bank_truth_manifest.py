from __future__ import annotations

from dataclasses import asdict, dataclass
import json
import os
from pathlib import Path
from typing import Any, Sequence

from bookkeeping_state_eval.dag.residual_bank_corpus import (
    RESIDUAL_BANK_CORPUS_SPECS,
    ResidualBankCaseSpec,
)


@dataclass(frozen=True)
class ResidualBankTruthEntry:
    case_id: str
    direction: str
    amount_mad: float
    description: str
    counterparty: str | None
    reference: str | None
    expected_terminal_type: str  # "CLASSIFIED" | "HOLD"
    expected_account_code: str | None  # exact 4-digit Moroccan PCGE code
    expected_macro_family: str | None  # "EXPENSE" | "REVENUE" | "ASSET" | "LIABILITY" | "EQUITY"
    expected_hold_reason: str | None
    risk_class: str
    rationale: str
    evaluator_only: bool = True


@dataclass(frozen=True)
class ResidualBankTruthManifest:
    total_cases: int
    exact_code_labeled_items: int
    semantic_hold_labeled_items: int
    counts_by_category: dict[str, int]
    counts_by_account_code: dict[str, int]
    counts_by_macro_family: dict[str, int]
    entries: list[ResidualBankTruthEntry]

    def to_dict(self) -> dict[str, Any]:
        return {
            "total_cases": self.total_cases,
            "exact_code_labeled_items": self.exact_code_labeled_items,
            "semantic_hold_labeled_items": self.semantic_hold_labeled_items,
            "counts_by_category": self.counts_by_category,
            "counts_by_account_code": self.counts_by_account_code,
            "counts_by_macro_family": self.counts_by_macro_family,
            "entries": [asdict(e) for e in self.entries],
        }


def build_residual_bank_truth_manifest(
    specs: Sequence[ResidualBankCaseSpec] = RESIDUAL_BANK_CORPUS_SPECS,
) -> ResidualBankTruthManifest:
    entries: list[ResidualBankTruthEntry] = []
    cat_counts: dict[str, int] = {}
    code_counts: dict[str, int] = {}
    macro_counts: dict[str, int] = {}

    exact_labeled = 0
    hold_labeled = 0

    for s in specs:
        cat_counts[s.category] = cat_counts.get(s.category, 0) + 1
        if s.expected_account_code:
            code_counts[s.expected_account_code] = code_counts.get(s.expected_account_code, 0) + 1
            exact_labeled += 1
        if s.expected_macro_family:
            macro_counts[s.expected_macro_family] = macro_counts.get(s.expected_macro_family, 0) + 1
        if s.expected_terminal_type == "HOLD":
            hold_labeled += 1

        entries.append(
            ResidualBankTruthEntry(
                case_id=s.case_id,
                direction=s.direction,
                amount_mad=float(s.amount),
                description=s.description,
                counterparty=s.counterparty_name,
                reference=s.reference,
                expected_terminal_type=s.expected_terminal_type,
                expected_account_code=s.expected_account_code,
                expected_macro_family=s.expected_macro_family,
                expected_hold_reason=s.expected_hold_reason,
                risk_class=s.risk_class,
                rationale=s.rationale,
                evaluator_only=True,
            )
        )

    return ResidualBankTruthManifest(
        total_cases=len(specs),
        exact_code_labeled_items=exact_labeled,
        semantic_hold_labeled_items=hold_labeled,
        counts_by_category=cat_counts,
        counts_by_account_code=code_counts,
        counts_by_macro_family=macro_counts,
        entries=entries,
    )


def write_residual_bank_truth_manifest_json(
    filepath: str | Path,
    manifest: ResidualBankTruthManifest | None = None,
) -> None:
    if manifest is None:
        manifest = build_residual_bank_truth_manifest()
    path = Path(filepath)
    os.makedirs(path.parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(manifest.to_dict(), f, indent=2, ensure_ascii=False)

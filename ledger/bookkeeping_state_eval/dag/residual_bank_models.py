from __future__ import annotations

from dataclasses import asdict, dataclass, field
from enum import Enum
from typing import Any, Mapping, Sequence


class FailureLayer(str, Enum):
    RAW_COA_MISSING = "raw_coa_missing"
    MACRO_ROUTING_MISS = "macro_routing_miss"
    CANDIDATE_FILTERING_MISS = "candidate_filtering_miss"
    MODEL_ACCOUNT_SELECTION_MISS = "model_account_selection_miss"
    CONFIDENCE_HOLD = "confidence_hold"
    TERMINAL_OR_CONTRACT_ISSUE = "terminal_or_contract_issue"

    # Backward compatibility aliases
    CANDIDATE_RETRIEVAL_MISS = "candidate_filtering_miss"
    CONFIDENCE_OR_HOLD_ISSUE = "confidence_hold"
    CONTRACT_OR_RUNTIME_FAILURE = "terminal_or_contract_issue"


@dataclass(frozen=True)
class ResidualBankObservation:
    provider: str  # "REAL_MODEL_DOMAIN_TOOLS" | "SIMULATED_STUB"
    bank_item_id: str
    terminal_type: str  # "CLASSIFIED" | "HOLD" | "PROVIDER_FAILURE"
    account_code: str | None = None
    confidence: float = 0.0
    hold_reason: str | None = None
    evidence_refs: tuple[str, ...] = ()
    rationale: str = ""
    provider_issue: str | None = None
    dag_run_id: str = ""
    latency_ms: float = 0.0
    candidate_source: str | None = None
    constrained_macro: str | None = None
    candidate_codes: tuple[str, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return {
            "provider": self.provider,
            "bank_item_id": self.bank_item_id,
            "terminal_type": self.terminal_type,
            "account_code": self.account_code,
            "confidence": round(self.confidence, 4),
            "hold_reason": self.hold_reason,
            "evidence_refs": list(self.evidence_refs),
            "rationale": self.rationale,
            "provider_issue": self.provider_issue,
            "dag_run_id": self.dag_run_id,
            "latency_ms": round(self.latency_ms, 2),
            "candidate_source": self.candidate_source,
            "constrained_macro": self.constrained_macro,
            "candidate_codes": list(self.candidate_codes),
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> ResidualBankObservation:
        return cls(
            provider=d["provider"],
            bank_item_id=d["bank_item_id"],
            terminal_type=d["terminal_type"],
            account_code=d.get("account_code"),
            confidence=float(d.get("confidence", 0.0)),
            hold_reason=d.get("hold_reason"),
            evidence_refs=tuple(d.get("evidence_refs", ())),
            rationale=d.get("rationale", ""),
            provider_issue=d.get("provider_issue"),
            dag_run_id=d.get("dag_run_id", ""),
            latency_ms=float(d.get("latency_ms", 0.0)),
            candidate_source=d.get("candidate_source"),
            constrained_macro=d.get("constrained_macro"),
            candidate_codes=tuple(d.get("candidate_codes", ())),
        )


@dataclass(frozen=True)
class ResidualBankCaseResult:
    case_id: str
    bank_item_id: str
    category: str  # "OUTFLOW" | "INFLOW" | "HOLD_SAFETY"
    direction: str  # "OUTFLOW" | "INFLOW"
    expected_terminal_type: str  # "CLASSIFIED" | "HOLD"
    expected_account_code: str | None
    expected_macro_family: str | None
    expected_hold_reason: str | None
    risk_class: str
    rationale: str
    obs: ResidualBankObservation
    terminal_match: bool
    exact_code_match: bool | None
    macro_match: bool | None
    hold_match: bool | None
    candidate_recall: bool | None
    raw_coa_contains_expected: bool | None = None
    predicted_macro: str | None = None
    macro_confidence: float | None = None
    candidate_contains_expected: bool | None = None
    model_selected_code: str | None = None
    account_confidence: float | None = None
    primary_failure_layer: FailureLayer | None = None
    secondary_notes: str | None = None
    failure_layer: FailureLayer | None = None

    def to_dict(self) -> dict[str, Any]:
        fl = self.primary_failure_layer or self.failure_layer
        return {
            "case_id": self.case_id,
            "bank_item_id": self.bank_item_id,
            "category": self.category,
            "direction": self.direction,
            "expected_terminal_type": self.expected_terminal_type,
            "expected_account_code": self.expected_account_code,
            "expected_macro_family": self.expected_macro_family,
            "expected_hold_reason": self.expected_hold_reason,
            "risk_class": self.risk_class,
            "rationale": self.rationale,
            "obs": self.obs.to_dict(),
            "terminal_match": self.terminal_match,
            "exact_code_match": self.exact_code_match,
            "macro_match": self.macro_match,
            "hold_match": self.hold_match,
            "candidate_recall": self.candidate_recall,
            "raw_coa_contains_expected": self.raw_coa_contains_expected,
            "predicted_macro": self.predicted_macro,
            "macro_confidence": round(self.macro_confidence, 4) if self.macro_confidence is not None else None,
            "candidate_contains_expected": self.candidate_contains_expected,
            "model_selected_code": self.model_selected_code,
            "account_confidence": round(self.account_confidence, 4) if self.account_confidence is not None else None,
            "primary_failure_layer": fl.value if fl else None,
            "secondary_notes": self.secondary_notes,
            "failure_layer": fl.value if fl else None,
        }


def compute_macro_family_from_code(code: str | None) -> str | None:
    if not code:
        return None
    c = code.strip()
    if not c:
        return None
    if c.startswith("6"):
        return "EXPENSE"
    if c.startswith("7"):
        return "REVENUE"
    if c.startswith("2") or c.startswith("3"):
        return "ASSET"
    if c.startswith("4") or c.startswith("14"):
        return "LIABILITY"
    if c.startswith("11"):
        return "EQUITY"
    return "UNKNOWN"


def evaluate_residual_bank_case(
    *,
    case_id: str,
    bank_item_id: str,
    category: str,
    direction: str,
    expected_terminal_type: str,
    expected_account_code: str | None,
    expected_macro_family: str | None,
    expected_hold_reason: str | None,
    risk_class: str,
    rationale: str,
    obs: ResidualBankObservation,
    raw_coa_codes: Sequence[str] | set[str] | None = None,
) -> ResidualBankCaseResult:
    # 1. Operational / Runtime Failure
    if obs.terminal_type == "PROVIDER_FAILURE":
        return ResidualBankCaseResult(
            case_id=case_id,
            bank_item_id=bank_item_id,
            category=category,
            direction=direction,
            expected_terminal_type=expected_terminal_type,
            expected_account_code=expected_account_code,
            expected_macro_family=expected_macro_family,
            expected_hold_reason=expected_hold_reason,
            risk_class=risk_class,
            rationale=rationale,
            obs=obs,
            terminal_match=False,
            exact_code_match=False if expected_account_code else None,
            macro_match=False if expected_macro_family else None,
            hold_match=False if expected_terminal_type == "HOLD" else None,
            candidate_recall=False if expected_account_code else None,
            raw_coa_contains_expected=(expected_account_code in raw_coa_codes) if (raw_coa_codes is not None and expected_account_code) else None,
            predicted_macro=obs.constrained_macro,
            macro_confidence=None,
            candidate_contains_expected=False if expected_account_code else None,
            model_selected_code=None,
            account_confidence=None,
            primary_failure_layer=FailureLayer.TERMINAL_OR_CONTRACT_ISSUE,
            secondary_notes=f"Operational provider failure: {obs.provider_issue}",
            failure_layer=FailureLayer.TERMINAL_OR_CONTRACT_ISSUE,
        )

    # 2. Terminal match
    terminal_match = (obs.terminal_type == expected_terminal_type)

    exact_code_match: bool | None = None
    macro_match: bool | None = None
    hold_match: bool | None = None
    candidate_recall: bool | None = None
    raw_coa_contains_expected: bool | None = None
    predicted_macro: str | None = None
    macro_confidence: float | None = None
    candidate_contains_expected: bool | None = None
    model_selected_code: str | None = None
    account_confidence: float | None = None
    primary_failure_layer: FailureLayer | None = None
    secondary_notes: str | None = None

    # Derive model selected code and account confidence
    if obs.terminal_type == "CLASSIFIED":
        model_selected_code = obs.account_code
        account_confidence = obs.confidence
    elif obs.terminal_type == "HOLD":
        if obs.account_code:
            model_selected_code = obs.account_code
            account_confidence = obs.confidence
        elif "top candidate '" in (obs.hold_reason or ""):
            import re
            m = re.search(r"top candidate '(\d{4})'", obs.hold_reason or "")
            if m:
                model_selected_code = m.group(1)
                m_conf = re.search(r"confidence \((\d+\.\d+)\)", obs.hold_reason or "")
                if m_conf:
                    account_confidence = float(m_conf.group(1))

    # Derive predicted macro and macro confidence
    predicted_macro = obs.constrained_macro
    if not predicted_macro and obs.account_code:
        predicted_macro = compute_macro_family_from_code(obs.account_code)
    elif not predicted_macro and model_selected_code:
        predicted_macro = compute_macro_family_from_code(model_selected_code)

    if obs.hold_reason and "confidence (" in obs.hold_reason and "bank_macro_classifier" in obs.hold_reason:
        import re
        m = re.search(r"confidence \((\d+\.\d+)\)", obs.hold_reason)
        if m:
            macro_confidence = float(m.group(1))
    elif predicted_macro and predicted_macro not in ("HOLD_AMBIGUOUS", "HOLD_INSUFFICIENT_EVIDENCE"):
        macro_confidence = 0.99

    if expected_terminal_type == "HOLD":
        hold_match = terminal_match
        if not terminal_match:
            primary_failure_layer = FailureLayer.TERMINAL_OR_CONTRACT_ISSUE
            secondary_notes = "Expected semantic HOLD, but transaction was classified."
    else:
        # Expected CLASSIFIED
        if expected_account_code:
            if raw_coa_codes is not None:
                raw_coa_contains_expected = expected_account_code in raw_coa_codes
            else:
                raw_coa_contains_expected = True

            candidate_contains_expected = expected_account_code in obs.candidate_codes
            candidate_recall = candidate_contains_expected
            exact_code_match = (obs.terminal_type == "CLASSIFIED" and obs.account_code == expected_account_code)

            if expected_macro_family:
                macro_match = (predicted_macro == expected_macro_family)

            if not exact_code_match:
                # Assign exactly ONE primary failure layer based on earliest failure point
                if raw_coa_contains_expected is False:
                    primary_failure_layer = FailureLayer.RAW_COA_MISSING
                    secondary_notes = f"Expected code '{expected_account_code}' absent from authoritative company CoA in database."
                elif expected_macro_family and macro_match is False:
                    primary_failure_layer = FailureLayer.MACRO_ROUTING_MISS
                    secondary_notes = f"Macro router selected '{predicted_macro}' instead of expected '{expected_macro_family}', causing target accounts to be excluded."
                elif len(obs.candidate_codes) > 0 and candidate_contains_expected is False:
                    primary_failure_layer = FailureLayer.CANDIDATE_FILTERING_MISS
                    secondary_notes = f"Macro was correct ({predicted_macro}), but candidate filtering dropped expected code '{expected_account_code}' from resolver candidate set."
                elif obs.terminal_type == "CLASSIFIED" and obs.account_code != expected_account_code:
                    primary_failure_layer = FailureLayer.MODEL_ACCOUNT_SELECTION_MISS
                    secondary_notes = f"Expected code '{expected_account_code}' was present in candidates, but model selected '{obs.account_code}' with confidence >= 0.98."
                elif obs.terminal_type == "HOLD":
                    primary_failure_layer = FailureLayer.CONFIDENCE_HOLD
                    if len(obs.candidate_codes) == 0:
                        secondary_notes = f"Macro '{predicted_macro}' correctly recognized, but router confidence ({macro_confidence}) below 0.98 halted execution before resolver."
                    else:
                        secondary_notes = f"Correct candidate path existed, but confidence below 0.98 threshold caused safety HOLD."
                else:
                    primary_failure_layer = FailureLayer.TERMINAL_OR_CONTRACT_ISSUE
                    secondary_notes = "Unexpected terminal or contract failure."

    return ResidualBankCaseResult(
        case_id=case_id,
        bank_item_id=bank_item_id,
        category=category,
        direction=direction,
        expected_terminal_type=expected_terminal_type,
        expected_account_code=expected_account_code,
        expected_macro_family=expected_macro_family,
        expected_hold_reason=expected_hold_reason,
        risk_class=risk_class,
        rationale=rationale,
        obs=obs,
        terminal_match=terminal_match,
        exact_code_match=exact_code_match,
        macro_match=macro_match,
        hold_match=hold_match,
        candidate_recall=candidate_recall,
        raw_coa_contains_expected=raw_coa_contains_expected,
        predicted_macro=predicted_macro,
        macro_confidence=macro_confidence,
        candidate_contains_expected=candidate_contains_expected,
        model_selected_code=model_selected_code,
        account_confidence=account_confidence,
        primary_failure_layer=primary_failure_layer,
        secondary_notes=secondary_notes,
        failure_layer=primary_failure_layer,
    )


def compute_residual_bank_metrics(
    results: Sequence[ResidualBankCaseResult],
) -> dict[str, Any]:
    total_items = len(results)
    if total_items == 0:
        return {}

    # Denominators
    exact_code_items = [r for r in results if r.expected_account_code is not None]
    hold_items = [r for r in results if r.expected_terminal_type == "HOLD"]
    macro_items = [r for r in results if r.expected_macro_family is not None]

    n_exact = len(exact_code_items)
    n_hold = len(hold_items)
    n_macro = len(macro_items)

    # 1. Raw CoA presence (Expected exact code exists anywhere in authoritative Entity.default_coa)
    raw_coa_hits = sum(1 for r in exact_code_items if r.raw_coa_contains_expected is True)
    raw_coa_rate = round(raw_coa_hits / (n_exact or 1), 4)

    # 2. Candidate recall (expected code in candidate set presented to resolver)
    cand_recall_hits = sum(1 for r in exact_code_items if r.candidate_recall is True)
    cand_recall_rate = round(cand_recall_hits / (n_exact or 1), 4)

    # 3. Conditional exact accuracy (only where expected account was available among candidates)
    cond_available = [r for r in exact_code_items if r.candidate_recall is True]
    cond_hits = sum(1 for r in cond_available if r.exact_code_match is True)
    cond_rate = round(cond_hits / (len(cond_available) or 1), 4) if cond_available else 0.0

    # 4. Overall exact-code accuracy
    exact_hits = sum(1 for r in exact_code_items if r.exact_code_match is True)
    exact_rate = round(exact_hits / (n_exact or 1), 4)

    # 5. Macro-family accuracy
    macro_hits = sum(1 for r in macro_items if r.macro_match is True)
    macro_rate = round(macro_hits / (n_macro or 1), 4)

    # 6. Terminal accuracy
    term_hits = sum(1 for r in results if r.terminal_match is True)
    term_rate = round(term_hits / total_items, 4)

    # 7. HOLD precision & recall
    # True positives: expected HOLD and obs HOLD
    hold_tp = sum(1 for r in hold_items if r.obs.terminal_type == "HOLD")
    # False positives: expected CLASSIFIED but obs HOLD
    hold_fp = sum(1 for r in results if r.expected_terminal_type == "CLASSIFIED" and r.obs.terminal_type == "HOLD")
    # False negatives: expected HOLD but obs CLASSIFIED
    hold_fn = n_hold - hold_tp

    hold_precision = round(hold_tp / ((hold_tp + hold_fp) or 1), 4) if (hold_tp + hold_fp) > 0 else 0.0
    hold_recall = round(hold_tp / (n_hold or 1), 4)

    # Failure layer counts (mutually exclusive primary failure layers)
    failure_counts: dict[str, int] = {
        FailureLayer.RAW_COA_MISSING.value: 0,
        FailureLayer.MACRO_ROUTING_MISS.value: 0,
        FailureLayer.CANDIDATE_FILTERING_MISS.value: 0,
        FailureLayer.MODEL_ACCOUNT_SELECTION_MISS.value: 0,
        FailureLayer.CONFIDENCE_HOLD.value: 0,
        FailureLayer.TERMINAL_OR_CONTRACT_ISSUE.value: 0,
    }
    failures_list: list[dict[str, Any]] = []
    for r in results:
        fl = r.primary_failure_layer or r.failure_layer
        if fl:
            failure_counts[fl.value] += 1
            failures_list.append({
                "case_id": r.case_id,
                "bank_item_id": r.bank_item_id,
                "category": r.category,
                "expected": r.expected_account_code or r.expected_hold_reason or r.expected_terminal_type,
                "observed_terminal": r.obs.terminal_type,
                "observed_code": r.obs.account_code,
                "observed_hold_reason": r.obs.hold_reason,
                "failure_layer": fl.value,
                "rationale": r.obs.rationale,
                "secondary_notes": r.secondary_notes,
            })

    # Category breakdowns
    by_category: dict[str, Any] = {}
    for cat in ("OUTFLOW", "INFLOW", "HOLD_SAFETY"):
        cat_items = [r for r in results if r.category == cat]
        if not cat_items:
            continue
        c_exact_items = [r for r in cat_items if r.expected_account_code is not None]
        c_exact_hits = sum(1 for r in c_exact_items if r.exact_code_match is True)
        c_term_hits = sum(1 for r in cat_items if r.terminal_match is True)
        by_category[cat] = {
            "total_items": len(cat_items),
            "exact_code_items": len(c_exact_items),
            "exact_code_correct": c_exact_hits,
            "exact_code_accuracy": round(c_exact_hits / (len(c_exact_items) or 1), 4) if c_exact_items else None,
            "terminal_correct": c_term_hits,
            "terminal_accuracy": round(c_term_hits / len(cat_items), 4),
        }

    return {
        "total_items": total_items,
        "exact_code_denominator": n_exact,
        "hold_denominator": n_hold,
        "macro_denominator": n_macro,
        "raw_coa_presence": {
            "hits": raw_coa_hits,
            "total": n_exact,
            "rate": raw_coa_rate,
        },
        "candidate_recall": {
            "hits": cand_recall_hits,
            "total": n_exact,
            "rate": cand_recall_rate,
        },
        "conditional_exact_accuracy": {
            "hits": cond_hits,
            "total": len(cond_available),
            "rate": cond_rate,
        },
        "overall_exact_code_accuracy": {
            "hits": exact_hits,
            "total": n_exact,
            "rate": exact_rate,
        },
        "macro_family_accuracy": {
            "hits": macro_hits,
            "total": n_macro,
            "rate": macro_rate,
        },
        "terminal_accuracy": {
            "hits": term_hits,
            "total": total_items,
            "rate": term_rate,
        },
        "hold_metrics": {
            "expected_holds": n_hold,
            "true_positives": hold_tp,
            "false_positives": hold_fp,
            "false_negatives": hold_fn,
            "precision": hold_precision,
            "recall": hold_recall,
        },
        "by_category": by_category,
        "failure_counts": failure_counts,
        "failures": failures_list,
    }


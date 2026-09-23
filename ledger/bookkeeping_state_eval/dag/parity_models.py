from __future__ import annotations

from dataclasses import asdict, dataclass, field
from enum import Enum
from typing import Any, Mapping

from bookkeeping_state.dag.view import DagView


class RiskClass(str, Enum):
    LOW = "LOW"
    MEDIUM = "MEDIUM"
    HIGH = "HIGH"


class DisagreementClass(str, Enum):
    MATCH = "MATCH"
    SIMULATOR_WRONG_GO_RIGHT = "SIMULATOR_WRONG_GO_RIGHT"
    GO_WRONG_SIMULATOR_RIGHT = "GO_WRONG_SIMULATOR_RIGHT"
    BOTH_WRONG = "BOTH_WRONG"
    TERMINAL_TYPE_MISMATCH = "TERMINAL_TYPE_MISMATCH"
    ACCOUNT_CODE_MISMATCH = "ACCOUNT_CODE_MISMATCH"
    HOLD_REASON_MISMATCH = "HOLD_REASON_MISMATCH"
    EVIDENCE_USAGE_MISMATCH = "EVIDENCE_USAGE_MISMATCH"
    TRUTH_UNSPECIFIED_DISAGREEMENT = "TRUTH_UNSPECIFIED_DISAGREEMENT"
    PROVIDER_FAILURE = "PROVIDER_FAILURE"
    BOUNDARY_VALIDATION_FAILURE = "BOUNDARY_VALIDATION_FAILURE"


@dataclass(frozen=True)
class BookCategorizationParityCase:
    """
    Immutable specification of a single parity evaluation case.
    Encapsulates a bounded DagView and optional independent expected truth.
    """

    case_id: str
    dag_view: DagView
    expected_truth: Mapping[str, str | None] | None = None  # book_item_id -> account_code or None (HOLD)
    risk_class: RiskClass = RiskClass.MEDIUM
    tags: tuple[str, ...] = ()


@dataclass(frozen=True)
class BookCategorizationParityObservation:
    """
    Normalized outcome recorded from a single classifier provider (Simulator or Go ASE).
    """

    provider: str  # "SIMULATOR" | "GO_ASE"
    book_item_id: str
    terminal_type: str  # "CLASSIFIED" | "HOLD" | "PROVIDER_FAILURE"
    account_code: str | None = None
    confidence: float = 0.0
    hold_reason: str | None = None
    evidence_refs: tuple[str, ...] = ()
    rationale: str = ""
    provider_issue: str | None = None
    dag_run_id: str = ""
    latency_ms: float = 0.0
    is_replay: bool = False
    candidate_source: str | None = None
    constrained_macro: str | None = None
    candidate_codes: tuple[str, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return {
            "provider": self.provider,
            "book_item_id": self.book_item_id,
            "terminal_type": self.terminal_type,
            "account_code": self.account_code,
            "confidence": round(self.confidence, 4),
            "hold_reason": self.hold_reason,
            "evidence_refs": list(self.evidence_refs),
            "rationale": self.rationale,
            "provider_issue": self.provider_issue,
            "dag_run_id": self.dag_run_id,
            "latency_ms": round(self.latency_ms, 2),
            "is_replay": self.is_replay,
            "candidate_source": self.candidate_source,
            "constrained_macro": self.constrained_macro,
            "candidate_codes": list(self.candidate_codes),
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> BookCategorizationParityObservation:
        return cls(
            provider=d["provider"],
            book_item_id=d["book_item_id"],
            terminal_type=d["terminal_type"],
            account_code=d.get("account_code"),
            confidence=float(d.get("confidence", 0.0)),
            hold_reason=d.get("hold_reason"),
            evidence_refs=tuple(d.get("evidence_refs", ())),
            rationale=d.get("rationale", ""),
            provider_issue=d.get("provider_issue"),
            dag_run_id=d.get("dag_run_id", ""),
            latency_ms=float(d.get("latency_ms", 0.0)),
            is_replay=bool(d.get("is_replay", False)),
            candidate_source=d.get("candidate_source"),
            constrained_macro=d.get("constrained_macro"),
            candidate_codes=tuple(d.get("candidate_codes", ())),
        )


@dataclass(frozen=True)
class BookCategorizationParityResult:
    """
    Comparison result for a single book item between Simulator and Go ASE.
    """

    case_id: str
    book_item_id: str
    risk_class: RiskClass
    tags: tuple[str, ...]
    expected_truth: str | None  # Expected account code, or "HOLD", or None if unspecified
    sim_obs: BookCategorizationParityObservation
    go_obs: BookCategorizationParityObservation
    disagreement_class: DisagreementClass
    provider_agreement: bool

    def to_dict(self) -> dict[str, Any]:
        return {
            "case_id": self.case_id,
            "book_item_id": self.book_item_id,
            "risk_class": self.risk_class.value,
            "tags": list(self.tags),
            "expected_truth": self.expected_truth,
            "sim_obs": self.sim_obs.to_dict(),
            "go_obs": self.go_obs.to_dict(),
            "disagreement_class": self.disagreement_class.value,
            "provider_agreement": self.provider_agreement,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> BookCategorizationParityResult:
        return cls(
            case_id=d["case_id"],
            book_item_id=d["book_item_id"],
            risk_class=RiskClass(d["risk_class"]),
            tags=tuple(d.get("tags", ())),
            expected_truth=d.get("expected_truth"),
            sim_obs=BookCategorizationParityObservation.from_dict(d["sim_obs"]),
            go_obs=BookCategorizationParityObservation.from_dict(d["go_obs"]),
            disagreement_class=DisagreementClass(d["disagreement_class"]),
            provider_agreement=bool(d["provider_agreement"]),
        )

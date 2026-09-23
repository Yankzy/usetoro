from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Sequence

import hashlib
import json

BOOK_CATEGORIZE_SCHEMA_VERSION = "bookkeeping.ase.book_categorize.v1"
BOOK_CATEGORIZATION_SCHEMA_VERSION = BOOK_CATEGORIZE_SCHEMA_VERSION
BOOK_CATEGORIZATION_DAG_ID = "bookkeeping_account_categorization_v1"
DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT = "worker.inbox.bookkeeping_ase_book_categorizer"
READINESS_SCHEMA_VERSION = "bookkeeping.ase.readiness.v1"


@dataclass(frozen=True, slots=True)
class BookCategorizeItemPayload:
    book_item_id: str
    date: str
    amount_units: int
    currency: str
    direction: str
    description: str | None = None
    counterparty_id: str | None = None
    counterparty_name: str | None = None
    reference: str | None = None
    active_bank_account_id: str | None = None
    evidence_refs: tuple[str, ...] = ()
    safe_evidence_summaries: tuple[str, ...] = ()
    existing_classification_id: str | None = None
    existing_account_code: str | None = None
    source_artifact_kind: str | None = None
    bookkeeping_role: str | None = None

    def __init__(
        self,
        book_item_id: str,
        date: str,
        amount_units: int | None = None,
        currency: str = "",
        direction: str = "",
        description: str | None = None,
        counterparty_id: str | None = None,
        counterparty_name: str | None = None,
        reference: str | None = None,
        active_bank_account_id: str | None = None,
        evidence_refs: tuple[str, ...] = (),
        safe_evidence_summaries: tuple[str, ...] = (),
        existing_classification_id: str | None = None,
        existing_account_code: str | None = None,
        source_artifact_kind: str | None = None,
        bookkeeping_role: str | None = None,
        *,
        amount: int | None = None,
    ) -> None:
        resolved_amount = (
            amount_units
            if amount_units is not None
            else (amount if amount is not None else 0)
        )
        object.__setattr__(self, "book_item_id", str(book_item_id))
        object.__setattr__(self, "date", str(date))
        object.__setattr__(self, "amount_units", int(resolved_amount))
        object.__setattr__(self, "currency", str(currency))
        object.__setattr__(self, "direction", str(direction))
        object.__setattr__(self, "description", description)
        object.__setattr__(self, "counterparty_id", counterparty_id)
        object.__setattr__(self, "counterparty_name", counterparty_name)
        object.__setattr__(self, "reference", reference)
        object.__setattr__(self, "active_bank_account_id", active_bank_account_id)
        object.__setattr__(
            self, "evidence_refs", tuple(sorted(str(x) for x in evidence_refs))
        )
        object.__setattr__(
            self,
            "safe_evidence_summaries",
            tuple(str(x) for x in safe_evidence_summaries),
        )
        object.__setattr__(
            self, "existing_classification_id", existing_classification_id
        )
        object.__setattr__(self, "existing_account_code", existing_account_code)
        object.__setattr__(self, "source_artifact_kind", source_artifact_kind)
        object.__setattr__(self, "bookkeeping_role", bookkeeping_role)

    @property
    def amount(self) -> int:
        return self.amount_units

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {
            "book_item_id": self.book_item_id,
            "date": self.date,
            "amount_units": self.amount_units,
            "amount": self.amount_units,  # Wire compatibility
            "currency": self.currency,
            "direction": self.direction,
            "evidence_refs": list(self.evidence_refs),
        }
        if self.safe_evidence_summaries:
            d["safe_evidence_summaries"] = list(self.safe_evidence_summaries)
        if self.description is not None:
            d["description"] = self.description
        if self.counterparty_id is not None:
            d["counterparty_id"] = self.counterparty_id
        if self.counterparty_name is not None:
            d["counterparty_name"] = self.counterparty_name
        if self.reference is not None:
            d["reference"] = self.reference
        if self.active_bank_account_id is not None:
            d["active_bank_account_id"] = self.active_bank_account_id
        if self.existing_classification_id is not None:
            d["existing_classification_id"] = self.existing_classification_id
        if self.existing_account_code is not None:
            d["existing_account_code"] = self.existing_account_code
        if self.source_artifact_kind is not None:
            d["source_artifact_kind"] = self.source_artifact_kind
        if self.bookkeeping_role is not None:
            d["bookkeeping_role"] = self.bookkeeping_role
        return d

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> BookCategorizeItemPayload:
        amt = data.get("amount_units")
        if amt is None:
            amt = data.get("amount", 0)
        return cls(
            book_item_id=str(data["book_item_id"]),
            date=str(data["date"]),
            amount_units=int(amt),
            currency=str(data.get("currency", "")),
            direction=str(data.get("direction", "")),
            description=data.get("description"),
            counterparty_id=data.get("counterparty_id"),
            counterparty_name=data.get("counterparty_name"),
            reference=data.get("reference"),
            active_bank_account_id=data.get("active_bank_account_id"),
            evidence_refs=tuple(str(x) for x in data.get("evidence_refs", ())),
            safe_evidence_summaries=tuple(
                str(x) for x in data.get("safe_evidence_summaries", ())
            ),
            existing_classification_id=data.get("existing_classification_id"),
            existing_account_code=data.get("existing_account_code"),
            source_artifact_kind=data.get("source_artifact_kind"),
            bookkeeping_role=data.get("bookkeeping_role"),
        )


@dataclass(frozen=True, slots=True)
class BookCategorizeRequestEnvelope:
    schema_version: str
    request_id: str
    idempotency_key: str
    company_id: str
    session_id: str
    state_revision: int
    persistence_revision: int
    dag_id: str
    requested_at: str
    book_items: tuple[BookCategorizeItemPayload, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema_version": self.schema_version,
            "request_id": self.request_id,
            "idempotency_key": self.idempotency_key,
            "company_id": self.company_id,
            "session_id": self.session_id,
            "state_revision": self.state_revision,
            "persistence_revision": self.persistence_revision,
            "dag_id": self.dag_id,
            "requested_at": self.requested_at,
            "book_items": [
                item.to_dict()
                for item in sorted(self.book_items, key=lambda x: x.book_item_id)
            ],
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> BookCategorizeRequestEnvelope:
        return cls(
            schema_version=str(data["schema_version"]),
            request_id=str(data["request_id"]),
            idempotency_key=str(data["idempotency_key"]),
            company_id=str(data["company_id"]),
            session_id=str(data["session_id"]),
            state_revision=int(data["state_revision"]),
            persistence_revision=int(data.get("persistence_revision", 0)),
            dag_id=str(data["dag_id"]),
            requested_at=str(data["requested_at"]),
            book_items=tuple(
                BookCategorizeItemPayload.from_dict(item)
                for item in data.get("book_items", ())
            ),
        )


@dataclass(frozen=True, slots=True)
class BookOutcomePayload:
    book_item_id: str
    status: str  # "CLASSIFIED" | "HOLD"
    account_code: str | None = None
    confidence: float | None = None
    rationale: str | None = None
    evidence_refs: tuple[str, ...] = ()
    ase_node_id: str | None = None
    terminal_property: str | None = None
    hold_reason: str | None = None
    required_evidence: tuple[str, ...] = ()
    candidate_source: str | None = None
    constrained_macro: str | None = None
    candidate_codes: tuple[str, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {
            "book_item_id": self.book_item_id,
            "status": self.status,
            "evidence_refs": list(self.evidence_refs),
        }
        if self.account_code is not None:
            d["account_code"] = self.account_code
        if self.confidence is not None:
            d["confidence"] = self.confidence
        if self.rationale is not None:
            d["rationale"] = self.rationale
        if self.ase_node_id is not None:
            d["ase_node_id"] = self.ase_node_id
        if self.terminal_property is not None:
            d["terminal_property"] = self.terminal_property
        if self.hold_reason is not None:
            d["hold_reason"] = self.hold_reason
        if self.required_evidence:
            d["required_evidence"] = list(self.required_evidence)
        if self.candidate_source is not None:
            d["candidate_source"] = self.candidate_source
        if self.constrained_macro is not None:
            d["constrained_macro"] = self.constrained_macro
        if self.candidate_codes:
            d["candidate_codes"] = list(self.candidate_codes)
        return d

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> BookOutcomePayload:
        return cls(
            book_item_id=str(data["book_item_id"]),
            status=str(data["status"]),
            account_code=data.get("account_code"),
            confidence=float(data["confidence"]) if data.get("confidence") is not None else None,
            rationale=data.get("rationale"),
            evidence_refs=tuple(str(x) for x in data.get("evidence_refs", ())),
            ase_node_id=data.get("ase_node_id"),
            terminal_property=data.get("terminal_property"),
            hold_reason=data.get("hold_reason"),
            required_evidence=tuple(str(x) for x in data.get("required_evidence", ())),
            candidate_source=data.get("candidate_source"),
            constrained_macro=data.get("constrained_macro"),
            candidate_codes=tuple(str(x) for x in data.get("candidate_codes", ())),
        )


@dataclass(frozen=True, slots=True)
class ProviderIssuePayload:
    book_item_id: str | None
    code: str
    message: str

    def to_dict(self) -> dict[str, Any]:
        return {
            "book_item_id": self.book_item_id,
            "code": self.code,
            "message": self.message,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> ProviderIssuePayload:
        return cls(
            book_item_id=data.get("book_item_id"),
            code=str(data.get("code", "UNKNOWN_ERROR")),
            message=str(data.get("message", "")),
        )


@dataclass(frozen=True, slots=True)
class BookCategorizeResponseEnvelope:
    schema_version: str
    request_id: str
    idempotency_key: str
    session_id: str
    state_revision: int
    dag_id: str
    dag_run_id: str
    status: str
    outcomes: tuple[BookOutcomePayload, ...] = ()
    provider_issues: tuple[ProviderIssuePayload, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema_version": self.schema_version,
            "request_id": self.request_id,
            "idempotency_key": self.idempotency_key,
            "session_id": self.session_id,
            "state_revision": self.state_revision,
            "dag_id": self.dag_id,
            "dag_run_id": self.dag_run_id,
            "status": self.status,
            "outcomes": [o.to_dict() for o in self.outcomes],
            "provider_issues": [p.to_dict() for p in self.provider_issues],
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> BookCategorizeResponseEnvelope:
        return cls(
            schema_version=str(data["schema_version"]),
            request_id=str(data["request_id"]),
            idempotency_key=str(data["idempotency_key"]),
            session_id=str(data["session_id"]),
            state_revision=int(data["state_revision"]),
            dag_id=str(data["dag_id"]),
            dag_run_id=str(data.get("dag_run_id", "")),
            status=str(data.get("status", "SUCCESS")),
            outcomes=tuple(
                BookOutcomePayload.from_dict(o) for o in data.get("outcomes", ())
            ),
            provider_issues=tuple(
                ProviderIssuePayload.from_dict(p)
                for p in data.get("provider_issues", ())
            ),
        )


def compute_canonical_semantic_dict(
    *,
    schema_version: str,
    company_id: str,
    session_id: str,
    state_revision: int,
    persistence_revision: int,
    dag_id: str,
    book_items: Sequence[BookCategorizeItemPayload],
) -> dict[str, Any]:
    """
    Produce deterministically ordered, canonicalized semantic request dictionary.
    Excludes non-semantic request metadata (request_id, idempotency_key, requested_at).
    """
    sorted_items = sorted(book_items, key=lambda it: it.book_item_id)
    canonical_items: list[dict[str, Any]] = []
    for it in sorted_items:
        unique_evidence_refs = sorted(list(set(str(x) for x in it.evidence_refs)))
        unique_summaries = sorted(list(set(str(x) for x in it.safe_evidence_summaries)))
        canonical_items.append({
            "active_bank_account_id": it.active_bank_account_id or "",
            "amount_units": int(it.amount_units),
            "book_item_id": str(it.book_item_id),
            "bookkeeping_role": it.bookkeeping_role or "",
            "counterparty_id": it.counterparty_id or "",
            "counterparty_name": it.counterparty_name or "",
            "currency": str(it.currency),
            "date": str(it.date),
            "description": it.description or "",
            "direction": str(it.direction),
            "evidence_refs": unique_evidence_refs,
            "existing_account_code": it.existing_account_code or "",
            "existing_classification_id": it.existing_classification_id or "",
            "reference": it.reference or "",
            "safe_evidence_summaries": unique_summaries,
            "source_artifact_kind": it.source_artifact_kind or "",
        })
    return {
        "company_id": str(company_id),
        "dag_id": str(dag_id),
        "items": canonical_items,
        "persistence_revision": int(persistence_revision),
        "schema_version": str(schema_version),
        "session_id": str(session_id),
        "state_revision": int(state_revision),
    }


def compute_canonical_payload_digest(
    *,
    schema_version: str,
    company_id: str,
    session_id: str,
    state_revision: int,
    persistence_revision: int,
    dag_id: str,
    book_items: Sequence[BookCategorizeItemPayload],
) -> str:
    """
    Compute 64-hex-character SHA-256 digest of canonical semantic request.
    Identical across Go and Python.
    """
    canonical_dict = compute_canonical_semantic_dict(
        schema_version=schema_version,
        company_id=company_id,
        session_id=session_id,
        state_revision=state_revision,
        persistence_revision=persistence_revision,
        dag_id=dag_id,
        book_items=book_items,
    )
    serialized = json.dumps(canonical_dict, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(serialized.encode("utf-8")).hexdigest()

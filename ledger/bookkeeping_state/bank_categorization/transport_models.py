from __future__ import annotations

import hashlib
import json
from datetime import date
from typing import Annotated, Any, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationItem,
    ResidualBankCategorizationView,
)
from bookkeeping_state.domain.enums import Direction

BANK_CATEGORIZE_SCHEMA_VERSION = "bookkeeping.ase.bank_categorize.v1"
BANK_CATEGORIZATION_DAG_ID = "bookkeeping_bank_categorization_v1"
DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT = (
    "worker.inbox.bookkeeping_ase_bank_categorizer"
)
BANK_CATEGORIZE_QUEUE_GROUP = "bookkeeping_ase_bank_categorizer_group"

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]

FORBIDDEN_SIMULATOR_AND_LEGACY_FIELDS = frozenset({
    "ground_truth",
    "target_account",
    "eval_label",
    "simulator_flag",
    "simulator_override",
    "true_account_code",
    "label",
    "book_item_id",
    "source_artifact_kind",
    "bookkeeping_role",
    "active_bank_account_id",
    "existing_account_code",
    "existing_classification_id",
    "safe_evidence_summaries",
})


class BankCategorizeItemPayload(BaseModel):
    """
    Wire item transport payload representing one residual bank movement.

    Derived strictly from ResidualBankCategorizationItem.
    Excludes truth, eval, simulator, and invoice/bill BookItem fields.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    bank_account_id: Identifier
    residual_amount_units: int = Field(
        ...,
        gt=0,
        description="Residual unmatched amount in solver units (10,000 units = 1.0000 currency unit).",
    )
    original_amount_units: int = Field(
        ...,
        gt=0,
        description="Original observed bank statement movement amount in solver units.",
    )
    direction: str = Field(
        ...,
        description="Cash movement direction: 'INFLOW' or 'OUTFLOW'.",
    )
    date: str = Field(
        ...,
        description="Observed transaction date in ISO format (YYYY-MM-DD).",
    )
    currency: str = Field(..., min_length=3, max_length=3)
    description: str = Field(
        ...,
        min_length=1,
        description="Bank statement description.",
    )
    reference: str | None = Field(
        default=None,
        description="Extracted payment/bank reference if available.",
    )
    provenance_refs: tuple[Identifier, ...] = Field(
        default_factory=tuple,
        description="Authoritative source artifact IDs establishing this observation.",
    )
    bank_account_name: str | None = Field(
        default=None,
        description="Human-readable name of the bank account.",
    )
    institution_name: str | None = Field(
        default=None,
        description="Bank/institution name (e.g. Attijariwafa, Chase).",
    )
    counterparty_name: str | None = Field(
        default=None,
        description="Resolved counterparty name if available.",
    )

    @property
    def amount_units(self) -> int:
        return self.residual_amount_units

    @property
    def amount(self) -> int:
        return self.residual_amount_units

    @field_validator("currency")
    @classmethod
    def validate_and_normalize_currency(cls, value: str) -> str:
        val = value.strip().upper()
        if len(val) != 3 or not val.isalpha():
            raise ValueError(f"Invalid currency code: {value!r}")
        return val

    @field_validator("direction")
    @classmethod
    def validate_direction(cls, value: str) -> str:
        val = value.strip().upper()
        if val in ("BANK_OUTFLOW", "OUTFLOW"):
            return "OUTFLOW"
        if val in ("BANK_INFLOW", "INFLOW"):
            return "INFLOW"
        raise ValueError(
            f"Invalid direction: expected 'INFLOW' or 'OUTFLOW', got {value!r}"
        )

    @model_validator(mode="after")
    def validate_production_staged_item(self) -> "BankCategorizeItemPayload":
        if not self.bank_item_id.startswith("staged:"):
            raise ValueError(
                f"BankCategorizeItemPayload requires authoritative 'staged:<uuid>' ID, got {self.bank_item_id!r}"
            )
        if self.residual_amount_units <= 0:
            raise ValueError(
                f"Residual amount units must be positive, got {self.residual_amount_units}"
            )
        if self.original_amount_units <= 0:
            raise ValueError(
                f"Original amount units must be positive, got {self.original_amount_units}"
            )
        if self.residual_amount_units > self.original_amount_units:
            raise ValueError(
                f"Residual amount ({self.residual_amount_units}) exceeds original amount ({self.original_amount_units})"
            )
        return self

    @classmethod
    def from_item(
        cls, item: ResidualBankCategorizationItem
    ) -> "BankCategorizeItemPayload":
        dir_val = (
            item.direction.value
            if isinstance(item.direction, Direction)
            else str(item.direction)
        )
        date_str = (
            item.date.isoformat()
            if isinstance(item.date, date)
            else str(item.date)
        )
        return cls(
            bank_item_id=item.bank_item_id,
            bank_account_id=item.bank_account_id,
            residual_amount_units=item.residual_amount_units,
            original_amount_units=item.original_amount_units,
            direction=dir_val,
            date=date_str,
            currency=item.currency,
            description=item.description,
            reference=item.reference,
            provenance_refs=tuple(str(x) for x in item.provenance_refs),
            bank_account_name=item.bank_account_name,
            institution_name=item.institution_name,
            counterparty_name=item.counterparty_name,
        )

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {
            "bank_item_id": self.bank_item_id,
            "bank_account_id": self.bank_account_id,
            "residual_amount_units": self.residual_amount_units,
            "original_amount_units": self.original_amount_units,
            "direction": self.direction,
            "date": self.date,
            "currency": self.currency,
            "description": self.description,
            "provenance_refs": list(self.provenance_refs),
        }
        if self.reference is not None:
            d["reference"] = self.reference
        if self.bank_account_name is not None:
            d["bank_account_name"] = self.bank_account_name
        if self.institution_name is not None:
            d["institution_name"] = self.institution_name
        if self.counterparty_name is not None:
            d["counterparty_name"] = self.counterparty_name
        return d

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "BankCategorizeItemPayload":
        for forbidden_key in FORBIDDEN_SIMULATOR_AND_LEGACY_FIELDS:
            if forbidden_key in data:
                raise ValueError(
                    f"Forbidden field {forbidden_key!r} detected in BankCategorizeItemPayload"
                )
        return cls.model_validate(data)


class BankCategorizeRequestEnvelope(BaseModel):
    """
    Versioned request envelope for residual bank categorization over NATS / ASE.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    schema_version: str
    request_id: Identifier
    idempotency_key: Identifier
    company_id: Identifier
    session_id: Identifier
    state_revision: int = Field(..., ge=0)
    persistence_revision: int = Field(default=0, ge=0)
    dag_id: Identifier = BANK_CATEGORIZATION_DAG_ID
    requested_at: str
    bank_items: tuple[BankCategorizeItemPayload, ...] = ()

    @property
    def items(self) -> tuple[BankCategorizeItemPayload, ...]:
        return self.bank_items

    @model_validator(mode="after")
    def validate_envelope(self) -> "BankCategorizeRequestEnvelope":
        if self.schema_version != BANK_CATEGORIZE_SCHEMA_VERSION:
            raise ValueError(
                f"Unsupported schema_version: expected {BANK_CATEGORIZE_SCHEMA_VERSION!r}, got {self.schema_version!r}"
            )
        seen_ids: set[str] = set()
        for it in self.bank_items:
            if it.bank_item_id in seen_ids:
                raise ValueError(
                    f"Duplicate bank_item_id {it.bank_item_id!r} in request"
                )
            seen_ids.add(it.bank_item_id)
        return self

    @classmethod
    def from_view(
        cls,
        view: ResidualBankCategorizationView,
        *,
        request_id: str,
        idempotency_key: str,
        dag_id: str = BANK_CATEGORIZATION_DAG_ID,
        requested_at: str,
    ) -> "BankCategorizeRequestEnvelope":
        items = tuple(
            BankCategorizeItemPayload.from_item(it) for it in view.items
        )
        return cls(
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            request_id=request_id,
            idempotency_key=idempotency_key,
            company_id=view.company_id,
            session_id=view.session_id,
            state_revision=view.state_revision,
            persistence_revision=view.persistence_revision,
            dag_id=dag_id,
            requested_at=requested_at,
            bank_items=items,
        )

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
            "bank_items": [
                item.to_dict()
                for item in sorted(self.bank_items, key=lambda x: x.bank_item_id)
            ],
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "BankCategorizeRequestEnvelope":
        raw_items = data.get("bank_items")
        if raw_items is None:
            raw_items = data.get("items", ())
        items = tuple(
            BankCategorizeItemPayload.from_dict(item) for item in raw_items
        )
        return cls(
            schema_version=str(data["schema_version"]),
            request_id=str(data["request_id"]),
            idempotency_key=str(data["idempotency_key"]),
            company_id=str(data["company_id"]),
            session_id=str(data["session_id"]),
            state_revision=int(data["state_revision"]),
            persistence_revision=int(data.get("persistence_revision", 0)),
            dag_id=str(data.get("dag_id", BANK_CATEGORIZATION_DAG_ID)),
            requested_at=str(data["requested_at"]),
            bank_items=items,
        )


class BankOutcomePayload(BaseModel):
    """
    Terminal categorization result for one residual bank item.

    Supports exactly two terminal variants:
    1. CLASSIFIED (requires account_code, forbids hold_reason)
    2. HOLD (requires hold_reason, forbids account_code)
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    status: str = Field(..., description="'CLASSIFIED' or 'HOLD'.")
    account_code: str | None = Field(default=None)
    confidence: float | None = Field(default=None, ge=0.0, le=1.0)
    rationale: str | None = Field(default=None)
    evidence_refs: tuple[Identifier, ...] = Field(default_factory=tuple)
    ase_node_id: str | None = Field(default=None)
    terminal_property: str | None = Field(default=None)
    hold_reason: str | None = Field(default=None)
    required_evidence: tuple[str, ...] = Field(default_factory=tuple)
    candidate_source: str | None = Field(default=None)
    constrained_macro: str | None = Field(default=None)
    candidate_codes: tuple[str, ...] = Field(default_factory=tuple)

    @field_validator("status")
    @classmethod
    def validate_status(cls, value: str) -> str:
        val = value.strip().upper()
        if val not in ("CLASSIFIED", "HOLD"):
            raise ValueError(
                f"Invalid outcome status: expected 'CLASSIFIED' or 'HOLD', got {value!r}"
            )
        return val

    @model_validator(mode="after")
    def validate_terminal_variant(self) -> "BankOutcomePayload":
        if not self.bank_item_id.startswith("staged:"):
            raise ValueError(
                f"BankOutcomePayload requires 'staged:<uuid>' bank_item_id, got {self.bank_item_id!r}"
            )

        if self.status == "CLASSIFIED":
            if not self.account_code or not self.account_code.strip():
                raise ValueError("CLASSIFIED outcome requires account_code")
            if self.hold_reason is not None and self.hold_reason.strip():
                raise ValueError(
                    "CLASSIFIED outcome must not specify hold_reason"
                )
        elif self.status == "HOLD":
            if self.account_code is not None and self.account_code.strip():
                raise ValueError("HOLD outcome must not specify account_code")
            if not self.hold_reason or not self.hold_reason.strip():
                raise ValueError("HOLD outcome requires hold_reason")

        return self

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {
            "bank_item_id": self.bank_item_id,
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
    def from_dict(cls, data: dict[str, Any]) -> "BankOutcomePayload":
        return cls.model_validate(data)


class ProviderIssuePayload(BaseModel):
    """Explicit error or diagnostic reported during processing."""

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: str | None = None
    code: str = "UNKNOWN_ERROR"
    message: str = ""

    def to_dict(self) -> dict[str, Any]:
        return {
            "bank_item_id": self.bank_item_id,
            "code": self.code,
            "message": self.message,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "ProviderIssuePayload":
        return cls.model_validate(data)


class BankCategorizeResponseEnvelope(BaseModel):
    """
    Versioned response envelope for residual bank categorization over NATS / ASE.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    schema_version: str
    request_id: Identifier
    idempotency_key: Identifier
    session_id: Identifier
    state_revision: int = Field(..., ge=0)
    dag_id: Identifier = BANK_CATEGORIZATION_DAG_ID
    dag_run_id: str = ""
    status: str = Field(default="SUCCESS")
    outcomes: tuple[BankOutcomePayload, ...] = ()
    provider_issues: tuple[ProviderIssuePayload, ...] = ()

    @model_validator(mode="after")
    def validate_envelope(self) -> "BankCategorizeResponseEnvelope":
        if self.schema_version != BANK_CATEGORIZE_SCHEMA_VERSION:
            raise ValueError(
                f"Unsupported schema_version: expected {BANK_CATEGORIZE_SCHEMA_VERSION!r}, got {self.schema_version!r}"
            )
        seen_ids: set[str] = set()
        for o in self.outcomes:
            if o.bank_item_id in seen_ids:
                raise ValueError(
                    f"Duplicate bank_item_id {o.bank_item_id!r} in response outcomes"
                )
            seen_ids.add(o.bank_item_id)
        return self

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
    def from_dict(cls, data: dict[str, Any]) -> "BankCategorizeResponseEnvelope":
        raw_outcomes = data.get("outcomes") or ()
        outcomes = tuple(
            BankOutcomePayload.from_dict(o) for o in raw_outcomes
        )
        raw_issues = data.get("provider_issues") or ()
        issues = tuple(
            ProviderIssuePayload.from_dict(p)
            for p in raw_issues
        )
        return cls(
            schema_version=str(data["schema_version"]),
            request_id=str(data["request_id"]),
            idempotency_key=str(data["idempotency_key"]),
            session_id=str(data["session_id"]),
            state_revision=int(data["state_revision"]),
            dag_id=str(data.get("dag_id", BANK_CATEGORIZATION_DAG_ID)),
            dag_run_id=str(data.get("dag_run_id", "")),
            status=str(data.get("status", "SUCCESS")),
            outcomes=outcomes,
            provider_issues=issues,
        )


def compute_canonical_bank_semantic_dict(
    *,
    schema_version: str,
    company_id: str,
    session_id: str,
    state_revision: int,
    persistence_revision: int,
    dag_id: str,
    bank_items: Sequence[
        BankCategorizeItemPayload | ResidualBankCategorizationItem
    ] | None = None,
    items: Sequence[
        BankCategorizeItemPayload | ResidualBankCategorizationItem
    ] | None = None,
) -> dict[str, Any]:
    """
    Produce deterministically ordered, canonicalized semantic request dictionary.
    Binds the exact accounting problem and excludes transport volatility
    (request_id, requested_at, idempotency_key, retry counts).
    """
    target_items = bank_items if bank_items is not None else items
    if target_items is None:
        target_items = ()
    sorted_items = sorted(target_items, key=lambda it: it.bank_item_id)
    canonical_items: list[dict[str, Any]] = []
    for it in sorted_items:
        unique_refs = sorted(list(set(str(x) for x in it.provenance_refs)))
        dir_val = (
            it.direction.value
            if isinstance(it.direction, Direction)
            else str(it.direction)
        )
        if dir_val in ("BANK_OUTFLOW", "OUTFLOW"):
            dir_val = "OUTFLOW"
        elif dir_val in ("BANK_INFLOW", "INFLOW"):
            dir_val = "INFLOW"
        date_str = (
            it.date.isoformat()
            if isinstance(it.date, date)
            else str(it.date)
        )
        canonical_items.append({
            "bank_account_id": str(it.bank_account_id),
            "bank_account_name": str(it.bank_account_name or ""),
            "bank_item_id": str(it.bank_item_id),
            "counterparty_name": str(it.counterparty_name or ""),
            "currency": str(it.currency),
            "date": date_str,
            "description": str(it.description or ""),
            "direction": dir_val,
            "institution_name": str(it.institution_name or ""),
            "original_amount_units": int(it.original_amount_units),
            "provenance_refs": unique_refs,
            "reference": str(it.reference or ""),
            "residual_amount_units": int(it.residual_amount_units),
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


def compute_canonical_bank_payload_digest(
    *,
    schema_version: str,
    company_id: str,
    session_id: str,
    state_revision: int,
    persistence_revision: int,
    dag_id: str,
    bank_items: Sequence[
        BankCategorizeItemPayload | ResidualBankCategorizationItem
    ] | None = None,
    items: Sequence[
        BankCategorizeItemPayload | ResidualBankCategorizationItem
    ] | None = None,
) -> str:
    """
    Compute 64-hex-character SHA-256 digest of canonical semantic bank request.
    Identical across Go and Python.
    """
    canonical_dict = compute_canonical_bank_semantic_dict(
        schema_version=schema_version,
        company_id=company_id,
        session_id=session_id,
        state_revision=state_revision,
        persistence_revision=persistence_revision,
        dag_id=dag_id,
        bank_items=bank_items,
        items=items,
    )
    serialized = json.dumps(
        canonical_dict,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    )
    return hashlib.sha256(serialized.encode("utf-8")).hexdigest()

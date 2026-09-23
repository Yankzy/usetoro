from __future__ import annotations

from datetime import date, datetime
from enum import StrEnum
import hashlib
import hmac
import json
import os
from typing import Annotated, Any, Literal, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    field_validator,
    model_validator,
)

from bookkeeping_state.domain.classifications import (
    ClassificationSource,
)
from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.hypotheses import (
    ReconciliationHypothesis,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.domain.routing import (
    RoutingDecisionSource,
)


class CommandSource(StrEnum):
    """
    Runtime subsystem that issued a command.

    This is distinct from the provenance source recorded on a durable
    bookkeeping decision artifact.
    """

    ROUTING = "ROUTING"
    ASE_DAG = "ASE_DAG"
    RECONCILIATION = "RECONCILIATION"
    HUMAN = "HUMAN"
    SYSTEM = "SYSTEM"


from bookkeeping_state.domain.evidence import (
    BookItemEvidenceType,
    EvidenceSource,
)


class CommandType(StrEnum):
    CREATE_ROUTING_DECISION = "CREATE_ROUTING_DECISION"
    INVALIDATE_ROUTING_DECISION = "INVALIDATE_ROUTING_DECISION"

    CREATE_CLASSIFICATION = "CREATE_CLASSIFICATION"
    INVALIDATE_CLASSIFICATION = "INVALIDATE_CLASSIFICATION"

    CREATE_RECONCILIATION = "CREATE_RECONCILIATION"
    INVALIDATE_RECONCILIATION = "INVALIDATE_RECONCILIATION"

    ASSERT_BOOK_ITEM_EVIDENCE = "ASSERT_BOOK_ITEM_EVIDENCE"
    INVALIDATE_BOOK_ITEM_EVIDENCE = "INVALIDATE_BOOK_ITEM_EVIDENCE"

    APPLY_PAYMENT = "APPLY_PAYMENT"

    RECORD_RESIDUAL_BANK_CLASSIFICATION = "RECORD_RESIDUAL_BANK_CLASSIFICATION"
    INVALIDATE_RESIDUAL_BANK_CLASSIFICATION = "INVALIDATE_RESIDUAL_BANK_CLASSIFICATION"
    POST_RESIDUAL_BANK_CLASSIFICATION = "POST_RESIDUAL_BANK_CLASSIFICATION"


class CommandBase(BaseModel):
    """
    Base contract for every requested BookkeepingState transition.

    expected_state_revision provides optimistic concurrency against the live
    in-memory BookkeepingState.

    Persistence has its own independent revision check at commit time.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    command_id: str = Field(..., min_length=1)

    expected_state_revision: int = Field(
        ...,
        ge=0,
    )

    source: CommandSource

    session_id: str = Field(
        ...,
        min_length=1,
    )

    issued_at: datetime

    @field_validator("issued_at")
    @classmethod
    def require_timezone_aware_issued_at(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError(
                "issued_at must be timezone-aware"
            )

        return value


# ======================================================================
# Routing
# ======================================================================


class CreateRoutingDecisionCommand(CommandBase):
    """
    Request creation of one immutable RoutingDecision.

    If supersedes_routing_decision_id is supplied, the new decision replaces
    that historical decision for the same BookItem.

    The old artifact itself remains untouched.
    """

    command_type: Literal[
        CommandType.CREATE_ROUTING_DECISION
    ] = CommandType.CREATE_ROUTING_DECISION

    routing_decision_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    bank_account_id: str = Field(
        ...,
        min_length=1,
    )

    decision_source: RoutingDecisionSource

    utility: int | None = Field(
        default=None,
        ge=0,
        le=1000,
    )

    solver_run_id: str | None = None

    supersedes_routing_decision_id: str | None = None

    @field_validator(
        "solver_run_id",
        "supersedes_routing_decision_id",
    )
    @classmethod
    def reject_empty_optional_identifiers(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "Optional identifier cannot be empty"
            )

        return value


class InvalidateRoutingDecisionCommand(CommandBase):
    """
    Request creation of a durable RoutingDecisionInvalidation artifact.

    Invalidation never deletes the original RoutingDecision.
    """

    command_type: Literal[
        CommandType.INVALIDATE_ROUTING_DECISION
    ] = CommandType.INVALIDATE_ROUTING_DECISION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    routing_decision_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Classification
# ======================================================================


class CreateClassificationCommand(CommandBase):
    """
    Request creation of one immutable ClassificationDecision.

    ASE interpretation becomes bookkeeping truth only after this command is
    accepted by the TransitionEngine.
    """

    command_type: Literal[
        CommandType.CREATE_CLASSIFICATION
    ] = CommandType.CREATE_CLASSIFICATION

    classification_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    account_code: str = Field(
        ...,
        min_length=1,
    )

    classification_source: ClassificationSource

    confidence: float | None = Field(
        default=None,
        ge=0.0,
        le=1.0,
    )

    rationale: str | None = None

    evidence_refs: tuple[str, ...] = ()

    supersedes_classification_id: str | None = None

    dag_run_id: str | None = None
    ase_node_id: str | None = None

    @field_validator(
        "supersedes_classification_id",
        "dag_run_id",
        "ase_node_id",
    )
    @classmethod
    def reject_empty_optional_identifiers(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "Optional identifier cannot be empty"
            )

        return value


class InvalidateClassificationCommand(CommandBase):
    """
    Request creation of a ClassificationInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_CLASSIFICATION
    ] = CommandType.INVALIDATE_CLASSIFICATION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    classification_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Reconciliation
# ======================================================================


class CreateReconciliationCommand(CommandBase):
    """
    Request creation of one immutable Reconciliation artifact.

    The model intentionally permits commands that may be economically invalid.

    For example:

        allocation exceeds remaining capacity
        currencies differ
        directions are incompatible
        hypothesis is stale

    Those commands must remain representable so the TransitionEngine can reject
    them deterministically and prove that state remained unchanged.
    """

    command_type: Literal[
        CommandType.CREATE_RECONCILIATION
    ] = CommandType.CREATE_RECONCILIATION

    reconciliation_id: str = Field(
        ...,
        min_length=1,
    )

    bank_allocations: tuple[
        BankAllocation,
        ...
    ] = Field(
        ...,
        min_length=1,
    )

    book_allocations: tuple[
        BookAllocation,
        ...
    ] = Field(
        ...,
        min_length=1,
    )

    source_hypothesis_id: str | None = None
    source_hypothesis: ReconciliationHypothesis | None = None
    source_hypothesis_admissibility: SemanticAdmissibility | None = None
    source_hypothesis_allocation_support: AllocationSupport | None = None

    evidence_refs: tuple[str, ...] = ()

    semantic_rationale: str | None = None

    @field_validator("source_hypothesis_id")
    @classmethod
    def reject_empty_hypothesis_id(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "source_hypothesis_id cannot be empty"
            )

        return value


class InvalidateReconciliationCommand(CommandBase):
    """
    Request creation of a ReconciliationInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_RECONCILIATION
    ] = CommandType.INVALIDATE_RECONCILIATION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    reconciliation_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Evidence
# ======================================================================


class AssertBookItemEvidenceCommand(CommandBase):
    """
    Request creation of one immutable BookItemEvidenceAssertion.

    If supersedes_assertion_id is supplied, the new assertion replaces
    that historical assertion for the same BookItem and same evidence_type.
    """

    command_type: Literal[
        CommandType.ASSERT_BOOK_ITEM_EVIDENCE
    ] = CommandType.ASSERT_BOOK_ITEM_EVIDENCE

    assertion_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    evidence_type: BookItemEvidenceType

    value: str = Field(
        ...,
        min_length=1,
        max_length=2000,
    )

    document_ids: tuple[str, ...] = Field(
        default_factory=tuple,
    )

    evidence_source: EvidenceSource = EvidenceSource.HUMAN_ASSERTION

    source_document_id: str | None = None

    confidence: float = Field(
        default=1.0,
        ge=0.0,
        le=1.0,
    )

    reason: str | None = None

    metadata: dict[str, Any] = Field(
        default_factory=dict,
    )

    supersedes_assertion_id: str | None = None


class InvalidateBookItemEvidenceCommand(CommandBase):
    """
    Request creation of a BookItemEvidenceInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_BOOK_ITEM_EVIDENCE
    ] = CommandType.INVALIDATE_BOOK_ITEM_EVIDENCE

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    assertion_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


def hkdf_sha256(ikm: bytes, *, salt: bytes = b"", info: bytes = b"", length: int = 32) -> bytes:
    """RFC 5869 HMAC-SHA256 Key Derivation Function (HKDF)."""
    if not salt:
        salt = bytes([0] * 32)
    prk = hmac.new(salt, ikm, hashlib.sha256).digest()
    okm = b""
    t = b""
    i = 1
    while len(okm) < length:
        t = hmac.new(prk, t + info + bytes([i]), hashlib.sha256).digest()
        okm += t
        i += 1
    return okm[:length]


def get_stage1_capability_secret(secret_key: bytes | None = None) -> bytes:
    """
    Source the HMAC secret for Stage1ExecutionCapability.

    Resolution:
    1. Explicit secret_key argument (ONLY for tests / trusted dependency injection)
    2. STAGE1_CAPABILITY_SECRET from environment or settings
    3. HKDF-derived key from a non-empty Django SECRET_KEY
    4. Fail closed (raise RuntimeError)
    """
    if secret_key:
        return secret_key

    env_secret = os.environ.get("STAGE1_CAPABILITY_SECRET")
    if env_secret:
        return env_secret.encode("utf-8")

    try:
        from django.conf import settings

        cfg_secret = getattr(settings, "STAGE1_CAPABILITY_SECRET", None)
        if cfg_secret:
            return cfg_secret.encode("utf-8") if isinstance(cfg_secret, str) else cfg_secret

        django_secret = getattr(settings, "SECRET_KEY", None)
        if django_secret:
            return hkdf_sha256(
                django_secret.encode("utf-8") if isinstance(django_secret, str) else django_secret,
                salt=b"stage1-capability-salt",
                info=b"stage1-execution-capability-signing-key",
                length=32,
            )
    except Exception:
        pass

    raise RuntimeError(
        "Stage-1 execution capability signing secret is not configured. "
        "STAGE1_CAPABILITY_SECRET or a valid Django SECRET_KEY must be provided."
    )


def canonicalize_stage1_intent_payload(
    *,
    bank_item_id: str,
    bank_account_id: str,
    direction: str | Direction,
    currency: str,
    total_amount_units: int,
    payment_date: date | str,
    allocations: Sequence[Any],
    base_state_revision: int,
    base_state_fingerprint: str,
    session_id: str,
) -> bytes:
    """
    Deterministically serialize the exact economic payment intent and snapshot boundary.
    """
    normalized_allocs: list[dict[str, Any]] = []
    for alloc in allocations:
        b_id = getattr(alloc, "book_item_id", None)
        if b_id is None:
            b_id = getattr(getattr(alloc, "obligation", None), "book_item_id", None)
        if b_id is None and isinstance(alloc, dict):
            b_id = alloc.get("book_item_id")
        if b_id is None and isinstance(alloc, (tuple, list)) and len(alloc) >= 2:
            b_id = alloc[0]
            units_val = alloc[1]
        else:
            units_val = getattr(alloc, "amount_units", None)
            if units_val is None:
                units_val = getattr(getattr(alloc, "allocated_amount", None), "units", None)
            if units_val is None and isinstance(alloc, dict):
                units_val = alloc.get("amount_units")

        if b_id is None or units_val is None:
            raise ValueError(f"Invalid allocation for capability canonicalization: {alloc}")

        normalized_allocs.append(
            {
                "amount_units": int(units_val),
                "book_item_id": str(b_id),
            }
        )

    normalized_allocs.sort(key=lambda x: x["book_item_id"])

    dir_str = direction.value if isinstance(direction, Direction) else str(direction)
    p_date_str = payment_date.isoformat() if isinstance(payment_date, date) else str(payment_date)

    payload_dict = {
        "allocations": normalized_allocs,
        "bank_account_id": str(bank_account_id),
        "bank_item_id": str(bank_item_id),
        "base_state_fingerprint": str(base_state_fingerprint),
        "base_state_revision": int(base_state_revision),
        "currency": str(currency).upper(),
        "direction": dir_str,
        "payment_date": p_date_str,
        "session_id": str(session_id),
        "total_amount_units": int(total_amount_units),
    }
    return json.dumps(payload_dict, sort_keys=True, separators=(",", ":")).encode("utf-8")


def compute_stage1_intent_digest(canonical_payload: bytes) -> str:
    return hashlib.sha256(canonical_payload).hexdigest()


class Stage1ExecutionCapability(BaseModel):
    """
    Opaque capability token minted exclusively by the Stage-1 coordinator
    proving that the EXACT PaymentPostingIntent was selected and authorized
    for Stage-1 accounting execution against a specific state snapshot.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: str = Field(
        ...,
        min_length=1,
    )
    session_id: str = Field(
        ...,
        min_length=1,
    )
    base_state_revision: int = Field(
        ...,
        ge=0,
    )
    base_state_fingerprint: str = Field(
        ...,
        min_length=1,
    )
    intent_digest: str = Field(
        ...,
        min_length=1,
    )
    token: str = Field(
        ...,
        min_length=1,
    )


def mint_stage1_capability(
    *,
    session_id: str,
    base_state_revision: int,
    base_state_fingerprint: str,
    intent: Any | None = None,
    bank_item_id: str | None = None,
    bank_account_id: str | None = None,
    direction: str | Direction | None = None,
    currency: str | None = None,
    total_amount_units: int | None = None,
    payment_date: date | str | None = None,
    allocations: Sequence[Any] | None = None,
    secret_key: bytes | None = None,
) -> Stage1ExecutionCapability:
    if intent is not None:
        b_id = getattr(intent, "bank_item_id")
        ba_id = getattr(intent, "bank_account_id")
        dir_val = getattr(intent, "direction")
        curr = getattr(intent, "currency")
        tot = int(
            getattr(
                intent,
                "total_amount_units",
                getattr(getattr(intent, "total_amount", None), "units", 0),
            )
        )
        p_date = getattr(intent, "payment_date")
        allocs = getattr(
            intent, "obligation_allocations", getattr(intent, "allocations", ())
        )
    else:
        assert bank_item_id is not None, "bank_item_id is required"
        assert bank_account_id is not None, "bank_account_id is required"
        assert direction is not None, "direction is required"
        assert currency is not None, "currency is required"
        assert total_amount_units is not None, "total_amount_units is required"
        assert payment_date is not None, "payment_date is required"
        assert allocations is not None, "allocations is required"
        b_id = bank_item_id
        ba_id = bank_account_id
        dir_val = direction
        curr = currency
        tot = total_amount_units
        p_date = payment_date
        allocs = allocations

    payload_bytes = canonicalize_stage1_intent_payload(
        bank_item_id=b_id,
        bank_account_id=ba_id,
        direction=dir_val,
        currency=curr,
        total_amount_units=tot,
        payment_date=p_date,
        allocations=allocs,
        base_state_revision=base_state_revision,
        base_state_fingerprint=base_state_fingerprint,
        session_id=session_id,
    )
    digest = compute_stage1_intent_digest(payload_bytes)
    effective_secret = get_stage1_capability_secret(secret_key)
    token = hmac.new(
        effective_secret, digest.encode("utf-8"), hashlib.sha256
    ).hexdigest()

    return Stage1ExecutionCapability(
        bank_item_id=b_id,
        session_id=session_id,
        base_state_revision=base_state_revision,
        base_state_fingerprint=base_state_fingerprint,
        intent_digest=digest,
        token=token,
    )


def verify_stage1_capability(
    capability: Stage1ExecutionCapability,
    *,
    expected_bank_item_id: str,
    expected_bank_account_id: str,
    expected_direction: str | Direction,
    expected_currency: str,
    expected_total_amount_units: int,
    expected_payment_date: date | str,
    expected_allocations: Sequence[Any],
    expected_session_id: str,
    expected_state_revision: int,
    expected_state_fingerprint: str,
    secret_key: bytes | None = None,
) -> bool:
    if (
        capability.bank_item_id != expected_bank_item_id
        or capability.session_id != expected_session_id
        or capability.base_state_revision != expected_state_revision
        or capability.base_state_fingerprint != expected_state_fingerprint
    ):
        return False

    try:
        canonical_bytes = canonicalize_stage1_intent_payload(
            bank_item_id=expected_bank_item_id,
            bank_account_id=expected_bank_account_id,
            direction=expected_direction,
            currency=expected_currency,
            total_amount_units=expected_total_amount_units,
            payment_date=expected_payment_date,
            allocations=expected_allocations,
            base_state_revision=expected_state_revision,
            base_state_fingerprint=expected_state_fingerprint,
            session_id=expected_session_id,
        )
    except Exception:
        return False

    expected_digest = compute_stage1_intent_digest(canonical_bytes)
    if not hmac.compare_digest(capability.intent_digest, expected_digest):
        return False

    effective_secret = get_stage1_capability_secret(secret_key)
    expected_token = hmac.new(
        effective_secret,
        expected_digest.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()
    return hmac.compare_digest(capability.token, expected_token)


class PaymentApplicationObligationAllocation(BaseModel):
    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )
    amount_units: int = Field(
        ...,
        gt=0,
    )


class ApplyPaymentCommand(CommandBase):
    """
    Request execution of an accepted Stage-1 payment application against
    the Django accounting kernel and atomic persistence of its executed provenance.
    """

    command_type: Literal[
        CommandType.APPLY_PAYMENT
    ] = CommandType.APPLY_PAYMENT

    source: CommandSource = CommandSource.SYSTEM

    payment_application_id: str = Field(
        ...,
        min_length=1,
    )

    bank_item_id: str = Field(
        ...,
        min_length=1,
    )

    bank_account_id: str = Field(
        ...,
        min_length=1,
    )

    direction: Direction

    payment_date: date

    total_amount_units: int = Field(
        ...,
        gt=0,
    )

    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    allocations: tuple[
        PaymentApplicationObligationAllocation,
        ...
    ] = Field(
        ...,
        min_length=1,
    )

    capability: Stage1ExecutionCapability

    @classmethod
    def from_intent(
        cls,
        *,
        command_id: str,
        expected_state_revision: int,
        session_id: str,
        issued_at: datetime,
        payment_application_id: str,
        plan_intent: Any,
        capability: Stage1ExecutionCapability,
    ) -> ApplyPaymentCommand:
        ba_id = getattr(plan_intent, "bank_account_id", None)
        if not ba_id:
            raise ValueError(f"Missing bank_account_id on plan_intent: {plan_intent}")
        dir_val = getattr(plan_intent, "direction", None)
        if not dir_val:
            raise ValueError(f"Missing direction on plan_intent: {plan_intent}")
        p_date = getattr(plan_intent, "payment_date", None)
        if not p_date:
            raise ValueError(f"Missing payment_date on plan_intent: {plan_intent}")

        if hasattr(plan_intent, "obligation_allocations"):
            allocs = tuple(
                PaymentApplicationObligationAllocation(
                    book_item_id=alloc.book_item_id,
                    amount_units=(
                        int(alloc.amount_units)
                        if isinstance(alloc.amount_units, str)
                        else int(alloc.amount_units)
                    ),
                )
                for alloc in plan_intent.obligation_allocations
            )
            total_units = (
                int(plan_intent.total_amount_units)
                if isinstance(plan_intent.total_amount_units, str)
                else int(plan_intent.total_amount_units)
            )
        elif hasattr(plan_intent, "allocations"):
            alloc_list: list[PaymentApplicationObligationAllocation] = []
            for alloc in plan_intent.allocations:
                b_id = getattr(alloc.obligation, "book_item_id", None) or getattr(
                    alloc, "book_item_id", None
                )
                if not b_id:
                    raise ValueError(f"Missing book_item_id in allocation: {alloc}")
                alloc_list.append(
                    PaymentApplicationObligationAllocation(
                        book_item_id=str(b_id),
                        amount_units=int(
                            getattr(
                                alloc.allocated_amount,
                                "units",
                                getattr(alloc, "amount_units", 0),
                            )
                        ),
                    )
                )
            allocs = tuple(alloc_list)
            total_units = int(
                getattr(
                    plan_intent.total_amount,
                    "units",
                    getattr(plan_intent, "total_amount_units", 0),
                )
            )
        else:
            raise ValueError(f"Unsupported plan_intent type: {type(plan_intent)}")

        return cls(
            command_id=command_id,
            expected_state_revision=expected_state_revision,
            source=CommandSource.ROUTING,
            session_id=session_id,
            issued_at=issued_at,
            payment_application_id=payment_application_id,
            bank_item_id=plan_intent.bank_item_id,
            bank_account_id=str(ba_id),
            direction=dir_val,
            payment_date=p_date,
            total_amount_units=total_units,
            currency=plan_intent.currency,
            allocations=allocs,
            capability=capability,
        )


# ======================================================================
# Residual Bank Classification
# ======================================================================


class RecordResidualBankClassificationCommand(CommandBase):
    """
    Request creation of one immutable ResidualBankClassificationDecision.

    Applies to residual unmatched bank items (after Stage 1 and Stage 2).
    If supersedes_decision_id is supplied, the new decision supersedes that
    historical decision tip for the same BankItem.
    """

    command_type: Literal[
        CommandType.RECORD_RESIDUAL_BANK_CLASSIFICATION
    ] = CommandType.RECORD_RESIDUAL_BANK_CLASSIFICATION

    expected_persistence_revision: int = Field(
        ...,
        ge=0,
    )

    decision_id: str = Field(
        ...,
        min_length=1,
    )

    bank_item_id: str = Field(
        ...,
        min_length=1,
    )

    bank_account_id: str = Field(
        ...,
        min_length=1,
    )

    original_amount_units: int = Field(
        ...,
        gt=0,
    )

    residual_amount_units: int = Field(
        ...,
        gt=0,
    )

    direction: Direction

    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    request_semantic_digest: str = Field(
        ...,
        min_length=1,
    )

    schema_version: str = Field(
        ...,
        min_length=1,
    )

    dag_id: str = Field(
        ...,
        min_length=1,
    )

    status: ResidualBankClassificationStatus

    account_code: str | None = None
    confidence: float | None = Field(
        default=None,
        ge=0.0,
        le=1.0,
    )
    rationale: str | None = None
    required_evidence: tuple[str, ...] = Field(default_factory=tuple)
    hold_reason: str | None = None
    ase_node_id: str | None = None
    terminal_property: str | None = None
    evidence_refs: tuple[str, ...] = Field(default_factory=tuple)
    supersedes_decision_id: str | None = None

    source: CommandSource = CommandSource.ASE_DAG

    @field_validator("direction", mode="before")
    @classmethod
    def normalize_direction(cls, value: Any) -> Direction:
        val = str(value).strip().upper()
        if val in ("BANK_OUTFLOW", "OUTFLOW"):
            return Direction.OUTFLOW
        if val in ("BANK_INFLOW", "INFLOW"):
            return Direction.INFLOW
        return Direction(val)

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.strip().upper()

    @field_validator(
        "supersedes_decision_id",
        "account_code",
        "hold_reason",
        "rationale",
        "ase_node_id",
        "terminal_property",
    )
    @classmethod
    def reject_empty_optional_strings(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError("Optional string identifier cannot be empty")
        return value

    @model_validator(mode="after")
    def validate_shape(self) -> "RecordResidualBankClassificationCommand":
        if not self.bank_item_id.startswith("staged:"):
            raise ValueError(
                f"bank_item_id must be authoritative 'staged:<uuid>', got {self.bank_item_id!r}"
            )
        if self.residual_amount_units > self.original_amount_units:
            raise ValueError(
                f"residual_amount_units ({self.residual_amount_units}) exceeds "
                f"original_amount_units ({self.original_amount_units})"
            )

        if self.status == ResidualBankClassificationStatus.CLASSIFIED:
            if not self.account_code or not self.account_code.strip():
                raise ValueError("CLASSIFIED residual bank classification requires account_code")
            if self.hold_reason is not None and self.hold_reason.strip():
                raise ValueError("CLASSIFIED residual bank classification must not specify hold_reason")
            if self.confidence is None:
                raise ValueError("CLASSIFIED residual bank classification requires confidence")
            if self.required_evidence:
                raise ValueError("CLASSIFIED residual bank classification must have empty required_evidence")
        elif self.status == ResidualBankClassificationStatus.HOLD:
            if self.account_code is not None and self.account_code.strip():
                raise ValueError("HOLD residual bank classification must not specify account_code")
            if not self.hold_reason or not self.hold_reason.strip():
                raise ValueError("HOLD residual bank classification requires hold_reason")

        return self


class InvalidateResidualBankClassificationCommand(CommandBase):
    """
    Request creation of a ResidualBankClassificationInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_RESIDUAL_BANK_CLASSIFICATION
    ] = CommandType.INVALIDATE_RESIDUAL_BANK_CLASSIFICATION

    expected_persistence_revision: int = Field(
        ...,
        ge=0,
    )

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    decision_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )

    source: CommandSource = CommandSource.HUMAN

    @field_validator("reason")
    @classmethod
    def validate_non_empty_reason(cls, value: str) -> str:
        trimmed = value.strip()
        if not trimmed:
            raise ValueError("reason cannot be empty or blank")
        return trimmed


class PostResidualBankClassificationCommand(CommandBase):
    """
    Request authoritative general ledger posting and direct reconciliation closure
    for an active durable CLASSIFIED residual bank classification decision.
    """

    command_type: Literal[
        CommandType.POST_RESIDUAL_BANK_CLASSIFICATION
    ] = CommandType.POST_RESIDUAL_BANK_CLASSIFICATION

    expected_persistence_revision: int = Field(
        ...,
        ge=0,
    )

    decision_id: str = Field(
        ...,
        min_length=1,
    )

    bank_item_id: str | None = None
    bank_account_id: str | None = None
    residual_amount_units: int | None = Field(default=None, gt=0)
    direction: Direction | None = None
    account_code: str | None = None

    source: CommandSource = CommandSource.SYSTEM

    @field_validator("direction", mode="before")
    @classmethod
    def normalize_direction(cls, value: Any) -> Direction | None:
        if value is None:
            return None
        val = str(value).strip().upper()
        if val in ("BANK_OUTFLOW", "OUTFLOW"):
            return Direction.OUTFLOW
        if val in ("BANK_INFLOW", "INFLOW"):
            return Direction.INFLOW
        return Direction(val)

    @field_validator("bank_item_id", "bank_account_id", "account_code")
    @classmethod
    def reject_empty_optional_strings(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError("Optional string identifier cannot be empty")
        return value


BookkeepingCommand = Annotated[
    (
        CreateRoutingDecisionCommand
        | InvalidateRoutingDecisionCommand
        | CreateClassificationCommand
        | InvalidateClassificationCommand
        | CreateReconciliationCommand
        | InvalidateReconciliationCommand
        | AssertBookItemEvidenceCommand
        | InvalidateBookItemEvidenceCommand
        | ApplyPaymentCommand
        | RecordResidualBankClassificationCommand
        | InvalidateResidualBankClassificationCommand
        | PostResidualBankClassificationCommand
    ),
    Field(discriminator="command_type"),
]
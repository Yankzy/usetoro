from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field

from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
)
from bookkeeping_state_eval.domain.evidence import BookItemEvidenceType
from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.derived import (
    DerivedStateError,
    BookkeepingDerivedState,
    build_derived_state,
)


DIRECTION_COMPATIBILITY_MAP: frozenset[
    tuple[Direction, Direction]
] = frozenset(
    {
        (Direction.OUTFLOW, Direction.OUTFLOW),
        (Direction.INFLOW, Direction.INFLOW),
        (
            Direction.BANK_OUTFLOW,
            Direction.BOOK_BANK_CREDIT,
        ),
        (
            Direction.BANK_INFLOW,
            Direction.BOOK_BANK_DEBIT,
        ),
    }
)
"""
Exact direction compatibility semantics preserved from the existing
reconciliation eval.
"""


class ValidationSeverity(StrEnum):
    ERROR = "ERROR"
    WARNING = "WARNING"


class ValidationCode(StrEnum):
    # Referential integrity
    UNKNOWN_BANK_ACCOUNT = "UNKNOWN_BANK_ACCOUNT"
    UNKNOWN_COUNTERPARTY = "UNKNOWN_COUNTERPARTY"
    UNKNOWN_BOOK_ITEM = "UNKNOWN_BOOK_ITEM"
    UNKNOWN_BANK_ITEM = "UNKNOWN_BANK_ITEM"
    UNKNOWN_DOCUMENT = "UNKNOWN_DOCUMENT"

    UNKNOWN_ROUTING_SUPERSESSION = "UNKNOWN_ROUTING_SUPERSESSION"
    UNKNOWN_ROUTING_INVALIDATION_TARGET = (
        "UNKNOWN_ROUTING_INVALIDATION_TARGET"
    )

    UNKNOWN_CLASSIFICATION_SUPERSESSION = (
        "UNKNOWN_CLASSIFICATION_SUPERSESSION"
    )
    UNKNOWN_CLASSIFICATION_INVALIDATION_TARGET = (
        "UNKNOWN_CLASSIFICATION_INVALIDATION_TARGET"
    )

    UNKNOWN_RECONCILIATION_INVALIDATION_TARGET = (
        "UNKNOWN_RECONCILIATION_INVALIDATION_TARGET"
    )

    UNKNOWN_EVIDENCE_ASSERTION_TARGET = (
        "UNKNOWN_EVIDENCE_ASSERTION_TARGET"
    )
    UNKNOWN_EVIDENCE_SUPERSESSION = "UNKNOWN_EVIDENCE_SUPERSESSION"
    EVIDENCE_SUPERSESSION_SUBJECT_MISMATCH = (
        "EVIDENCE_SUPERSESSION_SUBJECT_MISMATCH"
    )
    EVIDENCE_SUPERSESSION_TYPE_MISMATCH = (
        "EVIDENCE_SUPERSESSION_TYPE_MISMATCH"
    )
    UNKNOWN_EVIDENCE_INVALIDATION_TARGET = (
        "UNKNOWN_EVIDENCE_INVALIDATION_TARGET"
    )

    # Semantic consistency
    BANK_ACCOUNT_CURRENCY_MISMATCH = (
        "BANK_ACCOUNT_CURRENCY_MISMATCH"
    )

    ROUTING_SUPERSESSION_SUBJECT_MISMATCH = (
        "ROUTING_SUPERSESSION_SUBJECT_MISMATCH"
    )

    CLASSIFICATION_SUPERSESSION_SUBJECT_MISMATCH = (
        "CLASSIFICATION_SUPERSESSION_SUBJECT_MISMATCH"
    )

    MULTIPLE_ACTIVE_DECISIONS = "MULTIPLE_ACTIVE_DECISIONS"

    # Reconciliation invariants
    RECONCILIATION_MONETARY_IMBALANCE = (
        "RECONCILIATION_MONETARY_IMBALANCE"
    )

    RECONCILIATION_CURRENCY_MISMATCH = (
        "RECONCILIATION_CURRENCY_MISMATCH"
    )

    RECONCILIATION_DIRECTION_INCOMPATIBILITY = (
        "RECONCILIATION_DIRECTION_INCOMPATIBILITY"
    )

    PARTIAL_BANK_CONSUMPTION = "PARTIAL_BANK_CONSUMPTION"
    PARTIAL_BOOK_CONSUMPTION = "PARTIAL_BOOK_CONSUMPTION"

    BANK_CAPACITY_OVERFLOW = "BANK_CAPACITY_OVERFLOW"
    BOOK_CAPACITY_OVERFLOW = "BOOK_CAPACITY_OVERFLOW"

    INVALID_HYPOTHESIS_PROVENANCE = "INVALID_HYPOTHESIS_PROVENANCE"


class ValidationIssue(BaseModel):
    """
    One deterministic problem discovered in the hydrated bookkeeping world.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    code: ValidationCode
    severity: ValidationSeverity = ValidationSeverity.ERROR

    message: str

    artifact_ids: tuple[str, ...] = Field(
        default_factory=tuple,
    )


class StateValidationReport(BaseModel):
    """
    Deterministic validation result for one BookkeepingState revision.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)

    issues: tuple[ValidationIssue, ...] = Field(
        default_factory=tuple,
    )

    @property
    def is_valid(self) -> bool:
        return not any(
            issue.severity == ValidationSeverity.ERROR
            for issue in self.issues
        )

    @property
    def errors(self) -> tuple[ValidationIssue, ...]:
        return tuple(
            issue
            for issue in self.issues
            if issue.severity == ValidationSeverity.ERROR
        )

    @property
    def warnings(self) -> tuple[ValidationIssue, ...]:
        return tuple(
            issue
            for issue in self.issues
            if issue.severity == ValidationSeverity.WARNING
        )

    def require_valid(self) -> None:
        """
        Raise StateValidationError when the bookkeeping world is invalid.
        """
        if not self.is_valid:
            raise StateValidationError(self)


class StateValidationError(RuntimeError):
    """
    Raised when a caller requires a valid BookkeepingState but deterministic
    invariants fail.
    """

    def __init__(
        self,
        report: StateValidationReport,
    ) -> None:
        self.report = report

        summary = "; ".join(
            f"{issue.code.value}: {issue.message}"
            for issue in report.errors
        )

        super().__init__(
            "BookkeepingState validation failed"
            + (f": {summary}" if summary else "")
        )


def validate_state(
    state: BookkeepingState,
) -> StateValidationReport:
    """
    Perform deterministic validation of the entire hydrated bookkeeping world.

    The function is pure with respect to BookkeepingState:

    - no mutation
    - no persistence
    - no hypothesis generation
    - no LLM calls
    - no repair

    An invalid state remains inspectable so the eval can diagnose exactly
    which invariant failed.
    """

    issues: list[ValidationIssue] = []

    _validate_bank_items(
        state,
        issues,
    )

    _validate_book_items(
        state,
        issues,
    )

    _validate_evidence_artifacts(
        state,
        issues,
    )

    _validate_routing_artifacts(
        state,
        issues,
    )

    _validate_classification_artifacts(
        state,
        issues,
    )

    _validate_reconciliation_artifacts(
        state,
        issues,
    )

    derived = _try_build_derived_state(
        state,
        issues,
    )

    if derived is not None:
        _validate_derived_capacities(
            derived,
            issues,
        )

    return StateValidationReport(
        state_revision=state.revision,
        issues=tuple(
            sorted(
                issues,
                key=_issue_sort_key,
            )
        ),
    )


# ----------------------------------------------------------------------
# Bank artifacts
# ----------------------------------------------------------------------


def _validate_bank_items(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    for bank_item in state.bank_items.values():
        bank_account = state.get_bank_account(
            bank_item.bank_account_id
        )

        if bank_account is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_BANK_ACCOUNT,
                message=(
                    f"BankItem {bank_item.id!r} references unknown "
                    f"BankAccount {bank_item.bank_account_id!r}"
                ),
                artifact_ids=(
                    bank_item.id,
                    bank_item.bank_account_id,
                ),
            )
            continue

        if (
            state.context.policy.require_exact_currency_match
            and bank_item.currency != bank_account.currency
        ):
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .BANK_ACCOUNT_CURRENCY_MISMATCH
                ),
                message=(
                    f"BankItem {bank_item.id!r} currency "
                    f"{bank_item.currency!r} differs from "
                    f"BankAccount {bank_account.id!r} currency "
                    f"{bank_account.currency!r}"
                ),
                artifact_ids=(
                    bank_item.id,
                    bank_account.id,
                ),
            )


# ----------------------------------------------------------------------
# Book artifacts
# ----------------------------------------------------------------------


def _validate_book_items(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    for book_item in state.book_items.values():
        if (
            book_item.counterparty_id is not None
            and state.get_counterparty(
                book_item.counterparty_id
            )
            is None
        ):
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_COUNTERPARTY,
                message=(
                    f"BookItem {book_item.id!r} references unknown "
                    f"Counterparty {book_item.counterparty_id!r}"
                ),
                artifact_ids=(
                    book_item.id,
                    book_item.counterparty_id,
                ),
            )


# ----------------------------------------------------------------------
# Evidence artifacts
# ----------------------------------------------------------------------


def _validate_evidence_artifacts(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    assertions = state.book_item_evidence_assertions

    for assertion in assertions.values():
        if state.get_book_item(assertion.book_item_id) is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_EVIDENCE_ASSERTION_TARGET,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} targets "
                    f"unknown BookItem {assertion.book_item_id!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    assertion.book_item_id,
                ),
            )

        if (
            assertion.evidence_type == BookItemEvidenceType.COUNTERPARTY
            and assertion.value is not None
            and state.get_counterparty(assertion.value) is None
        ):
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_COUNTERPARTY,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} references "
                    f"unknown Counterparty {assertion.value!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    assertion.value,
                ),
            )

        if (
            assertion.evidence_type == BookItemEvidenceType.DOCUMENT_LINK
            and assertion.value is not None
            and state.get_document(assertion.value) is None
        ):
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_DOCUMENT,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} references "
                    f"unknown Document {assertion.value!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    assertion.value,
                ),
            )

        _validate_document_refs(
            state,
            owner_id=assertion.id,
            document_ids=assertion.document_ids,
            issues=issues,
        )

        superseded_id = assertion.supersedes_assertion_id
        if superseded_id is None:
            continue

        superseded = assertions.get(superseded_id)
        if superseded is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_EVIDENCE_SUPERSESSION,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} supersedes "
                    f"unknown BookItemEvidenceAssertion {superseded_id!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    superseded_id,
                ),
            )
            continue

        if superseded.book_item_id != assertion.book_item_id:
            _append_issue(
                issues,
                code=ValidationCode.EVIDENCE_SUPERSESSION_SUBJECT_MISMATCH,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} for "
                    f"BookItem {assertion.book_item_id!r} cannot "
                    f"supersede BookItemEvidenceAssertion {superseded.id!r} "
                    f"for BookItem {superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    superseded.id,
                ),
            )

        if superseded.evidence_type != assertion.evidence_type:
            _append_issue(
                issues,
                code=ValidationCode.EVIDENCE_SUPERSESSION_TYPE_MISMATCH,
                message=(
                    f"BookItemEvidenceAssertion {assertion.id!r} of type "
                    f"{assertion.evidence_type.value!r} cannot "
                    f"supersede BookItemEvidenceAssertion {superseded.id!r} "
                    f"of type {superseded.evidence_type.value!r}"
                ),
                artifact_ids=(
                    assertion.id,
                    superseded.id,
                ),
            )

    for invalidation in state.book_item_evidence_invalidations.values():
        if (
            state.get_book_item_evidence_assertion(
                invalidation.assertion_id
            )
            is None
        ):
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_EVIDENCE_INVALIDATION_TARGET,
                message=(
                    f"BookItemEvidenceInvalidation "
                    f"{invalidation.id!r} references unknown "
                    f"BookItemEvidenceAssertion "
                    f"{invalidation.assertion_id!r}"
                ),
                artifact_ids=(
                    invalidation.id,
                    invalidation.assertion_id,
                ),
            )


# ----------------------------------------------------------------------
# Routing artifacts
# ----------------------------------------------------------------------


def _validate_routing_artifacts(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    decisions = state.routing_decisions

    for decision in decisions.values():
        if state.get_book_item(decision.book_item_id) is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_BOOK_ITEM,
                message=(
                    f"RoutingDecision {decision.id!r} references "
                    f"unknown BookItem {decision.book_item_id!r}"
                ),
                artifact_ids=(
                    decision.id,
                    decision.book_item_id,
                ),
            )

        if state.get_bank_account(decision.bank_account_id) is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_BANK_ACCOUNT,
                message=(
                    f"RoutingDecision {decision.id!r} references "
                    f"unknown BankAccount {decision.bank_account_id!r}"
                ),
                artifact_ids=(
                    decision.id,
                    decision.bank_account_id,
                ),
            )

        superseded_id = decision.supersedes_routing_decision_id

        if superseded_id is None:
            continue

        superseded = decisions.get(superseded_id)

        if superseded is None:
            _append_issue(
                issues,
                code=(
                    ValidationCode.UNKNOWN_ROUTING_SUPERSESSION
                ),
                message=(
                    f"RoutingDecision {decision.id!r} supersedes "
                    f"unknown RoutingDecision {superseded_id!r}"
                ),
                artifact_ids=(
                    decision.id,
                    superseded_id,
                ),
            )
            continue

        if superseded.book_item_id != decision.book_item_id:
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .ROUTING_SUPERSESSION_SUBJECT_MISMATCH
                ),
                message=(
                    f"RoutingDecision {decision.id!r} for "
                    f"BookItem {decision.book_item_id!r} cannot "
                    f"supersede RoutingDecision {superseded.id!r} "
                    f"for BookItem {superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    decision.id,
                    superseded.id,
                ),
            )

    for invalidation in state.routing_invalidations.values():
        if (
            state.get_routing_decision(
                invalidation.routing_decision_id
            )
            is None
        ):
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .UNKNOWN_ROUTING_INVALIDATION_TARGET
                ),
                message=(
                    f"RoutingDecisionInvalidation "
                    f"{invalidation.id!r} references unknown "
                    f"RoutingDecision "
                    f"{invalidation.routing_decision_id!r}"
                ),
                artifact_ids=(
                    invalidation.id,
                    invalidation.routing_decision_id,
                ),
            )


# ----------------------------------------------------------------------
# Classification artifacts
# ----------------------------------------------------------------------


def _validate_classification_artifacts(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    classifications = state.classifications

    for classification in classifications.values():
        if state.get_book_item(classification.book_item_id) is None:
            _append_issue(
                issues,
                code=ValidationCode.UNKNOWN_BOOK_ITEM,
                message=(
                    f"ClassificationDecision "
                    f"{classification.id!r} references unknown "
                    f"BookItem {classification.book_item_id!r}"
                ),
                artifact_ids=(
                    classification.id,
                    classification.book_item_id,
                ),
            )

        _validate_document_refs(
            state,
            owner_id=classification.id,
            document_ids=classification.evidence_refs,
            issues=issues,
        )

        superseded_id = (
            classification.supersedes_classification_id
        )

        if superseded_id is None:
            continue

        superseded = classifications.get(
            superseded_id
        )

        if superseded is None:
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .UNKNOWN_CLASSIFICATION_SUPERSESSION
                ),
                message=(
                    f"ClassificationDecision "
                    f"{classification.id!r} supersedes unknown "
                    f"ClassificationDecision {superseded_id!r}"
                ),
                artifact_ids=(
                    classification.id,
                    superseded_id,
                ),
            )
            continue

        if superseded.book_item_id != classification.book_item_id:
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .CLASSIFICATION_SUPERSESSION_SUBJECT_MISMATCH
                ),
                message=(
                    f"ClassificationDecision "
                    f"{classification.id!r} for BookItem "
                    f"{classification.book_item_id!r} cannot "
                    f"supersede ClassificationDecision "
                    f"{superseded.id!r} for BookItem "
                    f"{superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    classification.id,
                    superseded.id,
                ),
            )

    for invalidation in (
        state.classification_invalidations.values()
    ):
        if (
            state.get_classification(
                invalidation.classification_id
            )
            is None
        ):
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .UNKNOWN_CLASSIFICATION_INVALIDATION_TARGET
                ),
                message=(
                    f"ClassificationInvalidation "
                    f"{invalidation.id!r} references unknown "
                    f"ClassificationDecision "
                    f"{invalidation.classification_id!r}"
                ),
                artifact_ids=(
                    invalidation.id,
                    invalidation.classification_id,
                ),
            )


# ----------------------------------------------------------------------
# Reconciliation artifacts
# ----------------------------------------------------------------------


def _validate_reconciliation_artifacts(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> None:
    for reconciliation in state.reconciliations.values():
        referenced_bank_items = []
        referenced_book_items = []

        missing_reference = False

        for allocation in reconciliation.bank_allocations:
            bank_item = state.get_bank_item(
                allocation.bank_item_id
            )

            if bank_item is None:
                missing_reference = True

                _append_issue(
                    issues,
                    code=ValidationCode.UNKNOWN_BANK_ITEM,
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"references unknown BankItem "
                        f"{allocation.bank_item_id!r}"
                    ),
                    artifact_ids=(
                        reconciliation.id,
                        allocation.bank_item_id,
                    ),
                )
                continue

            referenced_bank_items.append(
                (
                    allocation,
                    bank_item,
                )
            )

        for allocation in reconciliation.book_allocations:
            book_item = state.get_book_item(
                allocation.book_item_id
            )

            if book_item is None:
                missing_reference = True

                _append_issue(
                    issues,
                    code=ValidationCode.UNKNOWN_BOOK_ITEM,
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"references unknown BookItem "
                        f"{allocation.book_item_id!r}"
                    ),
                    artifact_ids=(
                        reconciliation.id,
                        allocation.book_item_id,
                    ),
                )
                continue

            referenced_book_items.append(
                (
                    allocation,
                    book_item,
                )
            )

        _validate_document_refs(
            state,
            owner_id=reconciliation.id,
            document_ids=reconciliation.evidence_refs,
            issues=issues,
        )

        prov_fields = (
            reconciliation.source_hypothesis_id,
            reconciliation.source_hypothesis_state_revision,
            reconciliation.source_hypothesis_utility,
            reconciliation.source_hypothesis_generated_at,
        )
        prov_count = sum(1 for v in prov_fields if v is not None)
        if prov_count not in (0, 4):
            _append_issue(
                issues,
                code=ValidationCode.INVALID_HYPOTHESIS_PROVENANCE,
                message=(
                    f"Reconciliation {reconciliation.id!r} has partial "
                    "hypothesis provenance fields"
                ),
                artifact_ids=(reconciliation.id,),
            )
        elif prov_count == 4:
            if (
                reconciliation.source_hypothesis_state_revision is not None
                and reconciliation.source_hypothesis_state_revision + 1
                != reconciliation.state_revision_at_creation
            ):
                _append_issue(
                    issues,
                    code=ValidationCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"source_hypothesis_state_revision + 1 "
                        f"({reconciliation.source_hypothesis_state_revision + 1}) "
                        f"does not match state_revision_at_creation "
                        f"({reconciliation.state_revision_at_creation})"
                    ),
                    artifact_ids=(reconciliation.id,),
                )

        if (
            reconciliation.source_hypothesis_admissibility is not None
            and reconciliation.source_hypothesis_admissibility != SemanticAdmissibility.SUPPORTED
        ):
            _append_issue(
                issues,
                code=ValidationCode.INVALID_HYPOTHESIS_PROVENANCE,
                message=(
                    f"Reconciliation {reconciliation.id!r} has invalid "
                    f"source_hypothesis_admissibility {reconciliation.source_hypothesis_admissibility.value!r}; "
                    "accepted reconciliations can only originate from SUPPORTED hypotheses"
                ),
                artifact_ids=(reconciliation.id,),
            )

        if (
            reconciliation.source_hypothesis_allocation_support is not None
            and reconciliation.source_hypothesis_allocation_support
            in (
                AllocationSupport.INSUFFICIENT_EVIDENCE,
                AllocationSupport.CONTRADICTED,
            )
        ):
            _append_issue(
                issues,
                code=ValidationCode.INVALID_HYPOTHESIS_PROVENANCE,
                message=(
                    f"Reconciliation {reconciliation.id!r} has invalid "
                    f"source_hypothesis_allocation_support {reconciliation.source_hypothesis_allocation_support.value!r}; "
                    "accepted reconciliations can only originate from EXPLICIT_EVIDENCE or UNIQUE_INFERENCE hypotheses"
                ),
                artifact_ids=(reconciliation.id,),
            )

        if missing_reference:
            continue

        _validate_reconciliation_math(
            state,
            reconciliation,
            referenced_bank_items,
            referenced_book_items,
            issues,
        )

    for invalidation in (
        state.reconciliation_invalidations.values()
    ):
        if (
            state.get_reconciliation(
                invalidation.reconciliation_id
            )
            is None
        ):
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .UNKNOWN_RECONCILIATION_INVALIDATION_TARGET
                ),
                message=(
                    f"ReconciliationInvalidation "
                    f"{invalidation.id!r} references unknown "
                    f"Reconciliation "
                    f"{invalidation.reconciliation_id!r}"
                ),
                artifact_ids=(
                    invalidation.id,
                    invalidation.reconciliation_id,
                ),
            )


def _validate_reconciliation_math(
    state: BookkeepingState,
    reconciliation,
    referenced_bank_items,
    referenced_book_items,
    issues: list[ValidationIssue],
) -> None:
    bank_sum = sum(
        allocation.amount_int
        for allocation, _ in referenced_bank_items
    )

    book_sum = sum(
        allocation.amount_int
        for allocation, _ in referenced_book_items
    )

    if bank_sum != book_sum:
        _append_issue(
            issues,
            code=(
                ValidationCode
                .RECONCILIATION_MONETARY_IMBALANCE
            ),
            message=(
                f"Reconciliation {reconciliation.id!r} allocates "
                f"{bank_sum} bank units but {book_sum} book units"
            ),
            artifact_ids=(reconciliation.id,),
        )

    if state.context.policy.require_exact_currency_match:
        currencies = {
            item.currency
            for _, item in referenced_bank_items
        } | {
            item.currency
            for _, item in referenced_book_items
        }

        if len(currencies) > 1:
            _append_issue(
                issues,
                code=(
                    ValidationCode
                    .RECONCILIATION_CURRENCY_MISMATCH
                ),
                message=(
                    f"Reconciliation {reconciliation.id!r} mixes "
                    f"currencies {sorted(currencies)!r}"
                ),
                artifact_ids=(reconciliation.id,),
            )

    for _, bank_item in referenced_bank_items:
        for _, book_item in referenced_book_items:
            direction_pair = (
                bank_item.direction,
                book_item.direction,
            )

            if direction_pair not in DIRECTION_COMPATIBILITY_MAP:
                _append_issue(
                    issues,
                    code=(
                        ValidationCode
                        .RECONCILIATION_DIRECTION_INCOMPATIBILITY
                    ),
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"contains incompatible directions "
                        f"{bank_item.direction.value!r} and "
                        f"{book_item.direction.value!r}"
                    ),
                    artifact_ids=(
                        reconciliation.id,
                        bank_item.id,
                        book_item.id,
                    ),
                )

    if not state.context.policy.allow_partial_bank_reconciliation:
        for allocation, bank_item in referenced_bank_items:
            if allocation.amount_int != bank_item.amount_int:
                _append_issue(
                    issues,
                    code=ValidationCode.PARTIAL_BANK_CONSUMPTION,
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"allocates {allocation.amount_int} units "
                        f"from BankItem {bank_item.id!r}, whose full "
                        f"amount is {bank_item.amount_int}"
                    ),
                    artifact_ids=(
                        reconciliation.id,
                        bank_item.id,
                    ),
                )

    if not state.context.policy.allow_partial_book_reconciliation:
        for allocation, book_item in referenced_book_items:
            if allocation.amount_int != book_item.amount_int:
                _append_issue(
                    issues,
                    code=ValidationCode.PARTIAL_BOOK_CONSUMPTION,
                    message=(
                        f"Reconciliation {reconciliation.id!r} "
                        f"allocates {allocation.amount_int} units "
                        f"from BookItem {book_item.id!r}, whose full "
                        f"amount is {book_item.amount_int}"
                    ),
                    artifact_ids=(
                        reconciliation.id,
                        book_item.id,
                    ),
                )


# ----------------------------------------------------------------------
# Derived-state consistency
# ----------------------------------------------------------------------


def _try_build_derived_state(
    state: BookkeepingState,
    issues: list[ValidationIssue],
) -> BookkeepingDerivedState | None:
    try:
        return build_derived_state(state)

    except DerivedStateError as exc:
        _append_issue(
            issues,
            code=ValidationCode.MULTIPLE_ACTIVE_DECISIONS,
            message=str(exc),
        )

        return None


def _validate_derived_capacities(
    derived: BookkeepingDerivedState,
    issues: list[ValidationIssue],
) -> None:
    for bank_item_id in (
        derived.overallocated_bank_item_ids
    ):
        _append_issue(
            issues,
            code=ValidationCode.BANK_CAPACITY_OVERFLOW,
            message=(
                f"BankItem {bank_item_id!r} has active "
                "reconciliation allocations exceeding its "
                "authoritative amount"
            ),
            artifact_ids=(bank_item_id,),
        )

    for book_item_id in (
        derived.overallocated_book_item_ids
    ):
        _append_issue(
            issues,
            code=ValidationCode.BOOK_CAPACITY_OVERFLOW,
            message=(
                f"BookItem {book_item_id!r} has active "
                "reconciliation allocations exceeding its "
                "authoritative amount"
            ),
            artifact_ids=(book_item_id,),
        )


# ----------------------------------------------------------------------
# Generic helpers
# ----------------------------------------------------------------------


def _validate_document_refs(
    state: BookkeepingState,
    *,
    owner_id: str,
    document_ids: tuple[str, ...],
    issues: list[ValidationIssue],
) -> None:
    for document_id in document_ids:
        if state.get_document(document_id) is not None:
            continue

        _append_issue(
            issues,
            code=ValidationCode.UNKNOWN_DOCUMENT,
            message=(
                f"Artifact {owner_id!r} references unknown "
                f"Document {document_id!r}"
            ),
            artifact_ids=(
                owner_id,
                document_id,
            ),
        )


def _append_issue(
    issues: list[ValidationIssue],
    *,
    code: ValidationCode,
    message: str,
    artifact_ids: tuple[str, ...] = (),
    severity: ValidationSeverity = ValidationSeverity.ERROR,
) -> None:
    issues.append(
        ValidationIssue(
            code=code,
            severity=severity,
            message=message,
            artifact_ids=artifact_ids,
        )
    )


def _issue_sort_key(
    issue: ValidationIssue,
) -> tuple[str, str, tuple[str, ...], str]:
    return (
        issue.severity.value,
        issue.code.value,
        issue.artifact_ids,
        issue.message,
    )
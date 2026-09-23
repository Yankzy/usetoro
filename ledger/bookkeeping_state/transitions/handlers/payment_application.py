from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    verify_stage1_capability,
)
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.money import amount_units_to_int
from bookkeeping_state.persistence.repository import (
    ApplyPaymentInstruction,
    PaymentApplicationObligationAllocationInstruction,
    PersistenceWriteSet,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionRejection,
)

PaymentApplicationHandlerResult: TypeAlias = (
    PersistenceWriteSet | TransitionRejection
)


def handle_apply_payment_command(
    state: BookkeepingState,
    command: ApplyPaymentCommand,
) -> PaymentApplicationHandlerResult:
    """
    Validate an ApplyPaymentCommand against the live BookkeepingState
    and mint the corresponding ApplyPaymentInstruction inside a PersistenceWriteSet.
    """
    # 1. Check session
    if command.session_id != state.session_id:
        return TransitionRejection(
            code=RejectionCode.SESSION_MISMATCH,
            message=(
                f"Command session_id '{command.session_id}' does not match "
                f"state session_id '{state.session_id}'."
            ),
        )

    # 2. Check expected state revision
    if command.expected_state_revision != state.revision:
        return TransitionRejection(
            code=RejectionCode.STATE_REVISION_CONFLICT,
            message=(
                f"Command expected state revision {command.expected_state_revision}, "
                f"but current state revision is {state.revision}."
            ),
        )

    # 3. Check Stage-1 capability HMAC and fields
    current_fp = state_fingerprint(state)
    if not verify_stage1_capability(
        command.capability,
        expected_bank_item_id=command.bank_item_id,
        expected_bank_account_id=command.bank_account_id,
        expected_direction=command.direction,
        expected_currency=command.currency,
        expected_total_amount_units=command.total_amount_units,
        expected_payment_date=command.payment_date,
        expected_allocations=command.allocations,
        expected_session_id=command.session_id,
        expected_state_revision=command.expected_state_revision,
        expected_state_fingerprint=current_fp,
    ):
        return TransitionRejection(
            code=RejectionCode.INVALID_STAGE1_CAPABILITY,
            message=(
                f"Stage-1 execution capability is invalid, forged, or stale "
                f"for bank item '{command.bank_item_id}'."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    # 4. Check bank item existence and match
    bank_item = state.bank_items.get(command.bank_item_id)
    if bank_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ITEM,
            message=f"Bank item '{command.bank_item_id}' not found.",
            artifact_ids=(command.bank_item_id,),
        )

    if bank_item.bank_account_id != command.bank_account_id:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ACCOUNT,
            message=(
                f"Command bank_account_id '{command.bank_account_id}' does not match "
                f"bank item bank_account_id '{bank_item.bank_account_id}'."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if bank_item.direction != command.direction:
        return TransitionRejection(
            code=RejectionCode.DIRECTION_MISMATCH,
            message=(
                f"Command direction '{command.direction}' does not match "
                f"bank item direction '{bank_item.direction}'."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if bank_item.date != command.payment_date:
        return TransitionRejection(
            code=RejectionCode.INVALID_STAGE1_CAPABILITY,
            message=(
                f"Command payment_date '{command.payment_date}' does not match "
                f"bank item date '{bank_item.date}'."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    # 5. Check active Stage-2 reconciliation capacity
    queries = BookkeepingQueries(state)
    st2_allocated = queries.bank_allocated_units(command.bank_item_id)
    if st2_allocated > 0:
        return TransitionRejection(
            code=RejectionCode.ACTIVE_STAGE2_ALLOCATION_EXISTS,
            message=(
                f"Bank item '{command.bank_item_id}' already has active "
                f"Stage-2 allocations ({st2_allocated} units)."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    # 6. Check if bank item has already been executed in payment applications
    if any(
        pa.bank_item_id == command.bank_item_id
        for pa in state.executed_payment_applications.values()
    ):
        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"Bank item '{command.bank_item_id}' has already been executed "
                "in an accepted payment application."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    # 7. Check if payment_application_id already exists
    if command.payment_application_id in state.executed_payment_applications:
        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"Payment application ID '{command.payment_application_id}' "
                "already exists."
            ),
            artifact_ids=(command.payment_application_id,),
        )

    # 8. Check amount and currency
    bank_units = amount_units_to_int(bank_item.amount_units)
    if bank_units != command.total_amount_units:
        return TransitionRejection(
            code=RejectionCode.MONETARY_IMBALANCE,
            message=(
                f"Command total amount units ({command.total_amount_units}) does not "
                f"match bank item amount units ({bank_units})."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if bank_item.currency != command.currency:
        return TransitionRejection(
            code=RejectionCode.CURRENCY_MISMATCH,
            message=(
                f"Command currency '{command.currency}' does not match "
                f"bank item currency '{bank_item.currency}'."
            ),
            artifact_ids=(command.bank_item_id,),
        )

    alloc_sum = sum(alloc.amount_units for alloc in command.allocations)
    if alloc_sum != command.total_amount_units:
        return TransitionRejection(
            code=RejectionCode.MONETARY_IMBALANCE,
            message=(
                f"Sum of allocations ({alloc_sum}) does not match "
                f"total amount units ({command.total_amount_units})."
            ),
            artifact_ids=(command.payment_application_id,),
        )

    # 9. Validate each obligation allocation
    seen_obligations: set[str] = set()
    for alloc in command.allocations:
        if alloc.book_item_id in seen_obligations:
            return TransitionRejection(
                code=RejectionCode.INVALID_ALLOCATION,
                message=(
                    f"Duplicate allocation to obligation '{alloc.book_item_id}' "
                    "in single payment application."
                ),
                artifact_ids=(alloc.book_item_id,),
            )
        seen_obligations.add(alloc.book_item_id)

        book_item = state.book_items.get(alloc.book_item_id)
        if book_item is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_BOOK_ITEM,
                message=f"Obligation book item '{alloc.book_item_id}' not found.",
                artifact_ids=(alloc.book_item_id,),
            )

        if book_item.source_type != SourceType.STAGING_BOOK_ITEM:
            return TransitionRejection(
                code=RejectionCode.INVALID_BOOK_ITEM_SOURCE_TYPE,
                message=(
                    f"Obligation book item '{alloc.book_item_id}' has source_type "
                    f"'{book_item.source_type}', expected 'STAGING_BOOK_ITEM'."
                ),
                artifact_ids=(alloc.book_item_id,),
            )

        if book_item.currency != command.currency:
            return TransitionRejection(
                code=RejectionCode.CURRENCY_MISMATCH,
                message=(
                    f"Obligation currency '{book_item.currency}' does not match "
                    f"payment currency '{command.currency}'."
                ),
                artifact_ids=(alloc.book_item_id,),
            )

        # Direction compatibility:
        # Inflow pays Receivable (BOOK_BANK_DEBIT), Outflow pays Payable (BOOK_BANK_CREDIT)
        if (
            bank_item.direction == Direction.BANK_INFLOW
            and book_item.direction != Direction.BOOK_BANK_DEBIT
        ):
            return TransitionRejection(
                code=RejectionCode.DIRECTION_MISMATCH,
                message=(
                    f"Bank inflow on '{command.bank_item_id}' cannot be applied to "
                    f"payable obligation '{alloc.book_item_id}' (direction: {book_item.direction})."
                ),
                artifact_ids=(alloc.book_item_id,),
            )
        if (
            bank_item.direction == Direction.BANK_OUTFLOW
            and book_item.direction != Direction.BOOK_BANK_CREDIT
        ):
            return TransitionRejection(
                code=RejectionCode.DIRECTION_MISMATCH,
                message=(
                    f"Bank outflow on '{command.bank_item_id}' cannot be applied to "
                    f"receivable obligation '{alloc.book_item_id}' (direction: {book_item.direction})."
                ),
                artifact_ids=(alloc.book_item_id,),
            )

        # Capacity check
        remaining_units = queries.book_remaining_units(alloc.book_item_id)
        if alloc.amount_units > remaining_units:
            return TransitionRejection(
                code=RejectionCode.CAPACITY_EXCEEDED,
                message=(
                    f"Allocation amount {alloc.amount_units} exceeds remaining "
                    f"capacity {remaining_units} for '{alloc.book_item_id}'."
                ),
                artifact_ids=(alloc.book_item_id,),
            )

    # 10. Success: prepare PersistenceWriteSet
    instruction = ApplyPaymentInstruction(
        payment_application_id=command.payment_application_id,
        bank_item_id=command.bank_item_id,
        total_amount_units=str(command.total_amount_units),
        payment_date=command.payment_date,
        allocations=tuple(
            PaymentApplicationObligationAllocationInstruction(
                book_item_id=alloc.book_item_id,
                amount_units=str(alloc.amount_units),
            )
            for alloc in command.allocations
        ),
        session_id=command.session_id,
    )
    return PersistenceWriteSet(
        payment_applications_to_execute=(instruction,),
    )

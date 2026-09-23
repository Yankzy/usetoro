from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state.domain.commands import (
    InvalidateResidualBankClassificationCommand,
    PostResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import amount_units_to_int
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationInvalidation,
    ResidualBankClassificationStatus,
)
from bookkeeping_state.persistence.repository import (
    PersistenceWriteSet,
    PostResidualBankClassificationInstruction,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionRejection,
)

ResidualBankClassificationCommand: TypeAlias = (
    RecordResidualBankClassificationCommand
    | InvalidateResidualBankClassificationCommand
    | PostResidualBankClassificationCommand
)

ResidualBankClassificationHandlerResult: TypeAlias = (
    PersistenceWriteSet | TransitionRejection
)


def _canonical_direction(d: Direction | str) -> str:
    val = str(d).strip().upper()
    if val in ("BANK_OUTFLOW", "OUTFLOW"):
        return "OUTFLOW"
    if val in ("BANK_INFLOW", "INFLOW"):
        return "INFLOW"
    return val


def handle_residual_bank_classification_command(
    state: BookkeepingState,
    command: ResidualBankClassificationCommand,
) -> ResidualBankClassificationHandlerResult:
    """
    Validate a residual bank classification command and prepare immutable durable artifacts.
    """
    if isinstance(command, RecordResidualBankClassificationCommand):
        return _handle_record_residual_bank_classification(state, command)

    if isinstance(command, InvalidateResidualBankClassificationCommand):
        return _handle_invalidate_residual_bank_classification(state, command)

    if isinstance(command, PostResidualBankClassificationCommand):
        return _handle_post_residual_bank_classification(state, command)

    return TransitionRejection(
        code=RejectionCode.UNSUPPORTED_COMMAND,
        message=(
            "Residual bank classification handler received unsupported command "
            f"{type(command).__name__!r}"
        ),
        artifact_ids=(command.command_id,),
    )


def _handle_record_residual_bank_classification(
    state: BookkeepingState,
    command: RecordResidualBankClassificationCommand,
) -> ResidualBankClassificationHandlerResult:
    # 1. Persistence Revision Check
    if command.expected_persistence_revision != state.persistence_revision:
        return TransitionRejection(
            code=RejectionCode.PERSISTENCE_REVISION_CONFLICT,
            message=(
                f"Command expected persistence revision {command.expected_persistence_revision}, "
                f"but state persistence revision is {state.persistence_revision}"
            ),
            artifact_ids=(command.command_id,),
        )

    # 2. BankItem existence
    bank_item = state.get_bank_item(command.bank_item_id)
    if bank_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ITEM,
            message=f"BankItem {command.bank_item_id!r} not found",
            artifact_ids=(command.bank_item_id,),
        )

    # 3. Bank Account check
    bank_account = state.get_bank_account(command.bank_account_id)
    if bank_account is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ACCOUNT,
            message=f"BankAccount {command.bank_account_id!r} not found",
            artifact_ids=(command.bank_account_id,),
        )

    if bank_item.bank_account_id != command.bank_account_id:
        return TransitionRejection(
            code=RejectionCode.BANK_ACCOUNT_MISMATCH,
            message=(
                f"BankItem bank_account_id {bank_item.bank_account_id!r} does not "
                f"match command bank_account_id {command.bank_account_id!r}"
            ),
            artifact_ids=(command.bank_item_id, command.bank_account_id),
        )

    # 4. Economic snapshot check
    if command.original_amount_units != amount_units_to_int(bank_item.amount_units):
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Command original_amount_units {command.original_amount_units} does "
                f"not match BankItem amount_units {bank_item.amount_units}"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if _canonical_direction(command.direction) != _canonical_direction(bank_item.direction):
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Command direction {command.direction} does not "
                f"match BankItem direction {bank_item.direction}"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if command.currency.upper() != bank_item.currency.upper():
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Command currency {command.currency} does not "
                f"match BankItem currency {bank_item.currency}"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if command.residual_amount_units <= 0:
        return TransitionRejection(
            code=RejectionCode.NOT_RESIDUAL_BANK_ITEM,
            message="Residual amount units must be positive",
            artifact_ids=(command.bank_item_id,),
        )

    # 5. Check if bank item remains residual in state
    queries = BookkeepingQueries(state)

    if command.supersedes_decision_id is not None:
        if queries.is_residual_bank_classification_posted(command.supersedes_decision_id):
            return TransitionRejection(
                code=RejectionCode.CANNOT_SUPERSEDE_POSTED_DECISION,
                message=(
                    f"Cannot supersede decision {command.supersedes_decision_id!r}: "
                    "posted decisions are permanently sealed"
                ),
                artifact_ids=(command.supersedes_decision_id,),
            )

    if command.bank_item_id in queries.executed_stage1_bank_item_ids():
        return TransitionRejection(
            code=RejectionCode.NOT_RESIDUAL_BANK_ITEM,
            message=(
                f"BankItem {command.bank_item_id!r} was consumed by an "
                "executed Stage-1 payment application"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    remaining_units = queries.bank_remaining_units(command.bank_item_id)
    if remaining_units <= 0:
        return TransitionRejection(
            code=RejectionCode.NOT_RESIDUAL_BANK_ITEM,
            message=(
                f"BankItem {command.bank_item_id!r} has no remaining units "
                f"({remaining_units}) after Stage 2 reconciliation"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    if command.residual_amount_units != remaining_units:
        return TransitionRejection(
            code=RejectionCode.RESIDUAL_AMOUNT_MISMATCH,
            message=(
                f"Command residual_amount_units ({command.residual_amount_units}) does "
                f"not match current state remaining units ({remaining_units})"
            ),
            artifact_ids=(command.bank_item_id,),
        )

    # 6. Confidence and Account checks
    account_id: str | None = None
    if command.status == ResidualBankClassificationStatus.CLASSIFIED:
        if command.confidence is None or command.confidence < 0.98:
            return TransitionRejection(
                code=RejectionCode.CONFIDENCE_BELOW_THRESHOLD,
                message=(
                    f"Classification confidence {command.confidence} is below "
                    "policy threshold 0.98"
                ),
                artifact_ids=(command.command_id,),
            )

        assert command.account_code is not None

        from ledger.models.accounts import AccountModel

        all_accs = list(AccountModel.objects.filter(code=command.account_code))
        if not all_accs:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_ACCOUNT_CODE,
                message=f"Account code {command.account_code!r} does not exist",
                artifact_ids=(command.command_id,),
            )

        default_coa_id = state.context.policy.chart_of_accounts_id
        coa_accs = [
            a for a in all_accs
            if str(getattr(a, "coa_model_id", "")) == default_coa_id
            or str(getattr(getattr(a, "coa_model", None), "uuid", "")) == default_coa_id
        ]
        if not coa_accs:
            return TransitionRejection(
                code=RejectionCode.ACCOUNT_OUTSIDE_DEFAULT_COA,
                message=(
                    f"Account {command.account_code!r} does not belong to "
                    f"entity default CoA {default_coa_id!r}"
                ),
                artifact_ids=(command.command_id,),
            )

        active_accs = [a for a in coa_accs if a.active]
        if not active_accs:
            return TransitionRejection(
                code=RejectionCode.INACTIVE_ACCOUNT,
                message=f"Account {command.account_code!r} in default CoA is inactive",
                artifact_ids=(command.command_id,),
            )

        account = active_accs[0]
        account_id = str(account.uuid)
    else:
        if command.confidence is not None and (command.confidence < 0.0 or command.confidence > 1.0):
            return TransitionRejection(
                code=RejectionCode.CONFIDENCE_BELOW_THRESHOLD,
                message="Confidence must be between 0.0 and 1.0",
                artifact_ids=(command.command_id,),
            )

    # 7. History, Replay & Supersession checks
    latest = queries.latest_residual_bank_classification(command.bank_item_id)
    active = queries.active_residual_bank_classification(command.bank_item_id)

    if command.supersedes_decision_id is None:
        if latest is not None:
            # Check if this is an exact replay of the currently active decision
            if (
                active is not None
                and active.status == command.status
                and active.account_code == command.account_code
                and active.hold_reason == command.hold_reason
                and active.original_amount_units == command.original_amount_units
                and active.residual_amount_units == command.residual_amount_units
                and _canonical_direction(active.direction) == _canonical_direction(command.direction)
                and active.currency == command.currency
                and active.request_semantic_digest == command.request_semantic_digest
            ):
                return PersistenceWriteSet()

            return TransitionRejection(
                code=RejectionCode.BRANCHING_HISTORY_FORBIDDEN,
                message=(
                    f"BankItem {command.bank_item_id!r} already has classification "
                    f"history (latest: {latest.id!r}); new decision must "
                    f"explicitly supersede the latest tip {latest.id!r}"
                ),
                artifact_ids=(command.bank_item_id, latest.id),
            )
    else:
        if latest is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_RESIDUAL_BANK_CLASSIFICATION,
                message=(
                    f"Cannot supersede {command.supersedes_decision_id!r}: "
                    f"no classification history exists for BankItem {command.bank_item_id!r}"
                ),
                artifact_ids=(command.supersedes_decision_id,),
            )

        if command.supersedes_decision_id != latest.id:
            target_dec = state.get_residual_bank_classification(command.supersedes_decision_id)
            if target_dec is None:
                return TransitionRejection(
                    code=RejectionCode.UNKNOWN_RESIDUAL_BANK_CLASSIFICATION,
                    message=f"Superseded decision {command.supersedes_decision_id!r} does not exist",
                    artifact_ids=(command.supersedes_decision_id,),
                )
            if target_dec.bank_item_id != command.bank_item_id:
                return TransitionRejection(
                    code=RejectionCode.RESIDUAL_BANK_CLASSIFICATION_SUBJECT_MISMATCH,
                    message=(
                        f"Superseded decision {command.supersedes_decision_id!r} belongs "
                        f"to BankItem {target_dec.bank_item_id!r}, not {command.bank_item_id!r}"
                    ),
                    artifact_ids=(command.supersedes_decision_id, command.bank_item_id),
                )
            return TransitionRejection(
                code=RejectionCode.BRANCHING_HISTORY_FORBIDDEN,
                message=(
                    f"Cannot supersede non-tip decision {command.supersedes_decision_id!r}; "
                    f"current latest tip is {latest.id!r}"
                ),
                artifact_ids=(command.supersedes_decision_id, latest.id),
            )

        if queries.is_residual_bank_classification_posted(command.supersedes_decision_id):
            return TransitionRejection(
                code=RejectionCode.CANNOT_SUPERSEDE_POSTED_DECISION,
                message=(
                    f"Cannot supersede decision {command.supersedes_decision_id!r}: "
                    "posted decisions are permanently sealed"
                ),
                artifact_ids=(command.supersedes_decision_id,),
            )

        # Check exact replay of active decision when superseding latest
        if (
            active is not None
            and active.id == latest.id
            and active.status == command.status
            and active.account_code == command.account_code
            and active.hold_reason == command.hold_reason
            and active.original_amount_units == command.original_amount_units
            and active.residual_amount_units == command.residual_amount_units
            and _canonical_direction(active.direction) == _canonical_direction(command.direction)
            and active.currency == command.currency
            and active.request_semantic_digest == command.request_semantic_digest
        ):
            return PersistenceWriteSet()

    # 8. Check duplicate artifact ID
    existing = state.get_residual_bank_classification(command.decision_id)
    if existing is not None:
        if (
            existing.bank_item_id == command.bank_item_id
            and existing.status == command.status
            and existing.account_code == command.account_code
            and existing.hold_reason == command.hold_reason
            and existing.original_amount_units == command.original_amount_units
            and existing.residual_amount_units == command.residual_amount_units
            and _canonical_direction(existing.direction) == _canonical_direction(command.direction)
            and existing.currency == command.currency
            and existing.request_semantic_digest == command.request_semantic_digest
            and existing.supersedes_decision_id == command.supersedes_decision_id
        ):
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"ResidualBankClassificationDecision ID {command.decision_id!r} "
                "already exists with different content"
            ),
            artifact_ids=(command.decision_id,),
        )

    # 9. Build immutable ResidualBankClassificationDecision
    staged_tx_id = (
        command.bank_item_id.split(":", 1)[1]
        if ":" in command.bank_item_id
        else command.bank_item_id
    )

    decision = ResidualBankClassificationDecision(
        id=command.decision_id,
        bank_item_id=command.bank_item_id,
        staged_transaction_id=staged_tx_id,
        bank_account_id=command.bank_account_id,
        status=command.status,
        account_code=command.account_code,
        account_id=account_id,
        original_amount_units=command.original_amount_units,
        residual_amount_units=command.residual_amount_units,
        direction=Direction(_canonical_direction(command.direction)),
        currency=command.currency,
        confidence=command.confidence,
        rationale=command.rationale,
        evidence_refs=command.evidence_refs,
        hold_reason=command.hold_reason,
        required_evidence=command.required_evidence,
        schema_version=command.schema_version,
        dag_id=command.dag_id,
        request_semantic_digest=command.request_semantic_digest,
        session_id=command.session_id,
        state_revision_at_decision=state.revision + 1,
        persistence_revision_at_decision=state.persistence_revision + 1,
        ase_node_id=command.ase_node_id,
        terminal_property=command.terminal_property,
        supersedes_decision_id=command.supersedes_decision_id,
        created_at=command.issued_at,
    )

    return PersistenceWriteSet(residual_bank_classifications=(decision,))


def _handle_invalidate_residual_bank_classification(
    state: BookkeepingState,
    command: InvalidateResidualBankClassificationCommand,
) -> ResidualBankClassificationHandlerResult:
    # 1. Persistence Revision Check
    if command.expected_persistence_revision != state.persistence_revision:
        return TransitionRejection(
            code=RejectionCode.PERSISTENCE_REVISION_CONFLICT,
            message=(
                f"Command expected persistence revision {command.expected_persistence_revision}, "
                f"but state persistence revision is {state.persistence_revision}"
            ),
            artifact_ids=(command.command_id,),
        )

    # 2. Target decision existence
    target = state.get_residual_bank_classification(command.decision_id)
    if target is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_RESIDUAL_BANK_CLASSIFICATION,
            message=(
                f"Cannot invalidate ResidualBankClassificationDecision "
                f"{command.decision_id!r}: artifact does not exist"
            ),
            artifact_ids=(command.decision_id,),
        )

    # 3. Target must be the current latest chain tip
    queries = BookkeepingQueries(state)
    latest = queries.latest_residual_bank_classification(target.bank_item_id)
    if latest is None or latest.id != target.id:
        return TransitionRejection(
            code=RejectionCode.CANNOT_INVALIDATE_NON_TIP,
            message=(
                f"Cannot invalidate decision {target.id!r} because it is not "
                f"the current latest chain tip ({getattr(latest, 'id', None)!r})"
            ),
            artifact_ids=(target.id, getattr(latest, "id", "")),
        )

    # 3b. Target must not be posted
    if queries.is_residual_bank_classification_posted(target.id):
        return TransitionRejection(
            code=RejectionCode.CANNOT_INVALIDATE_POSTED_DECISION,
            message=(
                f"Cannot invalidate decision {target.id!r}: "
                "posted decisions are permanently sealed"
            ),
            artifact_ids=(target.id,),
        )

    # 4. Check if already invalidated
    existing_invalidation = next(
        (
            inv for inv in state.residual_bank_classification_invalidations.values()
            if inv.classification_id == target.id
        ),
        None,
    )

    if existing_invalidation is not None:
        if existing_invalidation.reason == command.reason:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.ALREADY_INVALIDATED,
            message=(
                f"Residual bank classification decision {target.id!r} is "
                f"already invalidated by {existing_invalidation.id!r}"
            ),
            artifact_ids=(target.id, existing_invalidation.id),
        )

    # 5. Duplicate invalidation ID protection
    existing_by_id = state.get_residual_bank_classification_invalidation(command.invalidation_id)
    if existing_by_id is not None:
        if (
            existing_by_id.classification_id == command.decision_id
            and existing_by_id.reason == command.reason
        ):
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"ResidualBankClassificationInvalidation ID {command.invalidation_id!r} "
                "already exists with different content"
            ),
            artifact_ids=(command.invalidation_id, target.id),
        )

    # 6. Build immutable invalidation
    invalidation = ResidualBankClassificationInvalidation(
        id=command.invalidation_id,
        classification_id=command.decision_id,
        reason=command.reason,
        session_id=command.session_id,
        state_revision_at_invalidation=state.revision + 1,
        persistence_revision_at_invalidation=state.persistence_revision + 1,
        created_at=command.issued_at,
    )

    return PersistenceWriteSet(residual_bank_classification_invalidations=(invalidation,))


def _handle_post_residual_bank_classification(
    state: BookkeepingState,
    command: PostResidualBankClassificationCommand,
) -> ResidualBankClassificationHandlerResult:
    # 1. Resolve decision_id against hydrated state
    decision = state.get_residual_bank_classification(command.decision_id)
    if decision is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_RESIDUAL_BANK_CLASSIFICATION,
            message=f"ResidualBankClassificationDecision {command.decision_id!r} not found",
            artifact_ids=(command.decision_id,),
        )

    queries = BookkeepingQueries(state)

    # 2. Idempotent replay check: If durable ResidualBankPosting already exists
    if queries.is_residual_bank_classification_posted(decision.id):
        # Compare command expected-value fields against immutable decision
        conflict_reasons: list[str] = []
        if command.bank_item_id is not None and command.bank_item_id != decision.bank_item_id:
            conflict_reasons.append(
                f"bank_item_id mismatch (command: {command.bank_item_id!r}, decision: {decision.bank_item_id!r})"
            )
        if command.bank_account_id is not None and command.bank_account_id != decision.bank_account_id:
            conflict_reasons.append(
                f"bank_account_id mismatch (command: {command.bank_account_id!r}, decision: {decision.bank_account_id!r})"
            )
        if command.residual_amount_units is not None and command.residual_amount_units != decision.residual_amount_units:
            conflict_reasons.append(
                f"residual_amount_units mismatch (command: {command.residual_amount_units}, decision: {decision.residual_amount_units})"
            )
        if command.direction is not None and _canonical_direction(command.direction) != _canonical_direction(decision.direction):
            conflict_reasons.append(
                f"direction mismatch (command: {command.direction}, decision: {decision.direction})"
            )
        if command.account_code is not None and command.account_code != decision.account_code:
            conflict_reasons.append(
                f"account_code mismatch (command: {command.account_code!r}, decision: {decision.account_code!r})"
            )

        if conflict_reasons:
            return TransitionRejection(
                code=RejectionCode.DECISION_ALREADY_POSTED,
                message=(
                    f"Decision {decision.id!r} is already posted to the general ledger, "
                    f"and command expected values conflict with durable truth: {'; '.join(conflict_reasons)}"
                ),
                artifact_ids=(decision.id,),
            )

        # Exact semantic replay -> return NOOP / idempotent success before OCC rejection
        return PersistenceWriteSet()

    # 3. Only for UNPOSTED decision: enforce expected revisions
    if command.expected_state_revision != state.revision:
        return TransitionRejection(
            code=RejectionCode.STATE_REVISION_CONFLICT,
            message=(
                f"Command {command.command_id!r} was prepared against state revision "
                f"{command.expected_state_revision}, but current revision is {state.revision}"
            ),
            artifact_ids=(command.command_id,),
        )

    if command.expected_persistence_revision != state.persistence_revision:
        return TransitionRejection(
            code=RejectionCode.PERSISTENCE_REVISION_CONFLICT,
            message=(
                f"Command expected persistence revision {command.expected_persistence_revision}, "
                f"but state persistence revision is {state.persistence_revision}"
            ),
            artifact_ids=(command.command_id,),
        )

    # 4. Status must be CLASSIFIED
    if decision.status != ResidualBankClassificationStatus.CLASSIFIED:
        return TransitionRejection(
            code=RejectionCode.CANNOT_POST_NON_CLASSIFIED_DECISION,
            message=(
                f"Cannot post decision {decision.id!r} with status {decision.status!r}: "
                "only CLASSIFIED decisions may be posted to the general ledger"
            ),
            artifact_ids=(decision.id,),
        )

    # 5. Account must be non-null
    if not decision.account_code or not decision.account_id:
        return TransitionRejection(
            code=RejectionCode.CANNOT_POST_NON_CLASSIFIED_DECISION,
            message=f"Decision {decision.id!r} has no target account",
            artifact_ids=(decision.id,),
        )

    # 6. Must be current latest chain tip
    latest = queries.latest_residual_bank_classification(decision.bank_item_id)
    if latest is None or latest.id != decision.id:
        return TransitionRejection(
            code=RejectionCode.CANNOT_POST_NON_TIP_DECISION,
            message=(
                f"Cannot post non-tip decision {decision.id!r}; "
                f"current latest tip is {getattr(latest, 'id', None)!r}"
            ),
            artifact_ids=(decision.id,),
        )

    # 7. Must be active (not invalidated, no successor)
    if queries.is_residual_bank_classification_invalidated(decision.id):
        return TransitionRejection(
            code=RejectionCode.CANNOT_POST_INVALIDATED_DECISION,
            message=f"Cannot post decision {decision.id!r}: decision is invalidated",
            artifact_ids=(decision.id,),
        )

    active = queries.active_residual_bank_classification(decision.bank_item_id)
    if active is None or active.id != decision.id:
        return TransitionRejection(
            code=RejectionCode.CANNOT_POST_SUPERSEDED_DECISION,
            message=f"Decision {decision.id!r} is not active",
            artifact_ids=(decision.id,),
        )

    # 8. Confidence must satisfy frozen 0.98 policy
    if decision.confidence is None or decision.confidence < 0.98:
        return TransitionRejection(
            code=RejectionCode.CONFIDENCE_BELOW_THRESHOLD,
            message=(
                f"Classification confidence {decision.confidence} is below "
                "policy threshold 0.98"
            ),
            artifact_ids=(decision.id,),
        )

    # 9. Account still belongs to active entity.default_coa
    from ledger.models.accounts import AccountModel

    all_accs = list(AccountModel.objects.filter(code=decision.account_code))
    if not all_accs:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_ACCOUNT_CODE,
            message=f"Account code {decision.account_code!r} does not exist",
            artifact_ids=(decision.id,),
        )

    default_coa_id = state.context.policy.chart_of_accounts_id
    coa_accs = [
        a
        for a in all_accs
        if str(getattr(a, "coa_model_id", "")) == default_coa_id
        or str(getattr(getattr(a, "coa_model", None), "uuid", "")) == default_coa_id
    ]
    if not coa_accs:
        return TransitionRejection(
            code=RejectionCode.ACCOUNT_OUTSIDE_DEFAULT_COA,
            message=(
                f"Account {decision.account_code!r} does not belong to "
                f"entity default CoA {default_coa_id!r}"
            ),
            artifact_ids=(decision.id,),
        )

    active_accs = [a for a in coa_accs if a.active]
    if not active_accs:
        return TransitionRejection(
            code=RejectionCode.INACTIVE_ACCOUNT,
            message=f"Account {decision.account_code!r} in default CoA is inactive",
            artifact_ids=(decision.id,),
        )

    # 10. Bank item existence & Bank account checks
    bank_item = state.get_bank_item(decision.bank_item_id)
    if bank_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ITEM,
            message=f"BankItem {decision.bank_item_id!r} not found",
            artifact_ids=(decision.bank_item_id,),
        )

    bank_account = state.get_bank_account(decision.bank_account_id)
    if bank_account is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ACCOUNT,
            message=f"BankAccount {decision.bank_account_id!r} not found",
            artifact_ids=(decision.bank_account_id,),
        )

    if bank_item.bank_account_id != decision.bank_account_id:
        return TransitionRejection(
            code=RejectionCode.BANK_ACCOUNT_MISMATCH,
            message=(
                f"BankItem bank_account_id {bank_item.bank_account_id!r} does not "
                f"match decision bank_account_id {decision.bank_account_id!r}"
            ),
            artifact_ids=(decision.bank_item_id, decision.bank_account_id),
        )

    # 11 & 12 & 13. Residual checks: Stage 1 consumption
    if decision.bank_item_id in queries.executed_stage1_bank_item_ids():
        return TransitionRejection(
            code=RejectionCode.NOT_RESIDUAL_BANK_ITEM,
            message=(
                f"BankItem {decision.bank_item_id!r} was consumed by an "
                "executed Stage-1 payment application"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    # 14. Stage 2 remaining units exactly equal decision.residual_amount_units
    remaining_units = queries.bank_remaining_units(decision.bank_item_id)
    if remaining_units <= 0:
        return TransitionRejection(
            code=RejectionCode.NOT_RESIDUAL_BANK_ITEM,
            message=(
                f"BankItem {decision.bank_item_id!r} has no remaining units "
                f"({remaining_units}) after Stage 2 reconciliation"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    if decision.residual_amount_units != remaining_units:
        return TransitionRejection(
            code=RejectionCode.RESIDUAL_AMOUNT_MISMATCH,
            message=(
                f"Decision residual_amount_units ({decision.residual_amount_units}) does "
                f"not match current state remaining units ({remaining_units})"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    # 15. Original amount / direction / currency match
    if decision.original_amount_units != amount_units_to_int(bank_item.amount_units):
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Decision original_amount_units {decision.original_amount_units} does "
                f"not match BankItem amount_units {bank_item.amount_units}"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    if _canonical_direction(decision.direction) != _canonical_direction(bank_item.direction):
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Decision direction {decision.direction} does not "
                f"match BankItem direction {bank_item.direction}"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    if decision.currency.upper() != bank_item.currency.upper():
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Decision currency {decision.currency} does not "
                f"match BankItem currency {bank_item.currency}"
            ),
            artifact_ids=(decision.bank_item_id,),
        )

    # Validate optional command expected-value fields against decision
    if command.bank_item_id is not None and command.bank_item_id != decision.bank_item_id:
        return TransitionRejection(
            code=RejectionCode.RESIDUAL_BANK_CLASSIFICATION_SUBJECT_MISMATCH,
            message=(
                f"Command bank_item_id {command.bank_item_id!r} does not "
                f"match decision bank_item_id {decision.bank_item_id!r}"
            ),
            artifact_ids=(command.bank_item_id, decision.bank_item_id),
        )

    if command.bank_account_id is not None and command.bank_account_id != decision.bank_account_id:
        return TransitionRejection(
            code=RejectionCode.BANK_ACCOUNT_MISMATCH,
            message=(
                f"Command bank_account_id {command.bank_account_id!r} does not "
                f"match decision bank_account_id {decision.bank_account_id!r}"
            ),
            artifact_ids=(command.bank_account_id, decision.bank_account_id),
        )

    if command.residual_amount_units is not None and command.residual_amount_units != decision.residual_amount_units:
        return TransitionRejection(
            code=RejectionCode.RESIDUAL_AMOUNT_MISMATCH,
            message=(
                f"Command residual_amount_units {command.residual_amount_units} does "
                f"not match decision residual_amount_units {decision.residual_amount_units}"
            ),
            artifact_ids=(command.command_id, decision.id),
        )

    if command.direction is not None and _canonical_direction(command.direction) != _canonical_direction(decision.direction):
        return TransitionRejection(
            code=RejectionCode.ECONOMIC_INPUT_MISMATCH,
            message=(
                f"Command direction {command.direction} does not "
                f"match decision direction {decision.direction}"
            ),
            artifact_ids=(command.command_id,),
        )

    if command.account_code is not None and command.account_code != decision.account_code:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_ACCOUNT_CODE,
            message=(
                f"Command account_code {command.account_code!r} does not "
                f"match decision account_code {decision.account_code!r}"
            ),
            artifact_ids=(command.command_id,),
        )

    # Build external accounting mutation instruction
    instruction = PostResidualBankClassificationInstruction(
        decision_id=command.decision_id,
        command_id=command.command_id,
        session_id=command.session_id,
        state_revision_at_creation=state.revision + 1,
    )
    return PersistenceWriteSet(residual_bank_postings_to_execute=(instruction,))

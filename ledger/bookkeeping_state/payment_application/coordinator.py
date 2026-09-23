from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Sequence

from bookkeeping_state.domain.commands import (
    Stage1ExecutionCapability,
    mint_stage1_capability,
)
from bookkeeping_state.payment_application.models import (
    PaymentApplicationPlan,
)
from bookkeeping_state.payment_application.service import (
    PaymentApplicationService,
)
from bookkeeping_state.reconciliation.models import (
    ReconciliationPlan,
)
from bookkeeping_state.reconciliation.service import (
    ReconciliationService,
)
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries


@dataclass(frozen=True, slots=True)
class TwoStageSettlementAndReconciliationPlan:
    """
    Combined container holding the outcomes of Stage 2 bank reconciliation
    and subsequent Stage 1 payment application planning.
    """

    reconciliation_plan: ReconciliationPlan
    payment_application_plan: PaymentApplicationPlan
    posted_authority_bank_item_ids: tuple[str, ...]
    stage1_capabilities: dict[str, Stage1ExecutionCapability] = field(default_factory=dict)

    def get_capability(self, bank_item_id: str) -> Stage1ExecutionCapability | None:
        return self.stage1_capabilities.get(bank_item_id)


def plan_two_stage_bookkeeping(
    queries: BookkeepingQueries,
    *,
    reconciliation_service: ReconciliationService | None = None,
    payment_application_service: PaymentApplicationService | None = None,
    solver_run_id: str | None = None,
    session_id: str | None = None,
    issued_at: datetime | None = None,
    max_solve_seconds: float = 10.0,
) -> TwoStageSettlementAndReconciliationPlan:
    """
    Neutral production coordinator orchestrating:
    1. Stage 2 (Bank Reconciliation against POSTED_BOOK_ITEM)
    2. Posted-movement authority gate (extracting all bank items with SELECTABLE Stage 2 hypotheses)
    3. Stage 1 (Payment Application against STAGING_BOOK_ITEM for bank items not claimed by posted authority)
    """
    rec_svc = reconciliation_service or ReconciliationService()
    pay_svc = payment_application_service or PaymentApplicationService()

    # 1. Execute Stage 2 bank reconciliation
    try:
        rec_plan = rec_svc.plan(
            queries,
            solver_run_id=solver_run_id,
            session_id=session_id,
            issued_at=issued_at,
            max_solve_seconds=max_solve_seconds,
        )
    except TypeError as exc:
        if "max_solve_seconds" in str(exc):
            rec_plan = rec_svc.plan(
                queries,
                solver_run_id=solver_run_id,
                session_id=session_id,
                issued_at=issued_at,
            )
        else:
            raise

    # 2. Extract posted authority bank item IDs
    posted_authority_ids = rec_plan.posted_authority_bank_item_ids

    # 3. Execute Stage 1 payment application strictly excluding posted authority bank items
    pay_plan = pay_svc.plan(
        queries,
        excluded_bank_item_ids=posted_authority_ids,
        issued_at=issued_at,
        max_solve_seconds=max_solve_seconds,
    )

    # 4. Mint capabilities for Stage 1 proposals bound to exact intent
    current_fp = state_fingerprint(queries._state)
    capabilities = {
        proposal.bank_item_id: mint_stage1_capability(
            intent=proposal.intent,
            session_id=queries.session_id,
            base_state_revision=queries.state_revision,
            base_state_fingerprint=current_fp,
        )
        for proposal in pay_plan.proposals
    }

    return TwoStageSettlementAndReconciliationPlan(
        reconciliation_plan=rec_plan,
        payment_application_plan=pay_plan,
        posted_authority_bank_item_ids=posted_authority_ids,
        stage1_capabilities=capabilities,
    )

from __future__ import annotations

from datetime import datetime
from typing import Sequence

from bookkeeping_state.payment_application.candidate_generation import (
    PaymentApplicationCandidateGenerator,
)
from bookkeeping_state.payment_application.models import (
    PaymentApplicationPlan,
)
from bookkeeping_state.payment_application.optimizer import (
    optimize_payment_application,
)
from bookkeeping_state.payment_application.view import (
    build_payment_application_view,
)
from bookkeeping_state.state.queries import BookkeepingQueries


class PaymentApplicationService:
    """
    Pure read-only planning orchestrator for Stage 1 Payment Application.

    Matches unresolved bank movements / payment evidence to open obligations (STAGING_BOOK_ITEM).
    Does NOT depend on or invoke Stage 2 ReconciliationService internally.
    Any bank items claimed by posted authority are passed in via excluded_bank_item_ids.
    """

    def __init__(
        self,
        *,
        candidate_generator: PaymentApplicationCandidateGenerator | None = None,
    ) -> None:
        self.candidate_generator = candidate_generator or PaymentApplicationCandidateGenerator()

    def plan(
        self,
        queries: BookkeepingQueries,
        *,
        excluded_bank_item_ids: Sequence[str] = (),
        issued_at: datetime | None = None,
        max_solve_seconds: float = 10.0,
    ) -> PaymentApplicationPlan:
        """
        Execute read-only Stage 1 planning against open obligations.

        Returns a PaymentApplicationPlan containing non-conflicting PaymentPostingIntent proposals.
        Does not mutate BookkeepingState or advance revisions.
        """
        view = build_payment_application_view(
            queries,
            excluded_bank_item_ids=excluded_bank_item_ids,
        )

        candidates = self.candidate_generator.generate_candidates(view)

        plan = optimize_payment_application(
            view=view,
            candidates=candidates,
            issued_at=issued_at,
            max_solve_seconds=max_solve_seconds,
        )

        # Record the excluded bank items for complete provenance
        return plan.model_copy(
            update={
                "excluded_by_posted_authority_bank_item_ids": tuple(sorted(set(excluded_bank_item_ids)))
            }
        )

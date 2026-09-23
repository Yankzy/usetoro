from __future__ import annotations

from itertools import combinations
import re
from typing import Sequence

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import AmountUnits
from bookkeeping_state.payment_application.models import (
    ObligationAllocation,
    PaymentApplicationCandidate,
    PaymentApplicationProposal,
    PaymentPostingIntent,
)
from bookkeeping_state.payment_application.view import (
    PaymentApplicationBankItemView,
    PaymentApplicationObligationView,
    PaymentApplicationView,
)

TRANSACTION_WORDS = {
    "payment", "payments", "invoice", "invoices", "bill", "bills",
    "facture", "factures", "reglement", "reglements", "transfer", "transfers",
    "acompte", "acomptes", "solde", "soldes", "virement", "virements",
    "encaissement", "encaissements", "settlement", "settlements", "purchase", "purchases",
    "deposit", "deposits", "receivable", "receivables", "claim", "claims",
    "divers", "multi", "partial", "split", "batch", "order", "unrelated", "receipt", "receipts",
}


def _is_direction_compatible(bank_dir: Direction, oblig_dir: Direction) -> bool:
    if bank_dir in (Direction.BANK_INFLOW, Direction.INFLOW):
        return oblig_dir in (Direction.BOOK_BANK_DEBIT, Direction.INFLOW)
    if bank_dir in (Direction.BANK_OUTFLOW, Direction.OUTFLOW):
        return oblig_dir in (Direction.BOOK_BANK_CREDIT, Direction.OUTFLOW)
    return False


def _extract_tokens(s: str | None) -> set[str]:
    if not s:
        return set()
    return {
        word.lower()
        for word in re.findall(r"[a-zA-Z0-9_\-]+", s)
        if len(word) > 2 and word.lower() not in TRANSACTION_WORDS
    }


def score_payment_pair(
    bank: PaymentApplicationBankItemView,
    oblig: PaymentApplicationObligationView,
) -> tuple[int, list[str]]:
    """
    Score pairwise evidence associating a bank payment with an open obligation.
    """
    score = 0
    reasons: list[str] = []

    # 1. Reference matching
    b_ref = bank.reference.strip().lower() if bank.reference else ""
    o_ref = oblig.reference.strip().lower() if oblig.reference else ""

    ref_matched = False
    if b_ref and o_ref and b_ref == o_ref:
        score += 200
        reasons.append("exact_ref_match:+200")
        ref_matched = True
    elif o_ref and bank.description and re.search(r"\b" + re.escape(o_ref) + r"\b", bank.description.lower()):
        score += 200
        reasons.append("oblig_ref_in_bank_desc:+200")
        ref_matched = True
    elif b_ref and oblig.description and re.search(r"\b" + re.escape(b_ref) + r"\b", oblig.description.lower()):
        score += 200
        reasons.append("bank_ref_in_oblig_desc:+200")
        ref_matched = True

    # 2. Counterparty matching
    b_tokens = _extract_tokens(bank.description)
    o_tokens = _extract_tokens(oblig.counterparty_name) | _extract_tokens(oblig.description)
    shared_tokens = b_tokens & o_tokens
    if shared_tokens:
        score += 100
        reasons.append(f"cp_token_match:{','.join(sorted(shared_tokens))}:+100")

    # 3. Date proximity
    days = abs((bank.date - oblig.date).days)
    if days == 0:
        score += 50
        reasons.append("same_day:+50")
    elif days <= 3:
        score += 25
        reasons.append("within_3d:+25")

    # If no identity evidence matched at all, keep score low
    if not ref_matched and not shared_tokens:
        score = min(score, 25)

    return min(1000, score), reasons


class PaymentApplicationCandidateGenerator:
    """
    Deterministic candidate generation for matching bank movements to open obligations.
    """

    def __init__(
        self,
        *,
        max_grouped_subset_size: int = 4,
        max_candidates_per_bank: int = 25,
    ) -> None:
        self.max_grouped_subset_size = max_grouped_subset_size
        self.max_candidates_per_bank = max_candidates_per_bank

    def generate_candidates(
        self,
        view: PaymentApplicationView,
    ) -> tuple[PaymentApplicationCandidate, ...]:
        candidates: list[PaymentApplicationCandidate] = []
        cand_idx = 1

        for bank in view.bank_items:
            bank_candidates_count = 0

            compatible_obligations = [
                o for o in view.obligations
                if _is_direction_compatible(bank.direction, o.direction)
                and bank.currency == o.currency
                and (o.routed_bank_account_id is None or o.routed_bank_account_id == bank.bank_account_id)
            ]

            # 1. 1:1 Exact Match
            for oblig in compatible_obligations:
                if bank.remaining_amount_int == oblig.remaining_amount_int:
                    candidates.append(
                        PaymentApplicationCandidate(
                            candidate_id=f"pacand-{cand_idx}",
                            bank_item_id=bank.bank_item_id,
                            obligation_allocations=(
                                ObligationAllocation(
                                    book_item_id=oblig.book_item_id,
                                    amount_units=bank.remaining_amount_units,
                                ),
                            ),
                            total_amount_units=bank.remaining_amount_units,
                            rationale=f"1:1 exact settlement: Bank {bank.bank_item_id} -> {oblig.book_item_id}",
                        )
                    )
                    cand_idx += 1
                    bank_candidates_count += 1

            # 2. 1:1 Partial Settlement (Bank < Obligation)
            for oblig in compatible_obligations:
                if bank.remaining_amount_int < oblig.remaining_amount_int:
                    candidates.append(
                        PaymentApplicationCandidate(
                            candidate_id=f"pacand-{cand_idx}",
                            bank_item_id=bank.bank_item_id,
                            obligation_allocations=(
                                ObligationAllocation(
                                    book_item_id=oblig.book_item_id,
                                    amount_units=bank.remaining_amount_units,
                                ),
                            ),
                            total_amount_units=bank.remaining_amount_units,
                            rationale=f"1:1 partial settlement: Bank {bank.bank_item_id} ({bank.remaining_amount_int}) against {oblig.book_item_id} ({oblig.remaining_amount_int})",
                        )
                    )
                    cand_idx += 1
                    bank_candidates_count += 1

            # 3. 1:N Grouped Settlement (One bank movement settles multiple obligations exactly)
            eligible_for_grouped = [
                o for o in compatible_obligations
                if o.remaining_amount_int < bank.remaining_amount_int
            ]

            for r in range(2, min(self.max_grouped_subset_size + 1, len(eligible_for_grouped) + 1)):
                if bank_candidates_count >= self.max_candidates_per_bank:
                    break
                for subset in combinations(eligible_for_grouped, r):
                    if sum(o.remaining_amount_int for o in subset) == bank.remaining_amount_int:
                        allocs = tuple(
                            ObligationAllocation(
                                book_item_id=o.book_item_id,
                                amount_units=o.remaining_amount_units,
                            )
                            for o in subset
                        )
                        candidates.append(
                            PaymentApplicationCandidate(
                                candidate_id=f"pacand-{cand_idx}",
                                bank_item_id=bank.bank_item_id,
                                obligation_allocations=allocs,
                                total_amount_units=bank.remaining_amount_units,
                                rationale=f"1:{r} multi-obligation settlement: Bank {bank.bank_item_id} -> {sorted(o.book_item_id for o in subset)}",
                            )
                        )
                        cand_idx += 1
                        bank_candidates_count += 1
                        if bank_candidates_count >= self.max_candidates_per_bank:
                            break

        return tuple(candidates)

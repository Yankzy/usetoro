from typing import Dict, Any
from domain.patch import ProposedState
from evaluation.ground_truth import GroundTruth
from evaluation.routing_models import RoutingGroundTruth, RoutingMetrics

def calculate_metrics(proposal: ProposedState, truth: GroundTruth) -> Dict[str, Any]:
    # 1. Parse Proposed Matches
    proposed_groups = []
    for m in proposal.matches:
        bank_set = {b.bank_item_id for b in m.bank_allocations}
        book_set = {j.book_item_id for j in m.book_allocations}
        proposed_groups.append((bank_set, book_set))

    # 2. FRR & Recall Evaluation
    correct_selections = 0
    incorrect_selections = 0
    
    for p_banks, p_books in proposed_groups:
        is_correct = any(t.matches(p_banks, p_books) for t in truth.matches)
        if is_correct:
            correct_selections += 1
        else:
            incorrect_selections += 1

    total_proposed = len(proposed_groups)
    total_ground_truth = len(truth.matches)

    # FRR: False Rejection / Reconciliation Rate (Section 51)
    frr = (incorrect_selections / total_proposed) if total_proposed > 0 else 0.0

    # Recall (Section 52)
    recall = (correct_selections / total_ground_truth) if total_ground_truth > 0 else 0.0

    # 3. Hold Accuracy (Section 53)
    proposed_unresolved = set(proposal.unresolved_bank_ids)
    truth_unresolved = truth.unresolved_bank_ids
    
    correct_holds = len(proposed_unresolved.intersection(truth_unresolved))
    total_designed_holds = len(truth_unresolved)
    
    hold_accuracy = (correct_holds / total_designed_holds) if total_designed_holds > 0 else 1.0

    # 4. Global State Accuracy (Section 54)
    # Stricter than per-match accuracy. Requires EXACT match of all vectors.
    global_state_exact = (
        incorrect_selections == 0 and
        correct_selections == total_ground_truth and
        proposed_unresolved == truth_unresolved
    )

    return {
        "FRR": frr,
        "Recall": recall,
        "HoldAccuracy": hold_accuracy,
        "GLOBAL_STATE_EXACT": global_state_exact,
        "proposed_count": total_proposed,
        "correct_count": correct_selections,
        "incorrect_count": incorrect_selections
    }


def calculate_routing_metrics(
    routing_state,
    truth: RoutingGroundTruth,
) -> RoutingMetrics:
    """Calculate routing accuracy, partition leakage, and unroutable precision."""
    actual_assignments = {
        assignment.book_item_id: assignment.assigned_account_id
        for assignment in routing_state.assignments
    }
    total_routable = len(truth.expected_assignments)
    correct_assignments = sum(
        actual_assignments.get(book_id) == expected_account_id
        for book_id, expected_account_id in truth.expected_assignments.items()
    )
    incorrect_assignments = total_routable - correct_assignments

    correct_unroutable = len(
        set(routing_state.unroutable_book_ids).intersection(truth.expected_unroutable)
    )
    total_expected_unroutable = len(truth.expected_unroutable)

    return RoutingMetrics(
        routing_accuracy=(correct_assignments / total_routable) if total_routable else 1.0,
        partition_leakage_rate=(incorrect_assignments / total_routable) if total_routable else 0.0,
        unroutable_precision=(
            correct_unroutable / total_expected_unroutable
            if total_expected_unroutable
            else 1.0
        ),
    )

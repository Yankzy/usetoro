"""
Mathematical Formulation of the Bank Reconciliation Problem using OR-Tools CP-SAT.
"""

from ortools.sat.python import cp_model
from typing import Tuple, Dict, List
from domain.base import Eligibility
from optimizer.protocol import OptimizerRequest

def build_cp_model(
    req: OptimizerRequest
) -> Tuple[cp_model.CpModel, Dict[str, cp_model.IntVar], Dict[str, cp_model.IntVar]]:
    """
    Constructs the global exact reconciliation optimization problem.
    
    Returns:
        model: The instantiated CP-SAT model.
        x_vars: Mapping from hypothesis ID to its boolean decision variable (x_h).
        u_vars: Mapping from bank item ID to its unresolved boolean variable (u_b).
    """
    model = cp_model.CpModel()

    # -------------------------------------------------------------------------
    # 1. Decision Variables (Section 10)
    # -------------------------------------------------------------------------
    x_vars: Dict[str, cp_model.IntVar] = {}
    u_vars: Dict[str, cp_model.IntVar] = {}

    # x_h in {0, 1}: 1 if hypothesis h is selected, 0 otherwise.
    for hyp in req.hypotheses:
        x_vars[hyp.id] = model.NewBoolVar(f"x_{hyp.id}")

    # u_b in {0, 1}: 1 if bank item b remains unresolved, 0 otherwise.
    for bank in req.bank_items:
        u_vars[bank.id] = model.NewBoolVar(f"u_{bank.id}")

    # -------------------------------------------------------------------------
    # 2. Pre-processing lookups for constraint building
    # -------------------------------------------------------------------------
    # Map Bank ID -> List of Hypothesis IDs that consume it
    bank_to_hyps: Dict[str, List[str]] = {b.id: [] for b in req.bank_items}
    # Map Book ID -> List of Tuple(Hypothesis ID, Amount Consumed)
    book_to_hyps: Dict[str, List[Tuple[str, int]]] = {j.id: [] for j in req.book_items}

    for hyp in req.hypotheses:
        for b_alloc in hyp.bank_allocations:
            bank_to_hyps[b_alloc.bank_item_id].append(hyp.id)
        for j_alloc in hyp.book_allocations:
            book_to_hyps[j_alloc.book_item_id].append((hyp.id, j_alloc.amount_int))

    # -------------------------------------------------------------------------
    # 3. Bank Exclusivity Constraint (Section 11)
    # -------------------------------------------------------------------------
    # Each bank line is exactly matched by one hypothesis, or it is unresolved.
    # sum(x_h for all h containing b) + u_b == 1
    for bank in req.bank_items:
        hyp_vars_for_bank = [x_vars[h_id] for h_id in bank_to_hyps[bank.id]]
        model.AddExactlyOne(hyp_vars_for_bank + [u_vars[bank.id]])

    # -------------------------------------------------------------------------
    # 4. Book Capacity Constraint (Section 12)
    # -------------------------------------------------------------------------
    # Book items can participate in multiple hypotheses, but total consumed 
    # amount cannot exceed the remaining capacity.
    # sum(beta_h_j * x_h for all h containing j) <= R_j
    for book in req.book_items:
        capacity = book.remaining_amount_int
        allocations = book_to_hyps[book.id]
        
        # Using linear expressions for the sum
        consumption_expr = sum(
            amount * x_vars[h_id] for h_id, amount in allocations
        )
        model.Add(consumption_expr <= capacity)

    # -------------------------------------------------------------------------
    # 5. Check Proposal / Forced Constraints (Section 22)
    # -------------------------------------------------------------------------
    # If the LLM requests CHECK_PROPOSAL, certain hypotheses are forced to 1.
    for forced_id in req.forced_hypothesis_ids:
        # Constraint: x_h == 1
        model.Add(x_vars[forced_id] == 1)

    # -------------------------------------------------------------------------
    # 6. Optimizer Objective (Section 19)
    # -------------------------------------------------------------------------
    # Lexicographic tiered objective:
    # 1. Maximize total money reconciled (Highest Priority)
    # 2. Maximize total items reconciled (Break ties)
    # 3. Maximize LLM Utility (Semantic tie-breaking)
    
    W_MONEY = 10_000_000 
    W_ITEMS = 1_000_000
    HYPOTHESIS_PENALTY = -10_000
    W_UTILITY = 1
    
    objective_expr = sum(
        ((hyp.total_bank_allocation_int * W_MONEY) + 
         (len(hyp.bank_allocations) * W_ITEMS) + 
         HYPOTHESIS_PENALTY +
         (hyp.utility * W_UTILITY)) * x_vars[hyp.id] 
        for hyp in req.hypotheses 
        if hyp.eligibility == Eligibility.SELECTABLE
    )
    model.Maximize(objective_expr)

    return model, x_vars, u_vars
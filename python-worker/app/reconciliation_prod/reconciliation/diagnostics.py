"""
Rejection Diagnostics and Counterfactual Analysis.

This module provides the logic to mathematically explain *why* the CP-SAT optimizer
rejected a plausible hypothesis proposed by the Semantic Engine.
"""
from typing import List, Set, Dict
from ortools.sat.python import cp_model
from reconciliation_prod.reconciliation.protocol import OptimizerRequest
from reconciliation_prod.reconciliation.model import build_cp_model

def analyze_rejected_hypotheses(
    req: OptimizerRequest, 
    best_objective: int, 
    best_selected_ids: Set[str]
) -> List[Dict]:
    """
    Computes counterfactual diagnostics for rejected hypotheses.
    
    For materially competitive rejected hypotheses, this function forces them to be 
    selected (`x_h == 1`) in a secondary CP-SAT pass to calculate the counterfactual 
    objective delta and mathematically prove the reason for rejection (e.g., lower 
    global objective, or direct constraint violation).
    
    Args:
        req (OptimizerRequest): The original problem request.
        best_objective (int): The maximum objective value of the primary solve.
        best_selected_ids (Set[str]): The IDs of hypotheses in the primary optimal set.
        
    Returns:
        List[Dict]: A list of rejected hypothesis summaries with their counterfactual diagnostics.
    """
    diagnostics = []
    
    for hyp in req.hypotheses:
        if hyp.id in best_selected_ids:
            continue
            
        summary = {
            "hypothesis_id": hyp.id,
            "utility": hyp.utility,
            "bank_allocations": [{"bank_item_id": b.bank_item_id, "amount_units": b.amount_units} for b in hyp.bank_allocations],
            "book_allocations": [{"book_item_id": j.book_item_id, "amount_units": j.amount_units} for j in hyp.book_allocations]
        }
        
        if hyp.eligibility != "SELECTABLE":
            summary["reason"] = "NOT_SELECTABLE"
            diagnostics.append(summary)
            continue
            
        # Counterfactual Solve (Section 32)
        model, x_vars, _ = build_cp_model(req)
        
        # Force this hypothesis to be true
        model.Add(x_vars[hyp.id] == 1)
        
        solver = cp_model.CpSolver()
        solver.parameters.max_time_in_seconds = req.solver_options.max_solve_seconds
        status = solver.Solve(model)
        
        if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
            forced_obj = int(solver.ObjectiveValue())
            delta = best_objective - forced_obj
            summary["reason"] = "LOWER_GLOBAL_OBJECTIVE"
            summary["counterfactual_objective_delta"] = delta
            
            # Find what got displaced
            forced_selected = {h.id for h in req.hypotheses if solver.Value(x_vars[h.id]) == 1}
            displaced = best_selected_ids - forced_selected
            summary["conflicts_with_selected"] = list(displaced)
            
        else:
            # If it's globally infeasible to force this, why? 
            # We can check direct conflicts with forced constraints or capacities.
            if set(req.forced_hypothesis_ids).intersection(best_selected_ids):
                summary["reason"] = "INCOMPATIBLE_WITH_FORCED_HYPOTHESIS"
            else:
                summary["reason"] = "BOOK_CAPACITY_OR_BANK_EXCLUSIVITY_CONFLICT"
                
                
        diagnostics.append(summary)
        
    return diagnostics
"""
Counterfactual analysis and deep rejection diagnostics (Sections 31 & 32).
"""
from typing import List, Set, Dict
from ortools.sat.python import cp_model
from optimizer.protocol import OptimizerRequest
from optimizer.model import build_cp_model

def analyze_rejected_hypotheses(
    req: OptimizerRequest, 
    best_objective: int, 
    best_selected_ids: Set[str]
) -> List[Dict]:
    """
    For materially competitive rejected hypotheses, forces them to be selected 
    to calculate the counterfactual objective delta and determine the rejection reason.
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
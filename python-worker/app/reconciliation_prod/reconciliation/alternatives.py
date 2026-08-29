"""
Alternative Configurations and Solution Stability Analysis.

This module uses No-Good cuts to find alternative global configurations that 
are mathematically competitive with the primary optimal solution.
"""
from typing import List, Tuple, Set, Dict
from ortools.sat.python import cp_model
from reconciliation_prod.reconciliation.protocol import OptimizerRequest
from reconciliation_prod.reconciliation.model import build_cp_model

def find_alternatives_and_stability(
    req: OptimizerRequest, 
    best_objective: int, 
    best_selected_ids: Set[str]
) -> Tuple[List[Dict], str]:
    """
    Finds alternative global configurations within the allowed objective gap.
    
    This function iteratively bans previously found configurations using boolean OR
    constraints (No-Good cuts) and re-solves. It classifies the stability of the 
    problem based on the existence of these alternatives.
    
    Stability Classifications:
    - UNIQUE: No other valid global configurations exist.
    - STABLE: Other configurations exist, but their objective value is significantly lower.
    - AMBIGUOUS: Another configuration exists within a highly competitive objective gap.
    
    Args:
        req (OptimizerRequest): The original problem request.
        best_objective (int): The objective value of the primary optimal solution.
        best_selected_ids (Set[str]): The set of hypothesis IDs chosen in the primary solution.
        
    Returns:
        Tuple[List[Dict], str]: A list of alternative configurations and the stability classification.
    """
    alternatives = []
    gap = req.solver_options.alternative_objective_gap
    max_alts = req.solver_options.max_alternatives
    
    # Track configurations to ban (starting with the primary optimum)
    banned_configs: List[Set[str]] = [best_selected_ids]
    
    for _ in range(max_alts):
        model, x_vars, _ = build_cp_model(req)
        
        # 1. Enforce Objective Gap
        objective_expr = sum(
            hyp.utility * x_vars[hyp.id] 
            for hyp in req.hypotheses if hyp.eligibility == "SELECTABLE"
        )
        model.Add(objective_expr >= best_objective - gap)
        
        # 2. Add No-Good Cuts to ban previously found configurations
        for config in banned_configs:
            # sum(x_h for h NOT in config) + sum((1 - x_h) for h IN config) >= 1
            cut_terms = []
            for h_id, var in x_vars.items():
                if h_id in config:
                    cut_terms.append(var.Not()) # (1 - x_h) in CP-SAT
                else:
                    cut_terms.append(var)
            model.AddBoolOr(cut_terms)
            
        solver = cp_model.CpSolver()
        solver.parameters.max_time_in_seconds = req.solver_options.max_solve_seconds
        status = solver.Solve(model)
        
        if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
            alt_obj = int(solver.ObjectiveValue())
            alt_selected = {h.id for h in req.hypotheses if solver.Value(x_vars[h.id]) == 1}
            
            alternatives.append({
                "objective_value": alt_obj,
                "objective_delta": best_objective - alt_obj,
                "selected_hypotheses": list(alt_selected)
            })
            banned_configs.append(alt_selected)
        else:
            # No more alternatives exist within the gap
            break
            
    # Classify Stability (Section 34)
    if not alternatives:
        # We need to know if ANY alternative exists to distinguish UNIQUE from STABLE.
        # We run one more quick solve without the gap constraint.
        model_check, x_vars_check, _ = build_cp_model(req)
        # Ban only the primary optimum
        cut_terms = [x_vars_check[h].Not() if h in best_selected_ids else x_vars_check[h] for h in x_vars_check]
        model_check.AddBoolOr(cut_terms)
        solver_check = cp_model.CpSolver()
        status_check = solver_check.Solve(model_check)
        
        if status_check in (cp_model.OPTIMAL, cp_model.FEASIBLE):
            stability = "STABLE"
        else:
            stability = "UNIQUE"
    else:
        stability = "AMBIGUOUS"
        
    return alternatives, stability
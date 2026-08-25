"""
Execution and result extraction logic for the CP-SAT Optimizer.
"""

from ortools.sat.python import cp_model
from reconciliation_prod.reconciliation.protocol import OptimizerRequest, OptimizerResponse
from reconciliation_prod.reconciliation.optimizer_validation import validate_optimizer_request
from reconciliation_prod.reconciliation.model import build_cp_model
from reconciliation_prod.reconciliation.alternatives import find_alternatives_and_stability
from reconciliation_prod.reconciliation.diagnostics import analyze_rejected_hypotheses


def solve_reconciliation(req: OptimizerRequest) -> OptimizerResponse:
    """
    Primary entry point for the Global Optimizer tool.
    Validates the request, builds the CP-SAT model, solves it, 
    and translates raw boolean variables into the domain response.
    """
    # 1. Deterministic Input Validation (Section 27)
    val_resp = validate_optimizer_request(req)
    if val_resp.status == "INVALID_INPUT":
        return val_resp

    # 2. Build the Model
    model, x_vars, u_vars = build_cp_model(req)
    solver = cp_model.CpSolver()
    
    # Configure Solver Options
    solver.parameters.max_time_in_seconds = req.solver_options.max_solve_seconds

    # 3. Solve
    status = solver.Solve(model)

    # 4. Handle Status
    if status == cp_model.OPTIMAL:
        str_status = "OPTIMAL"
    elif status == cp_model.FEASIBLE:
        str_status = "FEASIBLE"
    elif status == cp_model.INFEASIBLE:
        # Returning INFEASIBLE cleanly (Section 35 deep diagnostics happen later)
        return OptimizerResponse(
            status="INFEASIBLE", 
            diagnostics=["Model is globally infeasible. Conflicting constraints detected."]
        )
    else:
        return OptimizerResponse(status="ERROR", diagnostics=[f"Solver failed with status {status}"])

    # 5. Extract Results (Sections 30, 31, 36, 37)
    objective_val = int(solver.ObjectiveValue())
    
    selected_hypotheses = []
    rejected_hypotheses = []
    
    # Fast lookups for residual calculations
    book_consumption_map = {j.id: 0 for j in req.book_items}
    
    for hyp in req.hypotheses:
        # Check if x_h == 1
        is_selected = solver.Value(x_vars[hyp.id]) == 1
        
        hyp_summary = {
            "hypothesis_id": hyp.id,
            "utility": hyp.utility,
            "bank_allocations": [{"bank_item_id": b.bank_item_id, "amount_units": b.amount_units} for b in hyp.bank_allocations],
            "book_allocations": [{"book_item_id": j.book_item_id, "amount_units": j.amount_units} for j in hyp.book_allocations]
        }
        
        if is_selected:
            # Track selected info
            hyp_summary["reason"] = "GLOBALLY_OPTIMAL"
            selected_hypotheses.append(hyp_summary)
            
            # Aggregate Book Consumption
            for j_alloc in hyp.book_allocations:
                book_consumption_map[j_alloc.book_item_id] += j_alloc.amount_int
        else:
            # Basic rejection logic (Deep counterfactual diagnostics are Phase 3)
            hyp_summary["reason"] = "NOT_SELECTED" # Placeholder for deeper diagnostics
            rejected_hypotheses.append(hyp_summary)

    # Unresolved Bank Items (Section 36)
    unresolved_bank_items = []
    for bank in req.bank_items:
        if solver.Value(u_vars[bank.id]) == 1:
            # Find candidate hypotheses for this bank item to provide context
            candidates = [h.id for h in req.hypotheses if any(b.bank_item_id == bank.id for b in h.bank_allocations)]
            unresolved_bank_items.append({
                "bank_item_id": bank.id,
                "candidates_considered": candidates,
                "reason": "NO_CANDIDATES" if not candidates else "ALL_CANDIDATES_GLOBALLY_DISPLACED"
            })

    # Book Residuals (Section 13, 37)
    book_residuals = []
    for book in req.book_items:
        starting = book.remaining_amount_int
        consumed = book_consumption_map[book.id]
        if consumed > 0:
            book_residuals.append({
                "book_item_id": book.id,
                "starting_amount_units": str(starting),
                "consumed_amount_units": str(consumed),
                "remaining_amount_units": str(starting - consumed)
            })

    return OptimizerResponse(
        status=str_status,
        objective_value=objective_val,
        selected_hypotheses=selected_hypotheses,
        rejected_hypotheses=rejected_hypotheses,
        unresolved_bank_items=unresolved_bank_items,
        book_residuals=book_residuals,
        solver_stats={
            "wall_time": solver.WallTime(),
            "conflicts": solver.NumConflicts(),
            "branches": solver.NumBranches()
        }
    )


    best_selected_ids = {h["hypothesis_id"] for h in selected_hypotheses}

    # 6. Deep Diagnostics & Counterfactuals
    rejected_hypotheses = analyze_rejected_hypotheses(req, objective_val, best_selected_ids)

    # 7. Alternative Configurations & Stability
    alternatives, stability = find_alternatives_and_stability(req, objective_val, best_selected_ids)

    # Return combined response
    return OptimizerResponse(
        status=str_status,
        objective_value=objective_val,
        selected_hypotheses=selected_hypotheses,
        rejected_hypotheses=rejected_hypotheses,
        unresolved_bank_items=unresolved_bank_items,
        book_residuals=book_residuals,
        alternatives=alternatives,
        solver_stats={
            "wall_time": solver.WallTime(),
            "conflicts": solver.NumConflicts(),
            "solution_stability": stability
        }
    )
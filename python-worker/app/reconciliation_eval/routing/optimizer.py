from typing import Dict, List, Optional
from pydantic import BaseModel, Field
from ortools.sat.python import cp_model

from domain.bank import BankItem
from routing.feasibility import InvoiceRoutingNode


class RoutedAssignment(BaseModel):
    book_item_id: str
    assigned_account_id: str
    utility_score: int
    rationale: Optional[str] = None


class GlobalRoutingState(BaseModel):
    """The final partitioned state produced by Phase 3, ready for Phase 4 execution."""
    assignments: List[RoutedAssignment] = Field(default_factory=list)
    unroutable_book_ids: List[str] = Field(default_factory=list)


def optimize_global_routing(
    scored_nodes: List[InvoiceRoutingNode],
    accounts_bank_items: Dict[str, List[BankItem]]
) -> GlobalRoutingState:
    """
    Phase 3 Core Function:
    Formulates and solves the Integer Programming model for multi-account invoice routing.
    """
    model = cp_model.CpModel()
    
    # Track decision variables y[i, a] -> Boolean variable indicating if Book Item i goes to Account a
    y: Dict[tuple[str, str], cp_model.IntVar] = {}
    
    # Store utility scores and rationales for fast lookup during results extraction
    scores_lookup: Dict[tuple[str, str], int] = {}
    rationales_lookup: Dict[tuple[str, str], str] = {}
    
    # Precompute total available capacity per account (in units) for global capacity bounds
    account_capacities: Dict[str, int] = {
        acc_id: sum(b.amount_int for b in bank_items)
        for acc_id, bank_items in accounts_bank_items.items()
    }

    # ---------------------------------------------------------------------------
    # 1. DEFINE VARIABLES & FEASIBILITY CONSTRAINTS
    # ---------------------------------------------------------------------------
    for node in scored_nodes:
        book_id = node.book_item_id
        
        # Build map of feasible account IDs for quick feasibility validation
        feasible_accounts = {
            f.account_id for f in node.feasibility_matrix if f.is_feasible
        }
        
        # Build map of LLM utility scores
        score_map = {
            s.account_id: (s.utility_score, s.semantic_rationale)
            for s in getattr(node, "semantic_scores", [])
        }
        
        for acc_id in accounts_bank_items.keys():
            var_name = f"y_{book_id}_{acc_id}"
            y[book_id, acc_id] = model.new_bool_var(var_name)
            
            # Constraint: Hard Feasibility Filter (y_{i,a} <= F_{i,a})
            if acc_id not in feasible_accounts:
                # Force variable to 0 if mathematically infeasible
                model.add(y[book_id, acc_id] == 0)
                scores_lookup[book_id, acc_id] = 0
            else:
                score, rationale = score_map.get(acc_id, (500, "Feasible candidate"))
                scores_lookup[book_id, acc_id] = score
                rationales_lookup[book_id, acc_id] = rationale

    # ---------------------------------------------------------------------------
    # 2. DEFINE EXCLUSIVITY & CAPACITY CONSTRAINTS
    # ---------------------------------------------------------------------------
    # Constraint A: Every book item can be assigned to AT MOST one account
    for node in scored_nodes:
        book_id = node.book_item_id
        model.add(sum(y[book_id, acc_id] for acc_id in accounts_bank_items.keys()) <= 1)

    # Constraint B: Total amount of invoices assigned to an account cannot exceed account capacity
    for acc_id, total_cap in account_capacities.items():
        account_demand = sum(
            node.amount_units * y[node.book_item_id, acc_id]
            for node in scored_nodes
        )
        model.add(account_demand <= total_cap)

    # ---------------------------------------------------------------------------
    # 3. DEFINE OBJECTIVE FUNCTION
    # ---------------------------------------------------------------------------
    W_MONEY = 1_000_000
    W_UTILITY = 1
    
    objective_terms = []
    for node in scored_nodes:
        book_id = node.book_item_id
        amount = node.amount_units
        for acc_id in accounts_bank_items.keys():
            score = scores_lookup.get((book_id, acc_id), 0)
            
            # The objective prioritizes the monetary amount scaled by 1,000,000,
            # with the utility score acting as a tie-breaker.
            obj_val = (amount * W_MONEY) + (score * W_UTILITY)
            objective_terms.append(obj_val * y[book_id, acc_id])
            
    model.maximize(sum(objective_terms))

    # ---------------------------------------------------------------------------
    # 4. SOLVE MODEL
    # ---------------------------------------------------------------------------
    solver = cp_model.CpSolver()
    solver.parameters.max_time_in_seconds = 10.0  # Strict timeout guard
    status = solver.Solve(model)

    result = GlobalRoutingState()

    if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        for node in scored_nodes:
            book_id = node.book_item_id
            assigned_acc: Optional[str] = None
            
            for acc_id in accounts_bank_items.keys():
                if solver.Value(y[book_id, acc_id]) == 1:
                    assigned_acc = acc_id
                    break
            
            if assigned_acc:
                result.assignments.append(
                    RoutedAssignment(
                        book_item_id=book_id,
                        assigned_account_id=assigned_acc,
                        utility_score=scores_lookup.get((book_id, assigned_acc), 0),
                        rationale=rationales_lookup.get((book_id, assigned_acc), "")
                    )
                )
            else:
                result.unroutable_book_ids.append(book_id)
    else:
        # Fallback if solver fails to find a solution: all items marked unroutable
        result.unroutable_book_ids = [node.book_item_id for node in scored_nodes]

    return result
import json
import logging
from pprint import pprint
import sys
from pathlib import Path

# Add paths to sys.path to allow imports
sys.path.insert(0, str(Path("/Users/Yankz/programming/usetoro/python-worker/app/reconciliation_eval")))

from domain.bank import BankItem
from domain.books import BookItem
from domain.hypothesis import ReconciliationHypothesis
from optimizer.protocol import OptimizerRequest, SolverOptions
from optimizer.cp_sat import solve_reconciliation
from scenarios.atlas_construction_sarl.scenario import atlas_const_bank_items, atlas_const_book_items

def main():
    # Load the JSON file
    with open("/Users/Yankz/programming/usetoro/python-worker/app/reconciliation_eval/logs/runs/eval_report_atlas_construction_sarl_20260825_014158.json") as f:
        data = json.load(f)
    
    # Find Run 1 B_OPTIMIZER
    run1 = next(r for r in data if r["configuration"] == "B_OPTIMIZER" and r["run_id"] == "8f0d7f09-f614-4713-a0a2-d79daf98135e")
    
    hypotheses = [ReconciliationHypothesis(**h) for h in run1["llm_proposed_hypotheses"]]
    
    req = OptimizerRequest(
        problem_id="atlas_construction_sarl",
        operation="OPTIMIZE",
        currency="MAD",
        bank_items=atlas_const_bank_items,
        book_items=atlas_const_book_items,
        hypotheses=hypotheses,
        forced_hypothesis_ids=[],
        solver_options=SolverOptions()
    )
    
    print("Running solver...")
    response = solve_reconciliation(req)
    
    print(f"Solver status: {response.status}")
    print("Selected matches:")
    for m in response.matches:
        print(f"  {m.group_id}")

if __name__ == "__main__":
    main()

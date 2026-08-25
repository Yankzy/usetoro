import json
import logging
from typing import List, Dict, Any
from domain.bank import BankItem
from domain.books import BookItem
from optimizer.protocol import OptimizerRequest, SolverOptions
from optimizer.cp_sat import solve_reconciliation
from domain.hypothesis import ReconciliationHypothesis

logger = logging.getLogger(__name__)

OPTIMIZER_TOOL_SCHEMA = {
    "type": "function",
    "function": {
        "name": "global_reconciliation_optimizer",
        "description": "Tests whether proposed reconciliation hypotheses can coexist globally under exact accounting constraints. Use this to detect capacity conflicts and find the optimal global assignment.",
        "parameters": {
            "type": "object",
            "properties": {
                "operation": {
                    "type": "string",
                    "enum": ["OPTIMIZE", "CHECK_PROPOSAL"],
                    "description": "OPTIMIZE finds the best global configuration. CHECK_PROPOSAL tests if specific hypotheses are jointly feasible."
                },
                "hypotheses": {
                    "type": "array",
                    "description": "List of proposed reconciliation groups.",
                    "items": {
                        "type": "object",
                        "properties": {
                            "id": {"type": "string"},
                            "eligibility": {"type": "string", "enum": ["SELECTABLE", "COUNTERFACTUAL_ONLY"]},
                            "utility": {"type": "integer", "minimum": 0, "maximum": 1000, "description": "Relative preference score."},
                            "bank_allocations": {
                                "type": "array",
                                "minItems": 1,
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "bank_item_id": {"type": "string"},
                                        "amount_units": {"type": "string"}
                                    },
                                    "required": ["bank_item_id", "amount_units"]
                                }
                            },
                            "book_allocations": {
                                "type": "array",
                                "minItems": 1,
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "book_item_id": {"type": "string"},
                                        "amount_units": {"type": "string"}
                                    },
                                    "required": ["book_item_id", "amount_units"]
                                }
                            },
                            "semantic_rationale": {"type": "string"}
                        },
                        "required": ["id", "utility", "bank_allocations", "book_allocations"]
                    }
                },
                "forced_hypothesis_ids": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Used with CHECK_PROPOSAL to force specific hypotheses."
                }
            },
            "required": ["operation", "hypotheses"]
        }
    }
}

def execute_optimizer_tool(
    tool_args: Dict[str, Any], 
    problem_id: str,
    currency: str,
    original_bank_items: List[BankItem],
    original_book_items: List[BookItem]
) -> str:
    """
    Executes the CP-SAT optimizer from LLM arguments and returns the JSON result.
    """
    try:
        # Parse arguments into domain models
        hypotheses = [ReconciliationHypothesis(**h) for h in tool_args.get("hypotheses", [])]
        
        req = OptimizerRequest(
            problem_id=problem_id,
            operation=tool_args.get("operation", "OPTIMIZE"),
            currency=currency,
            bank_items=original_bank_items,
            book_items=original_book_items,
            hypotheses=hypotheses,
            forced_hypothesis_ids=tool_args.get("forced_hypothesis_ids", []),
            solver_options=SolverOptions()
        )
        
        # Run the math
        response = solve_reconciliation(req)
        
        # Return serialized response to the LLM
        return response.model_dump_json(exclude_none=True)
        
    except Exception as e:
        logger.error(f"Optimizer execution failed: {e}")
        return json.dumps({"status": "ERROR", "diagnostics": [f"Tool execution error: {str(e)}"]})
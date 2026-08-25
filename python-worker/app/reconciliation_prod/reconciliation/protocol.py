from typing import List, Optional, Literal
from pydantic import BaseModel, Field
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.domain.hypothesis import ReconciliationHypothesis

class SolverOptions(BaseModel):
    max_solve_seconds: int = Field(default=10)
    max_alternatives: int = Field(default=3)
    alternative_objective_gap: int = Field(default=50)

class OptimizerRequest(BaseModel):
    problem_id: str
    operation: Literal["OPTIMIZE", "CHECK_PROPOSAL"] = "OPTIMIZE"
    currency: str
    bank_items: List[BankItem]
    book_items: List[BookItem]
    hypotheses: List[ReconciliationHypothesis]
    forced_hypothesis_ids: List[str] = Field(default_factory=list)
    solver_options: SolverOptions = Field(default_factory=SolverOptions)

class OptimizerResponse(BaseModel):
    status: Literal["OPTIMAL", "FEASIBLE", "INFEASIBLE", "INVALID_INPUT", "ERROR"]
    objective_value: Optional[int] = None
    selected_hypotheses: List[dict] = Field(default_factory=list)
    rejected_hypotheses: List[dict] = Field(default_factory=list)
    unresolved_bank_items: List[dict] = Field(default_factory=list)
    book_residuals: List[dict] = Field(default_factory=list)
    alternatives: List[dict] = Field(default_factory=list)
    diagnostics: List[str] = Field(default_factory=list)
    solver_stats: dict = Field(default_factory=dict)
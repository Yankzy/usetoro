"""
Bookkeeping Natural-Language Operator package (evaluation wrapper).
"""

from __future__ import annotations

from bookkeeping_state.operator import (
    BookkeepingOperator,
    OPERATOR_SYSTEM_PROMPT,
    OPERATOR_TOOLS,
    execute_operator_tool,
    get_operator_model_name,
)
from .workbench import (
    BookkeepingWorkbench,
    create_demo_repository,
    seed_scenario_repository,
)

__all__ = [
    "BookkeepingOperator",
    "BookkeepingWorkbench",
    "OPERATOR_SYSTEM_PROMPT",
    "OPERATOR_TOOLS",
    "execute_operator_tool",
    "get_operator_model_name",
    "seed_scenario_repository",
]

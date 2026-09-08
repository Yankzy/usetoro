"""
Bookkeeping Natural-Language Operator package.
"""

from .agent import BookkeepingOperator, get_operator_model_name
from .prompts import OPERATOR_SYSTEM_PROMPT
from .tools import OPERATOR_TOOLS, execute_operator_tool
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
    "create_demo_repository",
    "execute_operator_tool",
    "get_operator_model_name",
    "seed_scenario_repository",
]

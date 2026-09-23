"""
Adversarial Conversational Evaluation Harness for Bookkeeping Operator.
"""

from .cases import CANONICAL_CASES, get_canonical_cases, get_case
from .models import (
    CaseResult,
    ConversationCase,
    ConversationTurn,
    FailureCategory,
    FailureReason,
    MutationPolicy,
    StateSnapshot,
    SuiteResult,
    ToolCallRecord,
    TurnEnvelope,
    TurnVerdict,
)
from .reporting import format_verbose_turn, print_suite_report
from .runner import ConversationRunner
from .validators import validate_turn_envelope
from .worlds import capture_state_snapshot

__all__ = (
    "CANONICAL_CASES",
    "get_canonical_cases",
    "get_case",
    "CaseResult",
    "ConversationCase",
    "ConversationTurn",
    "FailureCategory",
    "FailureReason",
    "MutationPolicy",
    "StateSnapshot",
    "SuiteResult",
    "ToolCallRecord",
    "TurnEnvelope",
    "TurnVerdict",
    "format_verbose_turn",
    "print_suite_report",
    "ConversationRunner",
    "validate_turn_envelope",
    "capture_state_snapshot",
)

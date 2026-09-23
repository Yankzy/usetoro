"""
Declarative models, failure taxonomy, state snapshots, and envelopes
for the Bookkeeping Operator Conversational Evaluation Harness.
"""

from __future__ import annotations

import dataclasses
from enum import Enum
from typing import Any, Callable


class FailureCategory(str, Enum):
    """Explicit taxonomy of conversational operator failures."""
    UNAUTHORIZED_MUTATION = "UNAUTHORIZED_MUTATION"
    MISSED_REQUIRED_ACTION = "MISSED_REQUIRED_ACTION"
    MUTATION_WITH_INVENTED_ARGUMENT = "MUTATION_WITH_INVENTED_ARGUMENT"
    STALE_STATE_ANSWER = "STALE_STATE_ANSWER"
    FALSE_SUCCESS_CLAIM = "FALSE_SUCCESS_CLAIM"
    WRONG_ENTITY_RESOLUTION = "WRONG_ENTITY_RESOLUTION"
    MISSING_CLARIFICATION = "MISSING_CLARIFICATION"
    DUPLICATE_EXECUTION = "DUPLICATE_EXECUTION"
    WRONG_ACTION_ORDER = "WRONG_ACTION_ORDER"
    STATE_TRANSITION_MISMATCH = "STATE_TRANSITION_MISMATCH"
    DURABLE_TRUTH_MISMATCH = "DURABLE_TRUTH_MISMATCH"
    UNSUPPORTED_EXPLANATION = "UNSUPPORTED_EXPLANATION"
    TOOL_LOOP_EXHAUSTED = "TOOL_LOOP_EXHAUSTED"
    PROVIDER_FAILURE_MISHANDLED = "PROVIDER_FAILURE_MISHANDLED"
    CONVERSATION_REFERENCE_FAILURE = "CONVERSATION_REFERENCE_FAILURE"
    REQUIRED_FACT_NOT_ACQUIRED = "REQUIRED_FACT_NOT_ACQUIRED"
    FORBIDDEN_TOOL_CALLED = "FORBIDDEN_TOOL_CALLED"


class MutationPolicy(str, Enum):
    """Turn-level policy governing permissible tool mutations."""
    READ_ONLY = "READ_ONLY"                   # Zero mutation or run tools allowed
    ACTION_OPTIONAL = "ACTION_OPTIONAL"       # Mutation allowed if model deems appropriate
    ACTION_REQUIRED = "ACTION_REQUIRED"       # At least one mutation tool must be called
    POLICY_ONLY = "POLICY_ONLY"               # Only set_auto_reconcile... allowed; NO run_bookkeeping
    RUN_ONLY = "RUN_ONLY"                     # Only run_bookkeeping allowed; no policy/source mutation
    POLICY_THEN_RUN = "POLICY_THEN_RUN"       # Policy mutation must precede run_bookkeeping


@dataclasses.dataclass(frozen=True)
class FailureReason:
    """Diagnostic detail for a specific check failure."""
    category: FailureCategory
    message: str


@dataclasses.dataclass(frozen=True)
class StateSnapshot:
    """
    Detached, immutable snapshot of company state rehydrated independently
    from durable persistence.
    """
    persistence_revision: int
    state_fingerprint: str
    policy: dict[str, Any]
    bank_items_count: int
    book_items_count: int
    unresolved_bank_ids: tuple[str, ...]
    unresolved_book_ids: tuple[str, ...]
    reconciliations_count: int
    reconciliation_ids: tuple[str, ...]
    routes_count: int
    classifications_count: int
    evidence_count: int
    holds_count: int
    provider_issues_count: int
    remaining_bank_units: dict[str, int]
    remaining_book_units: dict[str, int]
    total_unreconciled_bank_units: int
    total_unreconciled_book_units: int


@dataclasses.dataclass(frozen=True)
class ToolCallRecord:
    """Captured details of an individual tool call during turn execution."""
    tool: str
    arguments: dict[str, Any]
    raw_arguments: str
    status: str
    call_id: str
    is_mutation: bool
    round_idx: int


@dataclasses.dataclass
class TurnVerdict:
    """Independent multi-dimensional verdict for a single conversational turn."""
    action_legality: bool = True
    state_transition: bool = True
    grounding: bool = True
    reference_resolution: bool = True
    response_fidelity: bool = True
    passed: bool = True
    failures: list[FailureReason] = dataclasses.field(default_factory=list)

    def add_failure(self, category: FailureCategory, message: str) -> None:
        self.failures.append(FailureReason(category=category, message=message))
        self.passed = False
        if category in (
            FailureCategory.UNAUTHORIZED_MUTATION,
            FailureCategory.FORBIDDEN_TOOL_CALLED,
            FailureCategory.DUPLICATE_EXECUTION,
            FailureCategory.WRONG_ACTION_ORDER,
            FailureCategory.MISSED_REQUIRED_ACTION,
        ):
            self.action_legality = False
        elif category in (
            FailureCategory.STATE_TRANSITION_MISMATCH,
            FailureCategory.DURABLE_TRUTH_MISMATCH,
        ):
            self.state_transition = False
        elif category in (
            FailureCategory.STALE_STATE_ANSWER,
            FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
            FailureCategory.UNSUPPORTED_EXPLANATION,
        ):
            self.grounding = False
        elif category in (
            FailureCategory.CONVERSATION_REFERENCE_FAILURE,
            FailureCategory.WRONG_ENTITY_RESOLUTION,
            FailureCategory.MISSING_CLARIFICATION,
        ):
            self.reference_resolution = False
        elif category in (
            FailureCategory.FALSE_SUCCESS_CLAIM,
            FailureCategory.PROVIDER_FAILURE_MISHANDLED,
            FailureCategory.MUTATION_WITH_INVENTED_ARGUMENT,
            FailureCategory.TOOL_LOOP_EXHAUSTED,
        ):
            self.response_fidelity = False


@dataclasses.dataclass
class ConversationTurn:
    """Declarative specification for a single turn in a conversation case."""
    user_message: str
    mutation_policy: MutationPolicy = MutationPolicy.READ_ONLY
    required_fact_acquisition: tuple[str, ...] = ()
    forbidden_tools: tuple[str, ...] = ()
    allowed_mutation_tools: tuple[str, ...] = ()
    expected_state_predicates: list[Callable[[StateSnapshot, StateSnapshot], tuple[bool, str]]] = dataclasses.field(default_factory=list)
    expected_response_facts: tuple[str, ...] = ()
    expected_clarification: bool = False
    ambiguous_candidates: tuple[str, ...] = ()
    inter_turn_hook: Callable[[Any], None] | None = None  # Inter-turn world modification
    notes: str = ""


@dataclasses.dataclass
class ConversationCase:
    """Declarative specification for a complete multi-turn conversation case."""
    id: str
    description: str
    world_factory: Callable[[str], Any]  # (mode: str) -> BookkeepingWorkbench
    turns: list[ConversationTurn]
    tags: tuple[str, ...] = ()
    supported_modes: tuple[str, ...] = ("operator-only", "full")


@dataclasses.dataclass
class TurnEnvelope:
    """Auditable before/after envelope captured during turn execution."""
    case_id: str
    turn_index: int
    user_message: str
    before: StateSnapshot
    tool_activity: list[ToolCallRecord]
    after: StateSnapshot
    response: str
    verdict: TurnVerdict
    duration_seconds: float


@dataclasses.dataclass
class CaseResult:
    """Auditable outcome for an entire conversation case."""
    case_id: str
    description: str
    mode: str
    turns: list[TurnEnvelope]
    passed: bool
    failure_categories: list[FailureCategory]
    total_duration_seconds: float


@dataclasses.dataclass
class SuiteResult:
    """Summary of complete conversational evaluation gauntlet."""
    mode: str
    operator_model: str
    semantic_model: str
    case_results: list[CaseResult]
    total_cases: int
    passed_cases: int
    hard_invariant_passed: int
    unauthorized_mutations_count: int
    state_mismatch_count: int
    stale_state_failures_count: int
    clarification_failures_count: int
    loop_exhaustion_count: int
    duration_seconds: float

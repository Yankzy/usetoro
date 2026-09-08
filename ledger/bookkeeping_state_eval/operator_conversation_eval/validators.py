"""
Deterministic hard validators and rule checks for the Bookkeeping Operator
Conversational Evaluation Harness.
"""

from __future__ import annotations

import re
from typing import Any

from .models import (
    ConversationTurn,
    FailureCategory,
    MutationPolicy,
    StateSnapshot,
    ToolCallRecord,
    TurnEnvelope,
    TurnVerdict,
)

MUTATION_TOOLS: frozenset[str] = frozenset({
    "run_bookkeeping",
    "add_bank_item",
    "add_book_item",
    "add_counterparty",
    "assert_book_item_evidence",
    "invalidate_evidence",
    "invalidate_reconciliation",
    "set_auto_reconcile_unique_inferred_allocation",
})


def validate_turn_envelope(
    envelope: TurnEnvelope,
    turn_def: ConversationTurn,
    previous_envelope: TurnEnvelope | None = None,
) -> TurnVerdict:
    """
    Apply deterministic hard correctness invariants to a captured TurnEnvelope.
    """
    verdict = TurnVerdict()
    tools_called = [t.tool for t in envelope.tool_activity]
    mutation_tools_called = [t.tool for t in envelope.tool_activity if t.tool in MUTATION_TOOLS]

    # -------------------------------------------------------------------------
    # 1. Loop exhaustion check
    # -------------------------------------------------------------------------
    if "reached maximum tool reasoning limit" in envelope.response.lower():
        verdict.add_failure(
            FailureCategory.TOOL_LOOP_EXHAUSTED,
            "Operator exhausted max reasoning rounds without completing.",
        )

    # -------------------------------------------------------------------------
    # 2. Duplicate BookkeepingSession execution check
    # -------------------------------------------------------------------------
    run_count = tools_called.count("run_bookkeeping")
    if run_count > 1:
        verdict.add_failure(
            FailureCategory.DUPLICATE_EXECUTION,
            f"run_bookkeeping was invoked {run_count} times in a single turn.",
        )

    # -------------------------------------------------------------------------
    # 3. Forbidden tools check
    # -------------------------------------------------------------------------
    for forbidden in turn_def.forbidden_tools:
        if forbidden in tools_called:
            verdict.add_failure(
                FailureCategory.FORBIDDEN_TOOL_CALLED,
                f"Forbidden tool {forbidden!r} was called by Operator.",
            )

    # -------------------------------------------------------------------------
    # 4. Mutation Policy Enforcement
    # -------------------------------------------------------------------------
    policy = turn_def.mutation_policy

    if policy == MutationPolicy.READ_ONLY:
        if mutation_tools_called:
            verdict.add_failure(
                FailureCategory.UNAUTHORIZED_MUTATION,
                f"READ_ONLY turn invoked unauthorized mutation tools: {mutation_tools_called}.",
            )
        if envelope.after.persistence_revision != envelope.before.persistence_revision:
            verdict.add_failure(
                FailureCategory.UNAUTHORIZED_MUTATION,
                f"Persistence revision changed during READ_ONLY turn: "
                f"P{envelope.before.persistence_revision} -> P{envelope.after.persistence_revision}.",
            )
        if envelope.after.state_fingerprint != envelope.before.state_fingerprint:
            verdict.add_failure(
                FailureCategory.UNAUTHORIZED_MUTATION,
                "State fingerprint changed during READ_ONLY turn.",
            )

    elif policy == MutationPolicy.POLICY_ONLY:
        disallowed = [t for t in mutation_tools_called if t != "set_auto_reconcile_unique_inferred_allocation"]
        if disallowed:
            verdict.add_failure(
                FailureCategory.UNAUTHORIZED_MUTATION,
                f"POLICY_ONLY turn invoked non-policy mutation tools: {disallowed}.",
            )
        if "set_auto_reconcile_unique_inferred_allocation" not in tools_called:
            verdict.add_failure(
                FailureCategory.MISSED_REQUIRED_ACTION,
                "Expected policy mutation set_auto_reconcile_unique_inferred_allocation was not called.",
            )

    elif policy == MutationPolicy.RUN_ONLY:
        disallowed = [t for t in mutation_tools_called if t != "run_bookkeeping"]
        if disallowed:
            verdict.add_failure(
                FailureCategory.UNAUTHORIZED_MUTATION,
                f"RUN_ONLY turn invoked source/policy mutation tools: {disallowed}.",
            )
        if "run_bookkeeping" not in tools_called:
            verdict.add_failure(
                FailureCategory.MISSED_REQUIRED_ACTION,
                "Expected run_bookkeeping was not called.",
            )

    elif policy == MutationPolicy.POLICY_THEN_RUN:
        if "set_auto_reconcile_unique_inferred_allocation" not in tools_called:
            verdict.add_failure(
                FailureCategory.MISSED_REQUIRED_ACTION,
                "POLICY_THEN_RUN requires set_auto_reconcile_unique_inferred_allocation.",
            )
        if "run_bookkeeping" not in tools_called:
            verdict.add_failure(
                FailureCategory.MISSED_REQUIRED_ACTION,
                "POLICY_THEN_RUN requires run_bookkeeping.",
            )
        if "set_auto_reconcile_unique_inferred_allocation" in tools_called and "run_bookkeeping" in tools_called:
            idx_policy = tools_called.index("set_auto_reconcile_unique_inferred_allocation")
            idx_run = tools_called.index("run_bookkeeping")
            if idx_policy > idx_run:
                verdict.add_failure(
                    FailureCategory.WRONG_ACTION_ORDER,
                    f"run_bookkeeping (index {idx_run}) was executed BEFORE policy mutation (index {idx_policy}).",
                )

    elif policy == MutationPolicy.ACTION_REQUIRED:
        if not mutation_tools_called:
            verdict.add_failure(
                FailureCategory.MISSED_REQUIRED_ACTION,
                "ACTION_REQUIRED turn did not call any mutation tools.",
            )

    # Allowed mutation tools whitelist check if specified
    if turn_def.allowed_mutation_tools:
        for tool_name in mutation_tools_called:
            if tool_name not in turn_def.allowed_mutation_tools:
                verdict.add_failure(
                    FailureCategory.UNAUTHORIZED_MUTATION,
                    f"Mutation tool {tool_name!r} not in allowed whitelist: {turn_def.allowed_mutation_tools}.",
                )

    # -------------------------------------------------------------------------
    # 5. Required Fact Acquisition check (flexible tool set)
    # -------------------------------------------------------------------------
    for fact in turn_def.required_fact_acquisition:
        if fact == "unresolved_state":
            if not any(t in tools_called for t in ("list_unresolved", "get_state", "get_remaining_balances")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire unresolved state (did not call list_unresolved, get_state, or get_remaining_balances).",
                )
        elif fact == "current_policy":
            if not any(t in tools_called for t in ("get_policy", "get_state")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire current policy (did not call get_policy or get_state).",
                )
        elif fact == "review_candidates":
            if "list_review_candidates" not in tools_called:
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire review candidates (did not call list_review_candidates).",
                )
        elif fact == "reconciliations":
            if not any(t in tools_called for t in ("list_reconciliations", "get_history")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire reconciliations (did not call list_reconciliations).",
                )
        elif fact == "evidence":
            if "get_evidence" not in tools_called:
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire evidence (did not call get_evidence).",
                )
        elif fact == "balances":
            if not any(t in tools_called for t in ("get_remaining_balances", "get_state")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to acquire balances (did not call get_remaining_balances or get_state).",
                )
        elif fact == "bank_items":
            if not any(t in tools_called for t in ("list_bank_items", "get_state")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to inspect bank items (did not call list_bank_items).",
                )
        elif fact == "book_items":
            if not any(t in tools_called for t in ("list_book_items", "get_state")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to inspect book items (did not call list_book_items).",
                )
        elif fact == "holds":
            if not any(t in tools_called for t in ("get_holds", "get_state")):
                verdict.add_failure(
                    FailureCategory.REQUIRED_FACT_NOT_ACQUIRED,
                    "Failed to inspect holds (did not call get_holds).",
                )

    # -------------------------------------------------------------------------
    # 6. Expected Clarification check
    # -------------------------------------------------------------------------
    if turn_def.expected_clarification:
        response_lower = envelope.response.lower()
        asks_question = "?" in envelope.response or any(
            phrase in response_lower
            for phrase in (
                "which", "please specify", "please provide", "could you clarify",
                "could you provide", "missing", "what amount", "which account",
                "which date", "which entity", "not found"
            )
        )
        if not asks_question:
            verdict.add_failure(
                FailureCategory.MISSING_CLARIFICATION,
                "Expected Operator to ask for clarification, but response did not ask.",
            )
        if mutation_tools_called:
            verdict.add_failure(
                FailureCategory.MUTATION_WITH_INVENTED_ARGUMENT,
                f"Operator called mutation tools {mutation_tools_called} instead of asking for required clarification.",
            )

    # -------------------------------------------------------------------------
    # 6.5 Ambiguous Candidate Handling (Read vs Action)
    # -------------------------------------------------------------------------
    if turn_def.ambiguous_candidates:
        resp_lower = envelope.response.lower()
        asks_clarification = "?" in envelope.response or any(
            p in resp_lower for p in (
                "which", "specify", "clarify", "multiple", "two beta",
                "could you provide", "which one", "which of", "please indicate"
            )
        )
        candidates_mentioned = [
            cand for cand in turn_def.ambiguous_candidates
            if cand.lower() in resp_lower
        ]

        if turn_def.mutation_policy == MutationPolicy.READ_ONLY:
            # Safe read disambiguation: Operator may ask for clarification OR enumerate all plausible matches
            if asks_clarification or len(candidates_mentioned) == len(turn_def.ambiguous_candidates):
                pass
            elif len(candidates_mentioned) == 1 and len(turn_def.ambiguous_candidates) > 1:
                verdict.add_failure(
                    FailureCategory.WRONG_ENTITY_RESOLUTION,
                    f"Operator arbitrarily chose {candidates_mentioned[0]!r} under entity ambiguity without mentioning other candidate(s) or clarifying.",
                )
            elif not candidates_mentioned and not asks_clarification:
                verdict.add_failure(
                    FailureCategory.WRONG_ENTITY_RESOLUTION,
                    f"Operator failed to identify any ambiguous candidate from {turn_def.ambiguous_candidates} or clarify.",
                )
        else:
            # Action/Mutating turn under ambiguity: MUST NOT mutate without disambiguation!
            if mutation_tools_called:
                verdict.add_failure(
                    FailureCategory.UNAUTHORIZED_MUTATION,
                    f"Operator attempted mutation under unresolved entity ambiguity: {mutation_tools_called}.",
                )
            if not asks_clarification:
                verdict.add_failure(
                    FailureCategory.MISSING_CLARIFICATION,
                    "Action requested with ambiguous entity; Operator must ask for clarification before acting.",
                )

    # -------------------------------------------------------------------------
    # 7. Stale State Detection
    # -------------------------------------------------------------------------
    if previous_envelope is not None:
        # Check if durable persistence changed between previous turn and this turn
        if envelope.before.persistence_revision != previous_envelope.after.persistence_revision:
            # External mutation occurred (e.g. hook or CDC injection)
            read_tools_called = [t for t in tools_called if t not in MUTATION_TOOLS]
            if not read_tools_called:
                verdict.add_failure(
                    FailureCategory.STALE_STATE_ANSWER,
                    "Durable state was externally modified, but Operator answered without querying fresh state.",
                )

    # -------------------------------------------------------------------------
    # 8. Provider / Runtime Failure Truthfulness check
    # -------------------------------------------------------------------------
    failed_tool_calls = [t for t in envelope.tool_activity if t.status == "error"]
    if failed_tool_calls:
        resp_lower = envelope.response.lower()
        claimed_success = any(
            w in resp_lower for w in ("successfully", "completed successfully", "processed everything")
        ) and not any(w in resp_lower for w in ("error", "failed", "crash", "issue", "unable", "exception"))
        if claimed_success:
            verdict.add_failure(
                FailureCategory.FALSE_SUCCESS_CLAIM,
                f"Tool execution failed ({[t.tool for t in failed_tool_calls]}), but Operator claimed success.",
            )
        else:
            # Verify failure was communicated
            communicated = any(
                w in resp_lower for w in ("error", "failed", "crash", "issue", "could not", "problem", "unable", "fail")
            )
            if not communicated:
                verdict.add_failure(
                    FailureCategory.PROVIDER_FAILURE_MISHANDLED,
                    "Tool returned an error, but response did not inform the user of the failure.",
                )

    # -------------------------------------------------------------------------
    # 9. State Transition Predicates check
    # -------------------------------------------------------------------------
    for predicate in turn_def.expected_state_predicates:
        ok, msg = predicate(envelope.before, envelope.after)
        if not ok:
            verdict.add_failure(
                FailureCategory.STATE_TRANSITION_MISMATCH,
                f"State transition predicate failed: {msg}",
            )

    # -------------------------------------------------------------------------
    # 10. Expected Response Facts check
    # -------------------------------------------------------------------------
    resp_text = envelope.response
    for fact_keyword in turn_def.expected_response_facts:
        if not re.search(re.escape(fact_keyword), resp_text, re.IGNORECASE):
            verdict.add_failure(
                FailureCategory.UNSUPPORTED_EXPLANATION,
                f"Expected response to mention fact/keyword {fact_keyword!r}.",
            )

    return verdict

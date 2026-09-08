from agents.extensions.handoff_prompt import prompt_with_handoff_instructions
from agents import Agent
from .types import (
    RuntimeEvents,
    LocalContext,
)
from .tools import *
from .prompts import *

account_suggestion_agent = Agent[LocalContext](
    name="account_suggestion_agent",
    instructions=prompt_with_handoff_instructions(account_suggestion_instructions),
    model="gpt-4.1",
    tools=[
        create_proposed_account,
        get_roles_by_category,
        validate_roles,
        find_similar_accounts,
        validate_account_proposal,
    ],
    hooks=RuntimeEvents()
)


rule_quality_auditor = Agent[LocalContext](
    name="rule_auditor",
    instructions=prompt_with_handoff_instructions(rule_quality_instruction),
    model="gpt-4.1",
    tools=[
        find_similar_rules,
        verify_target_account_exists,
        validate_rule_structure,
    ],
    hooks=RuntimeEvents()
)

rule_creation_agent = Agent[LocalContext](
    name="rule_creation_agent",
    instructions=prompt_with_handoff_instructions(rule_instructions),
    model="gpt-4.1",
    tools=[
        list_chart_of_accounts_local,
    ],
    hooks=RuntimeEvents()
)

rule_creation_for_known_account_agent = Agent[LocalContext](
    name="rule_creation_for_known_account_agent",
    instructions=prompt_with_handoff_instructions(rule_instructions_known_account),
    model="gpt-4.1",
    tools=[
        rule_quality_auditor.as_tool(
            tool_name="rule_quality_auditor",
            tool_description="Assures rule quality and prevent Duplicates."
        ),
    ],
    hooks=RuntimeEvents()
)



from agents.extensions.handoff_prompt import prompt_with_handoff_instructions
from .tools import *
from trading.trading_agents.types import ContextAwareAgent


bookkeeping_agent = ContextAwareAgent(
    name="bookkeeping_agent",
    instructions=prompt_with_handoff_instructions(
        """
        You are the Bookkeeping Agent (not a full fledge accounting agent).
        General Instructions:
        - Use the available tools to manage journal entries.
        - Always return concise, user-friendly summaries. 
        - Do not expose internal IDs unless necessary.
        - If a user request cannot be fulfilled by the available tools, explain why and suggest using the app UI.
        - If a user asks for something outside your scope, politely decline and suggest the correct workflow.
        - You cannot do the following: create a customer or vendor account, user can do these in app settings.
        - If an error occurs, always return a concise error message.
        - Never tell user how to do something, it's your job to do work for user, you can only ask for missing info to do your work.
        - After answering the user, always suggest 1 or 2 next steps the user can take.
        - Don't be verbose or try to teach user what to say, be concise.
        - Do not send complicated accounting requests to user, you're in a conversation with someone who know nothing about accounting, so be simple, concise and ask one question at a time.
        """
    ),
    model="gpt-5.1",
    tools=[
        create_business_entity,
        list_business_entities,
        list_chart_of_accounts,
        get_one_chart_of_account,
        create_journal_entry,
        update_journal_entry,
        delete_journal_entry,
        change_journal_entry_state,
        list_journal_entries,
        get_journal_entry,
        list_journal_entries_by_year,
        list_journal_entries_by_month,
        update_journal_entry_transactions,
        list_ledger_accounts_for_journal_entry,
    ],
    hooks=RuntimeEvents()
)

"""
PlaidAgentManager

Lightweight manager for running and coordinating Plaid-related bookkeeping agents.

Features:
- A simple registry for known Plaid agents (register/override).
- Async wrappers around `Runner.run` for single-run and batch-run use cases.
- Concurrency-controlled batch execution using asyncio.Semaphore.
- Helpers adapted for trimming transactions
  and running the account suggestion agent on individual transactions or the
  sample transactions.json file used by the project.

This module is intentionally minimal: it delegates core agent execution to
`agents.Runner` and focuses on orchestration, error isolation, and convenience
helpers used across bookkeeping workflows.
"""

import asyncio
import json
import logging
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Optional

from agents import Runner, RunConfig
from django.conf import importlib

from .types import LocalContext, PlaidTransaction
from .plaid_agents import (
    account_suggestion_agent,
    rule_quality_auditor,
    rule_creation_agent,
    rule_creation_for_known_account_agent,
)

logger = logging.getLogger(__name__)


class PlaidAgentManager:
    """
    Manager for Plaid bookkeeping agents.

    Responsibilities
    - Maintain a registry of agents used by Plaid-related workflows.
    - Provide an ergonomic async API for running single agents and running an
      agent across many transactions concurrently.
    - Include transaction helpers for the account suggestion flow.

    Design goals
    - Minimal abstraction over `agents.Runner` to avoid duplicating execution logic.
    - Safe parallelism: capture per-transaction errors without failing the whole batch.
    - Small, well-documented surface area for easy reuse in services/tests.
    """

    def __init__(self, agents: Optional[Dict[str, Any]] = None):
        """
        Initialize the manager.

        - `agents`: optional mapping name -> Agent to override the defaults.
        """
        default = {
            "account_suggestion_agent": account_suggestion_agent,
            "rule_auditor": rule_quality_auditor,
            "rule_creation_agent": rule_creation_agent,
            "rule_creation_for_known_account_agent": rule_creation_for_known_account_agent,
        }
        self._agents: Dict[str, Any] = agents or default

    def register(self, name: str, agent: Any) -> None:
        """
        Register or override an agent in the manager.

        - `name`: the lookup key for retrieving the agent.
        - `agent`: an `Agent` object (from your agents framework).
        """
        self._agents[name] = agent

    def get_agent(self, name: str) -> Any:
        """
        Return a registered agent by name.

        Raises:
            KeyError if the agent is not registered.
        """
        if name not in self._agents:
            raise KeyError(f"Agent '{name}' not registered")
        return self._agents[name]

    async def run_agent(
        self,
        name: str,
        input_text: str,
        context: Optional[Any] = None,
        max_turns: int = 10,
        **runner_kwargs: Any,
    ) -> Any:
        """
        Run a single agent via `Runner.run`.

        - `name`: agent key registered in the manager.
        - `input_text`: textual input passed to the agent (often JSON for structured inputs).
        - `context`: optional Pydantic model or dict used as the run context (e.g., `LocalContext`).
        - `max_turns`: forwarded to the Runner to limit multi-turn executions.
        - `runner_kwargs`: forwarded to `Runner.run` for additional runner options.

        Returns the Runner result object (framework-specific).
        """
        agent = self.get_agent(name)
        return await Runner.run(starting_agent=agent, input=input_text, context=context, max_turns=max_turns, **runner_kwargs)

    async def run_on_transactions(
        self,
        name: str,
        transactions: Iterable[dict],
        context_factory: Optional[Callable[[dict], Any]] = None,
        concurrency: int = 10,
        max_turns: int = 10,
        timeout: Optional[float] = None,
    ) -> List[Dict[str, Any]]:
        """
        Run a registered agent on many transactions concurrently.

        - `transactions`: iterable of transaction dicts.
        - `context_factory(tx)`: optional callable returning a context for each tx.
        - `concurrency`: maximum number of parallel Runner executions.
        - `timeout`: optional per-task timeout in seconds.

        Returns a list of dicts with either:
            {"tx": tx, "result": runner_result}
        or
            {"tx": tx, "error": "<exception str>"}

        Errors are isolated per-transaction to allow other tasks to continue.
        """
        agent = self.get_agent(name)
        sem = asyncio.Semaphore(concurrency)
        results: List[Dict[str, Any]] = []

        async def _run(tx: dict) -> None:
            # Build per-run context if requested
            ctx = context_factory(tx) if context_factory else None
            try:
                async with sem:
                    coro = Runner.run(starting_agent=agent, input=tx.get("text", str(tx)), context=ctx, max_turns=max_turns)
                    res = await asyncio.wait_for(coro, timeout=timeout) if timeout else await coro
                results.append({"tx": tx, "result": res})
            except Exception as exc:
                # Log and capture the error; don't let one failure stop the batch
                logger.exception("Agent run failed for transaction")
                results.append({"tx": tx, "error": str(exc)})

        # Launch tasks and await completion
        tasks = [asyncio.create_task(_run(tx)) for tx in transactions]
        if tasks:
            await asyncio.gather(*tasks)
        return results

    # -------------------------
    # Account suggestion helpers (extracted and adapted)
    # -------------------------
    @staticmethod
    async def trim_tx_for_llm(tx: dict) -> dict:
        """
        Produce a minimal transaction representation suitable for LLM input.

        Keeps only the fields the LLM needs (amount, date, categories, merchant/name,
        location, counterparties, currency, payment_channel and a composed description).
        Ensures types are safe (e.g., float for amount, list for categories).
        """
        amt = tx.get("amount", 0.0)
        try:
            amount = float(amt)
        except (TypeError, ValueError):
            amount = 0.0

        raw_cat = tx.get("category")
        if isinstance(raw_cat, (list, tuple)):
            category = [str(c) for c in raw_cat if c is not None]
        elif raw_cat is None:
            category = None
        else:
            category = [str(raw_cat)]

        counterparties = tx.get("counterparties") if isinstance(tx.get("counterparties"), list) else []
        location = tx.get("location") if isinstance(tx.get("location"), dict) else None

        merchant_name = tx.get("merchant_name") or None
        name = tx.get("name") or None
        personal_finance_category = tx.get("personal_finance_category")

        category_str = "/".join(category) if category else None
        parts = [p for p in (merchant_name, name, category_str) if p]
        description = " | ".join(parts) if parts else ""

        return {
            "amount": amount,
            "iso_currency_code": tx.get("iso_currency_code") or "",
            "date": tx.get("date") or "",
            "category": category,
            "counterparties": counterparties,
            "location": location,
            "merchant_name": merchant_name,
            "name": name,
            "description": description,
            "personal_finance_category": personal_finance_category,
            "payment_channel": tx.get("payment_channel") or "",
        }

    async def run_account_suggestion_for_tx(self, tx_data: dict, entity_uuid: str, user_uuid: str):
        """
        Run the `account_suggestion_agent` for a single transaction.

        - `tx_data` should be already trimmed (or can be passed raw and trimmed before calling).
        - Returns the agent final_output on success, or None on failure.
        """
        transaction_model = PlaidTransaction(**tx_data)
        context_instance = await LocalContext.build(
            user_uuid=user_uuid, 
            entity_uuid=entity_uuid, 
            transaction=transaction_model
        )
        try:
            result = await self.run_agent(
                "account_suggestion_agent",
                json.dumps({"transaction": tx_data}),
                context=context_instance,
                run_config=RunConfig(tracing_disabled=True, workflow_name="account_suggester"),
            )
            return result.final_output
        except Exception as e:
            logger.exception("Account suggestion agent failed: %s", e)
            return None

    async def run_account_suggestion_on_sample_file(self, entity_uuid: str, user_uuid: str):
        """
        Convenience helper that loads the repository sample `transactions.json` (used by other helpers)
        and runs the account suggestion agent on the chosen sample record.

        The path mirrors the original helper's relative resolution:
            Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
        """
        path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
        with open(path, "r", encoding="utf-8") as f:
            transactions = json.load(f)

        # The original helper used the 8th item in "added"
        tx_data = transactions.get("added")[8] or []
        trimmed_tx = await self.trim_tx_for_llm(tx_data)
        return await self.run_account_suggestion_for_tx(trimmed_tx, entity_uuid, user_uuid)

    def run_account_suggestion_for_tx_sync(self, tx_data: dict, entity_uuid: str, user_uuid: str):
        """
        Synchronous convenience wrapper around `run_account_suggestion_for_tx`.
        Useful for quick scripts or tests that don't already run in an event loop.
        """
        return asyncio.run(self.run_account_suggestion_for_tx(tx_data, entity_uuid, user_uuid))

    # -------------------------
    # Rule creation helpers
    # -------------------------
    async def run_rule_creation_for_known_account(
        self,
        tx_data: dict,
        target_account_index: int,
        entity_uuid: str,
        user_uuid: str
    ):
        """
        Run the `rule_creation_for_known_account_agent` for a single transaction
        and a pre-determined target account.

        - `tx_data`: The transaction data to base the rule on.
        - `target_account_index`: The index of the account the rule should point to.
        - `entity_uuid`: The entity context for the run.
        - `user_uuid`: The user context for the run.
        - Returns the agent's final_output on success, or None on failure.
        """
        try:
            # 1. Prepare inputs for the agent
            transaction_model = PlaidTransaction(**tx_data)
            agent_input = {
                "transaction": transaction_model.model_dump(),
                "target_account_index": target_account_index
            }
            
            # 2. Build the context, which asynchronously fetches the Chart of Accounts
            context_instance = await LocalContext.build(
                user_uuid=user_uuid,
                entity_uuid=entity_uuid,
                transaction=transaction_model
            )

            # 3. Run the agent via the generic runner
            result = await self.run_agent(
                "rule_creation_for_known_account_agent",
                json.dumps(agent_input),
                context=context_instance,
                run_config=RunConfig(tracing_disabled=True, workflow_name="rule_creator_known_account"),
            )
            return result.final_output
        except Exception as e:
            logger.exception("Rule creation for known account agent failed: %s", e)
            return None

    def run_rule_creation_for_known_account_sync(
        self,
        tx_data: dict,
        target_account_index: int,
        entity_uuid: str,
        user_uuid: str
    ):
        """
        Synchronous convenience wrapper for `run_rule_creation_for_known_account`.
        """
        return asyncio.run(
            self.run_rule_creation_for_known_account(
                tx_data,
                target_account_index,
                entity_uuid,
                user_uuid
            )
        )

# from ledger.bookkeeping_agents.plaid_agent_manager import PlaidAgentManager
# import asyncio

# user_uuid = '1a2bf997-e7a5-417d-9195-7e813017480e'
# entity_uuid = '1fb9f680-971f-467b-8c01-858181a279fe'

# mgr = PlaidAgentManager()
# result = asyncio.run(mgr.run_account_suggestion_on_sample_file(
#     entity_uuid=entity_uuid, user_uuid=user_uuid))
# print(result)


# TEST LOCALCONTEXT.BUILD
# from ledger.bookkeeping_agents.types import *
# from ledger.bookkeeping_agents.tools import find_similar_accounts
# import asyncio
# import ledger.bookkeeping_agents.embeddings as embeddings_module
# importlib.reload(embeddings_module)
# from ledger.bookkeeping_agents.embeddings import embeddings_manager

# user_uuid = '1a2bf997-e7a5-417d-9195-7e813017480e'
# entity_uuid = '1fb9f680-971f-467b-8c01-858181a279fe'
# wrapper = asyncio.run(LocalContext.build(user_uuid=user_uuid, entity_uuid=entity_uuid))
# args = AccountProposalType(
#     name="Interest Income",
#     role="in_interest",
#     balance_type="credit",
#     confidence=1.0,
#     justification="This is a test justification"
# )
# result = asyncio.run(find_similar_accounts(wrapper, args))
# print(result)
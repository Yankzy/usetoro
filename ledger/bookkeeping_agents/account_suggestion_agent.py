import re
from decimal import Decimal, InvalidOperation
from pathlib import Path
import json
from django.db import transaction
from ledger.models import (
    ChartOfAccountModel, 
    TransactionModel, 
    RuleAuditLog, 
    EntityModel,
    JournalEntryModel,
)
import logging
from agents import Agent, Runner, RunConfig
import unicodedata
from decimal import Decimal, InvalidOperation
from pathlib import Path
import random
from ledger.io.roles import EXPENSE_OPERATIONAL, INCOME_OPERATIONAL
from ledger.models.accounts import DEBIT, CREDIT
from ledger.bookkeeping_agents.tools import LocalContext, PlaidTransaction
from asgiref.sync import sync_to_async
from .plaid_agents import account_suggestion_agent


logger = logging.getLogger(__name__)

async def trim_tx_for_llm(tx: dict) -> dict:
    """
    Return a minimal transaction dict containing only the fields the LLM should use:
    - amount, category (list), counterparties (list), location (dict),
      merchant_name, name, description, personal_finance_category

    Also include fields required by PlaidTransaction: iso_currency_code,
    date, payment_channel (with safe defaults).
    """
    # amount -> float
    amt = tx.get("amount", 0.0)
    try:
        amount = float(amt)
    except (TypeError, ValueError):
        amount = 0.0

    # category -> normalized list or None
    raw_cat = tx.get("category")
    if isinstance(raw_cat, (list, tuple)):
        category = [str(c) for c in raw_cat if c is not None]
    elif raw_cat is None:
        category = None
    else:
        category = [str(raw_cat)]

    # counterparties and location (ensure counterparties is a list for the model)
    counterparties = tx.get("counterparties") if isinstance(tx.get("counterparties"), list) else []
    location = tx.get("location") if isinstance(tx.get("location"), dict) else None

    merchant_name = tx.get("merchant_name") or None
    name = tx.get("name") or None
    personal_finance_category = tx.get("personal_finance_category")

    # build category string for description
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



async def _call_account_suggestion_agent(tx_data: dict, entity_uuid: str, user_uuid: str):
    transaction_model = PlaidTransaction(**tx_data)
    context_instance = LocalContext(
        user_uuid=user_uuid,
        entity_uuid=entity_uuid,
        transaction=transaction_model
    )
    try:
        context = await Runner.run(
            account_suggestion_agent,
            input=json.dumps({"transaction": tx_data}),
            run_config=RunConfig(tracing_disabled=True, workflow_name="account_suggester"),
            context=context_instance
        )
        return context.final_output
    except Exception as e:
        logger.exception("Account suggestion agent failed: %s", e)
        # Push a notification to the user's AI chat channel
        try:
            from asgiref.sync import async_to_sync
            from channels.layers import get_channel_layer
            from datetime import datetime, timezone
            import uuid
            from assistant.tasks import save_ai_chat
            channel_layer = get_channel_layer()
            if channel_layer:
                # Pre-create a chat session with an assistant message
                session_id = str(uuid.uuid4())
                assistant_text = (
                    f"I noticed an error while running 'account_suggestion_agent'. "
                    f"Details: {str(e)}. "
                    f"Let's resolve this—could you confirm the inputs or context for that step?"
                )
                save_ai_chat.delay(
                    module_id="bookkeeping",
                    session_id=session_id,
                    role="assistant",
                    content=assistant_text,
                    user_uuid=uuid.UUID(user_uuid),
                    entity_for_session=None,
                )
                async_to_sync(channel_layer.group_send)(
                    f"tool_notifications_{user_uuid}",
                    {
                        "type": "tool_notifications",
                        "title": "Account suggestion agent failed",
                        "message": str(e),
                        "tool_name": "account_suggestion_agent",
                        "timestamp": datetime.now(timezone.utc).isoformat(),
                        "session_id": session_id,
                        "meta": {"stage": "agent_run"},
                    },
                )
        except Exception:
            pass
        return None

async def run_account_suggestion_agent_on_plaid_transactions(entity_uuid: str, user_uuid: str):
    path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
    with open(path, "r", encoding="utf-8") as f:
        transactions = json.load(f)

    tx_data = transactions.get("added")[7] or []
    # trimmed tx_data
    trimmed_tx = await trim_tx_for_llm(tx_data)
    return await _call_account_suggestion_agent(trimmed_tx, entity_uuid, user_uuid)


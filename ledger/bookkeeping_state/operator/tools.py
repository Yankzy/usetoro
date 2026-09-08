"""
Typed tool definitions and dispatcher for the Bookkeeping Operator.

Exposes exactly 22 typed tools against BookkeepingWorkbench:
- 14 read tools
- 1 execution tool (run_bookkeeping)
- 7 mutation tools

Tools are serialized as OpenAI Responses API function tools:
{"type": "function", "name": ..., "description": ..., "parameters": ...}
"""

from __future__ import annotations

import json
from typing import Any, Callable

from .workbench import BookkeepingWorkbench


OPERATOR_TOOLS: list[dict[str, Any]] = [
    # -------------------------------------------------------------------------
    # Read Tools (14)
    # -------------------------------------------------------------------------
    {
        "type": "function",
        "name": "get_state",
        "description": "Get current bookkeeping machine state summary (revision, counters, remaining amounts, holds).",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_bank_items",
        "description": "List bank transactions in state. Optionally filter by status ('RECONCILED', 'UNRECONCILED', 'PARTIALLY_RECONCILED').",
        "parameters": {
            "type": "object",
            "properties": {
                "status": {
                    "type": "string",
                    "enum": ["RECONCILED", "UNRECONCILED", "PARTIALLY_RECONCILED"],
                    "description": "Optional filter by status",
                }
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_book_items",
        "description": "List book items (invoices, bills) in state. Optionally filter by status ('RECONCILED', 'UNRECONCILED', 'PARTIALLY_RECONCILED').",
        "parameters": {
            "type": "object",
            "properties": {
                "status": {
                    "type": "string",
                    "enum": ["RECONCILED", "UNRECONCILED", "PARTIALLY_RECONCILED"],
                    "description": "Optional filter by status",
                }
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_remaining_balances",
        "description": "Get remaining unreconciled balances across bank and book items, including overall totals and itemized breakdowns.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_unresolved",
        "description": "List all items needing attention: unreconciled bank/book items, unrouted items, unclassified items, and review candidates.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_review_candidates",
        "description": "List reconciliation candidates awaiting review, including hypotheses held by policy (e.g. inferred allocations).",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_reconciliations",
        "description": "List active durable reconciliations with allocations and semantic scores. Optionally filter by bank_item_id or book_item_id.",
        "parameters": {
            "type": "object",
            "properties": {
                "bank_item_id": {
                    "type": "string",
                    "description": "Filter reconciliations containing this bank item ID",
                },
                "book_item_id": {
                    "type": "string",
                    "description": "Filter reconciliations containing this book item ID",
                },
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_routes",
        "description": "List active durable routing decisions per book item. Optionally filter by book_item_id.",
        "parameters": {
            "type": "object",
            "properties": {
                "book_item_id": {
                    "type": "string",
                    "description": "Filter by book item ID",
                }
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "list_classifications",
        "description": "List active durable account classifications per book item. Optionally filter by book_item_id.",
        "parameters": {
            "type": "object",
            "properties": {
                "book_item_id": {
                    "type": "string",
                    "description": "Filter by book item ID",
                }
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_evidence",
        "description": "List durable evidence assertions (supporting documents, emails). Optionally filter by book_item_id.",
        "parameters": {
            "type": "object",
            "properties": {
                "book_item_id": {
                    "type": "string",
                    "description": "Filter evidence by book item ID",
                }
            },
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_history",
        "description": "Get repository snapshot revision history and durable artifact counts.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_policy",
        "description": "Get active bookkeeping policy settings (auto-reconcile rules, thresholds).",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_holds",
        "description": "List active processing holds currently applied on items or accounts.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },
    {
        "type": "function",
        "name": "get_provider_issues",
        "description": "List provider degradation issues, rate limits, or warnings recorded by the session.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },

    # -------------------------------------------------------------------------
    # Execution Tool (1)
    # -------------------------------------------------------------------------
    {
        "type": "function",
        "name": "run_bookkeeping",
        "description": "Execute the authoritative bookkeeping session (routing, transitions, reconciliation) and return summary, review candidates, and execution plan.",
        "parameters": {
            "type": "object",
            "properties": {},
            "required": [],
        },
    },

    # -------------------------------------------------------------------------
    # Mutation Tools (7)
    # -------------------------------------------------------------------------
    {
        "type": "function",
        "name": "add_bank_item",
        "description": "Add a new bank transaction to the source ledger.",
        "parameters": {
            "type": "object",
            "properties": {
                "bank_item_id": {
                    "type": "string",
                    "description": "Unique identifier for the bank item (e.g. 'txn_101')",
                },
                "amount": {
                    "type": "string",
                    "description": "Amount as decimal string (e.g. '1500.00' or '-250.00')",
                },
                "currency": {
                    "type": "string",
                    "description": "Currency code (e.g. 'USD', 'EUR')",
                },
                "date": {
                    "type": "string",
                    "description": "ISO date string (e.g. '2026-03-15')",
                },
                "counterparty": {
                    "type": "string",
                    "description": "Optional name of the counterparty/payer/payee",
                },
                "description": {
                    "type": "string",
                    "description": "Optional transaction description or narrative",
                },
                "reference": {
                    "type": "string",
                    "description": "Optional bank reference or check number",
                },
            },
            "required": ["bank_item_id", "amount", "currency", "date"],
        },
    },
    {
        "type": "function",
        "name": "add_book_item",
        "description": "Add a new book item (invoice, bill) to the source ledger.",
        "parameters": {
            "type": "object",
            "properties": {
                "book_item_id": {
                    "type": "string",
                    "description": "Unique identifier for the book item (e.g. 'inv_101')",
                },
                "amount": {
                    "type": "string",
                    "description": "Amount as decimal string (e.g. '1500.00')",
                },
                "currency": {
                    "type": "string",
                    "description": "Currency code (e.g. 'USD', 'EUR')",
                },
                "date": {
                    "type": "string",
                    "description": "ISO date string (e.g. '2026-03-10')",
                },
                "item_type": {
                    "type": "string",
                    "enum": ["INVOICE", "BILL", "JOURNAL_ENTRY", "TRANSFER"],
                    "description": "Type of book item (defaults to 'INVOICE')",
                },
                "counterparty": {
                    "type": "string",
                    "description": "Optional customer or vendor name",
                },
                "description": {
                    "type": "string",
                    "description": "Optional item description",
                },
                "reference": {
                    "type": "string",
                    "description": "Optional invoice number or PO reference",
                },
            },
            "required": ["book_item_id", "amount", "currency", "date"],
        },
    },
    {
        "type": "function",
        "name": "add_counterparty",
        "description": "Add a counterparty master record with known aliases.",
        "parameters": {
            "type": "object",
            "properties": {
                "counterparty_id": {
                    "type": "string",
                    "description": "Unique ID for the counterparty",
                },
                "name": {
                    "type": "string",
                    "description": "Canonical legal or display name",
                },
                "counterparty_type": {
                    "type": "string",
                    "enum": ["CUSTOMER", "VENDOR", "INTERNAL"],
                    "description": "Counterparty type (defaults to 'CUSTOMER')",
                },
                "aliases": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Optional list of alias strings",
                },
            },
            "required": ["counterparty_id", "name"],
        },
    },
    {
        "type": "function",
        "name": "assert_book_item_evidence",
        "description": "Assert a durable evidence record supporting a book item (email, remittance advice, document).",
        "parameters": {
            "type": "object",
            "properties": {
                "evidence_id": {
                    "type": "string",
                    "description": "Unique ID for the evidence assertion",
                },
                "book_item_id": {
                    "type": "string",
                    "description": "ID of the book item this evidence pertains to",
                },
                "evidence_type": {
                    "type": "string",
                    "description": "Type of evidence (e.g. 'EMAIL_THREAD', 'REMITTANCE_ADVICE', 'DOCUMENT')",
                },
                "raw_text": {
                    "type": "string",
                    "description": "Text body or excerpt of the evidence",
                },
                "confidence": {
                    "type": "number",
                    "description": "Confidence score 0.0-1.0 (defaults to 1.0)",
                },
                "tags": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Optional categorization tags",
                },
                "source": {
                    "type": "string",
                    "description": "Source of evidence (defaults to 'OPERATOR')",
                },
            },
            "required": ["evidence_id", "book_item_id", "evidence_type", "raw_text"],
        },
    },
    {
        "type": "function",
        "name": "invalidate_evidence",
        "description": "Invalidate an existing evidence assertion.",
        "parameters": {
            "type": "object",
            "properties": {
                "evidence_id": {
                    "type": "string",
                    "description": "ID of the evidence assertion to invalidate",
                },
                "reason": {
                    "type": "string",
                    "description": "Reason for invalidating the evidence",
                },
            },
            "required": ["evidence_id", "reason"],
        },
    },
    {
        "type": "function",
        "name": "invalidate_reconciliation",
        "description": "Invalidate an existing durable reconciliation.",
        "parameters": {
            "type": "object",
            "properties": {
                "reconciliation_id": {
                    "type": "string",
                    "description": "ID of the reconciliation to invalidate",
                },
                "reason": {
                    "type": "string",
                    "description": "Reason for invalidating the reconciliation",
                },
            },
            "required": ["reconciliation_id", "reason"],
        },
    },
    {
        "type": "function",
        "name": "set_auto_reconcile_unique_inferred_allocation",
        "description": "Configure policy for auto-reconciliation of unique inferred allocations (True enables auto-reconcile, False holds them for review).",
        "parameters": {
            "type": "object",
            "properties": {
                "enabled": {
                    "type": "boolean",
                    "description": "Whether to auto-reconcile unique inferred allocations",
                },
            },
            "required": ["enabled"],
        },
    },
]


_DISPATCHER: dict[str, Callable[[BookkeepingWorkbench, dict[str, Any]], Any]] = {
    "get_state": lambda wb, args: wb.get_state(),
    "list_bank_items": lambda wb, args: wb.list_bank_items(status=args.get("status")),
    "list_book_items": lambda wb, args: wb.list_book_items(status=args.get("status")),
    "get_remaining_balances": lambda wb, args: wb.get_remaining_balances(),
    "list_unresolved": lambda wb, args: wb.list_unresolved(),
    "list_review_candidates": lambda wb, args: wb.list_review_candidates(),
    "list_reconciliations": lambda wb, args: wb.list_reconciliations(
        bank_item_id=args.get("bank_item_id"),
        book_item_id=args.get("book_item_id"),
    ),
    "list_routes": lambda wb, args: wb.list_routes(book_item_id=args.get("book_item_id")),
    "list_classifications": lambda wb, args: wb.list_classifications(
        book_item_id=args.get("book_item_id")
    ),
    "get_evidence": lambda wb, args: wb.get_evidence(book_item_id=args.get("book_item_id")),
    "get_history": lambda wb, args: wb.get_history(),
    "get_policy": lambda wb, args: wb.get_policy(),
    "get_holds": lambda wb, args: wb.get_holds(),
    "get_provider_issues": lambda wb, args: wb.get_provider_issues(),
    "run_bookkeeping": lambda wb, args: wb.run_bookkeeping(),
    "add_bank_item": lambda wb, args: wb.add_bank_item(
        bank_item_id=args["bank_item_id"],
        amount=args["amount"],
        currency=args["currency"],
        date=args["date"],
        counterparty=args.get("counterparty"),
        description=args.get("description"),
        reference=args.get("reference"),
    ),
    "add_book_item": lambda wb, args: wb.add_book_item(
        book_item_id=args["book_item_id"],
        amount=args["amount"],
        currency=args["currency"],
        date=args["date"],
        item_type=args.get("item_type", "INVOICE"),
        counterparty=args.get("counterparty"),
        description=args.get("description"),
        reference=args.get("reference"),
    ),
    "add_counterparty": lambda wb, args: wb.add_counterparty(
        counterparty_id=args["counterparty_id"],
        name=args["name"],
        counterparty_type=args.get("counterparty_type", "CUSTOMER"),
        aliases=args.get("aliases"),
    ),
    "assert_book_item_evidence": lambda wb, args: wb.assert_book_item_evidence(
        evidence_id=args["evidence_id"],
        book_item_id=args["book_item_id"],
        evidence_type=args["evidence_type"],
        raw_text=args["raw_text"],
        confidence=float(args.get("confidence", 1.0)),
        tags=args.get("tags"),
        source=args.get("source", "OPERATOR"),
    ),
    "invalidate_evidence": lambda wb, args: wb.invalidate_evidence(
        evidence_id=args["evidence_id"],
        reason=args["reason"],
    ),
    "invalidate_reconciliation": lambda wb, args: wb.invalidate_reconciliation(
        reconciliation_id=args["reconciliation_id"],
        reason=args["reason"],
    ),
    "set_auto_reconcile_unique_inferred_allocation": lambda wb, args: wb.set_auto_reconcile_unique_inferred_allocation(
        enabled=bool(args["enabled"]),
    ),
}


def execute_operator_tool(
    workbench: BookkeepingWorkbench,
    name: str,
    arguments: dict[str, Any] | str | None,
) -> dict[str, Any]:
    """
    Execute a typed operator tool against the BookkeepingWorkbench.
    Always returns a JSON-serializable dictionary with status 'ok' or 'error'.
    """
    if name not in _DISPATCHER:
        return {
            "status": "error",
            "error": f"Unknown tool: '{name}'. Available tools: {sorted(_DISPATCHER.keys())}",
            "tool": name,
        }

    parsed_args: dict[str, Any] = {}
    if isinstance(arguments, str):
        if not arguments.strip():
            parsed_args = {}
        else:
            try:
                loaded = json.loads(arguments)
                if isinstance(loaded, dict):
                    parsed_args = loaded
                else:
                    return {
                        "status": "error",
                        "error": f"Tool arguments must be a JSON object, got {type(loaded).__name__}",
                        "tool": name,
                    }
            except Exception as e:
                return {
                    "status": "error",
                    "error": f"Malformed JSON arguments: {e}",
                    "tool": name,
                }
    elif isinstance(arguments, dict):
        parsed_args = arguments
    elif arguments is None:
        parsed_args = {}
    else:
        return {
            "status": "error",
            "error": f"Invalid arguments type: {type(arguments).__name__}",
            "tool": name,
        }

    handler = _DISPATCHER[name]
    try:
        result = handler(workbench, parsed_args)
        return {"status": "ok", "result": result}
    except Exception as exc:
        return {
            "status": "error",
            "error": str(exc),
            "tool": name,
        }

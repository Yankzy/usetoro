"""
Real Test Script for python-worker/app/reconciliation_prod/reconciliation_worker.py

This script runs the REAL Reconciliation Worker pipeline end-to-end:
1. Real candidate generation (Bipartite Graph + CP-SAT subset sums)
2. Real OpenAI LLM semantic scoring (via AsyncOpenAI & gpt-5.4-mini / gpt-4o structured outputs)
3. Real CP-SAT global optimization (Lexicographic optimization via Google OR-Tools)

The only thing mocked is NATS transport (SimulatedNatsMsg and nats_client.publish interceptor)
because the NATS container is currently offline.

Usage:
    python test_reconciliation_worker.py
    # or
    python python-worker/app/reconciliation_prod/test_reconciliation_worker.py
"""

import asyncio
import json
import os
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional
from unittest.mock import patch

# ---------------------------------------------------------------------
# Path and Environment Setup
# ---------------------------------------------------------------------
current_dir = Path(__file__).resolve().parent
app_dir = current_dir.parent
worker_root = app_dir.parent
repo_root = worker_root.parent

for p in [str(repo_root), str(worker_root), str(app_dir), str(current_dir)]:
    if p not in sys.path:
        sys.path.insert(0, p)

def load_dotenv_if_needed():
    """Loads OPENAI_API_KEY from .env or container/.env if not present in os.environ."""
    if os.environ.get("OPENAI_API_KEY"):
        return

    candidate_env_paths = [
        repo_root / ".env",
        repo_root / "container" / ".env",
        worker_root / ".env",
    ]

    for env_path in candidate_env_paths:
        if env_path.is_file():
            try:
                with open(env_path, "r", encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line and not line.startswith("#") and "=" in line:
                            k, v = line.split("=", 1)
                            k = k.strip()
                            v = v.strip().strip("'\"")
                            if k == "OPENAI_API_KEY" and v:
                                os.environ["OPENAI_API_KEY"] = v
                                print(f"🔑 Loaded OPENAI_API_KEY from {env_path}")
                                return
            except Exception as e:
                print(f"⚠️ Failed to read {env_path}: {e}")

load_dotenv_if_needed()

from reconciliation_prod.reconciliation_worker import handler, get_openai_client


# ---------------------------------------------------------------------
# Simulated NATS Message (In-Memory)
# ---------------------------------------------------------------------
class SimulatedNatsMsg:
    """
    Simulates a NATS Msg object in-memory without requiring a running NATS server.
    """

    def __init__(
        self,
        subject: str,
        data: bytes,
        reply: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
    ):
        self.subject = subject
        self.data = data
        self.reply = reply
        self.headers = headers or {}
        self.acked = False

    async def ack(self):
        self.acked = True


# ---------------------------------------------------------------------
# Scenario Execution Helper
# ---------------------------------------------------------------------
async def execute_scenario(
    scenario_title: str,
    payload_data: Dict[str, Any],
    reply_subject: str = "worker.inbox.reconciliation.reply",
) -> Dict[str, Any]:
    """
    Executes a reconciliation scenario using the real OpenAI client and CP-SAT solver,
    intercepting the published NATS response.
    """
    print("\n" + "=" * 80)
    print(f"🧪 [SCENARIO] {scenario_title}")
    print("=" * 80)

    envelope = {
        "type": "REQUEST",
        "reply_subject": reply_subject,
        "payload": payload_data,
    }

    raw_msg_data = json.dumps(envelope).encode("utf-8")
    fake_msg = SimulatedNatsMsg(
        subject="worker.inbox.reconciliation",
        data=raw_msg_data,
        reply=reply_subject,
    )

    published_messages: List[tuple[str, dict]] = []

    async def mock_publish(subject: str, payload: dict):
        published_messages.append((subject, payload))
        print(f"📥 [NATS INTERCEPTOR] Message successfully intercepted for subject '{subject}'")

    # Use the real OpenAI client from reconciliation_worker
    llm_client = get_openai_client()

    with patch("app.nats_client.publish", side_effect=mock_publish):
        await handler(fake_msg, llm_client)

    if not fake_msg.acked:
        print("⚠️ Warning: Simulated NATS message was not ACKed!")

    if not published_messages:
        raise RuntimeError("No response was published to reply subject!")

    pub_subject, pub_envelope = published_messages[0]
    raw_payload = pub_envelope.get("payload")
    if isinstance(raw_payload, str):
        try:
            parsed_payload = json.loads(raw_payload)
        except Exception:
            parsed_payload = raw_payload
    else:
        parsed_payload = raw_payload

    print("\n📊 [RESULTS SUMMARY]")
    print(f"   • Published Envelope Type: {pub_envelope.get('type')}")
    if isinstance(parsed_payload, dict):
        print(f"   • Solver Status:           {parsed_payload.get('status')}")
        print(f"   • Objective Value:         {parsed_payload.get('objective_value')}")
        print(f"   • Selected Hypotheses:     {len(parsed_payload.get('selected_hypotheses', []))}")
        for h in parsed_payload.get("selected_hypotheses", []):
            print(f"       -> Hypothesis {h.get('hypothesis_id')}: utility={h.get('utility')}, reason={h.get('reason')}")
            for b in h.get("bank_allocations", []):
                print(f"          Bank {b.get('bank_item_id')}: {b.get('amount_units')} units")
            for j in h.get("book_allocations", []):
                print(f"          Book {j.get('book_item_id')}: {j.get('amount_units')} units")
        print(f"   • Unresolved Bank Items:   {len(parsed_payload.get('unresolved_bank_items', []))}")
        for u in parsed_payload.get("unresolved_bank_items", []):
            print(f"       -> Unresolved Bank {u.get('bank_item_id')}: reason={u.get('reason')}")
        print(f"   • Book Residuals:          {len(parsed_payload.get('book_residuals', []))}")
        for r in parsed_payload.get("book_residuals", []):
            print(f"       -> Book {r.get('book_item_id')}: consumed={r.get('consumed_amount_units')}, remaining={r.get('remaining_amount_units')}")
    else:
        print(f"   • Raw Response: {parsed_payload}")

    return {
        "envelope": pub_envelope,
        "payload": parsed_payload,
    }


# ---------------------------------------------------------------------
# Test Scenarios
# ---------------------------------------------------------------------
async def test_scenario_1_exact_match():
    """
    Scenario 1: Real 1:1 Exact Match.
    Bank receives 5,000.00 MAD from Atlas Corp with description referencing INV-2026-001.
    Book item has 5,000.00 MAD open invoice for Atlas Corp.
    """
    payload = {
        "account_id": "ACC_BANK_MAD_01",
        "currency": "MAD",
        "bank_items": [
            {
                "id": "BANK-001",
                "source_type": "BANK_STATEMENT_LINE",
                "date": "2026-08-01",
                "amount_units": "50000000",  # 5,000.0000 MAD
                "direction": "BANK_INFLOW",
                "currency": "MAD",
                "description": "VIR SEPA ATLAS CORP INV-2026-001 REGLEMENT FACTURE",
                "reference": "DEP-7701",
            }
        ],
        "book_items": [
            {
                "id": "BOOK-001",
                "source_type": "POSTED_BOOK_ITEM",
                "origin_period": "2026-08",
                "date": "2026-08-01",
                "remaining_amount_units": "50000000",  # 5,000.0000 MAD
                "direction": "BOOK_BANK_DEBIT",
                "currency": "MAD",
                "counterparty_id": "ATLAS_CORP",
                "reference": "INV-2026-001",
                "provenance_refs": ["INV-2026-001"],
            }
        ],
        "evidence_text": "Client Atlas Corp confirmed bank wire for invoice INV-2026-001 totaling 5000 MAD.",
    }

    res = await execute_scenario("1:1 Exact Match with Real OpenAI LLM & CP-SAT", payload)
    assert res["envelope"]["type"] == "INFORM"
    assert res["payload"]["status"] in ("OPTIMAL", "FEASIBLE")
    assert len(res["payload"]["selected_hypotheses"]) == 1
    assert len(res["payload"]["unresolved_bank_items"]) == 0
    print("✅ Scenario 1 Complete & Verified!")


async def test_scenario_2_grouped_match():
    """
    Scenario 2: Real 1-to-Many Grouped Match.
    Bank receives 3,000.00 MAD from Beta SARL.
    Book has two invoices: INV-002A (1,000.00 MAD) and INV-002B (2,000.00 MAD).
    """
    payload = {
        "account_id": "ACC_BANK_MAD_01",
        "currency": "MAD",
        "bank_items": [
            {
                "id": "BANK-002",
                "source_type": "BANK_STATEMENT_LINE",
                "date": "2026-08-02",
                "amount_units": "30000000",  # 3,000.0000 MAD
                "direction": "BANK_INFLOW",
                "currency": "MAD",
                "description": "VIR GLOBAL BETA SARL INVS 002A 002B",
                "reference": "DEP-7702",
            }
        ],
        "book_items": [
            {
                "id": "BOOK-002A",
                "source_type": "POSTED_BOOK_ITEM",
                "origin_period": "2026-08",
                "date": "2026-08-02",
                "remaining_amount_units": "10000000",  # 1,000.0000 MAD
                "direction": "BOOK_BANK_DEBIT",
                "currency": "MAD",
                "counterparty_id": "BETA_SARL",
                "reference": "INV-2026-002A",
                "provenance_refs": ["INV-2026-002A"],
            },
            {
                "id": "BOOK-002B",
                "source_type": "POSTED_BOOK_ITEM",
                "origin_period": "2026-08",
                "date": "2026-08-02",
                "remaining_amount_units": "20000000",  # 2,000.0000 MAD
                "direction": "BOOK_BANK_DEBIT",
                "currency": "MAD",
                "counterparty_id": "BETA_SARL",
                "reference": "INV-2026-002B",
                "provenance_refs": ["INV-2026-002B"],
            },
        ],
        "evidence_text": "Single wire transfer of 3,000 MAD settled two open invoices: INV-2026-002A (1,000 MAD) and INV-2026-002B (2,000 MAD).",
    }

    res = await execute_scenario("1:Many Grouped Match with Real OpenAI LLM & CP-SAT", payload)
    assert res["envelope"]["type"] == "INFORM"
    assert res["payload"]["status"] in ("OPTIMAL", "FEASIBLE")
    assert len(res["payload"]["selected_hypotheses"]) == 1
    assert len(res["payload"]["unresolved_bank_items"]) == 0
    print("✅ Scenario 2 Complete & Verified!")


async def test_scenario_3_unresolved():
    """
    Scenario 3: Unresolved Bank Fee.
    Bank line has bank fees (150.00 MAD) with no corresponding book items.
    CP-SAT should recognize this bank item as unresolved.
    """
    payload = {
        "account_id": "ACC_BANK_MAD_01",
        "currency": "MAD",
        "bank_items": [
            {
                "id": "BANK-UNMATCHED-999",
                "source_type": "BANK_STATEMENT_LINE",
                "date": "2026-08-03",
                "amount_units": "1500000",  # 150.0000 MAD
                "direction": "BANK_OUTFLOW",
                "currency": "MAD",
                "description": "COMMISSIONS ET FRAIS BANCAIRES TENUE DE COMPTE",
                "reference": "FEE-999",
            }
        ],
        "book_items": [],
        "evidence_text": "Monthly bank account maintenance fee debited by the bank.",
    }

    res = await execute_scenario("Unmatched Bank Movement (Expected Unresolved)", payload)
    assert res["envelope"]["type"] == "INFORM"
    assert len(res["payload"]["selected_hypotheses"]) == 0
    assert len(res["payload"]["unresolved_bank_items"]) == 1
    assert res["payload"]["unresolved_bank_items"][0]["bank_item_id"] == "BANK-UNMATCHED-999"
    print("✅ Scenario 3 Complete & Verified!")


async def test_scenario_4_error_handling():
    """
    Scenario 4: Malformed payload structure -> error is caught and published as FAILURE envelope.
    """
    print("\n" + "=" * 80)
    print("🧪 [SCENARIO] Error Handling for Malformed Payload")
    print("=" * 80)

    envelope = {
        "type": "REQUEST",
        "reply_subject": "worker.inbox.test.error",
        "payload": "{ invalid json malformed",
    }
    fake_msg = SimulatedNatsMsg(
        subject="worker.inbox.reconciliation",
        data=json.dumps(envelope).encode("utf-8"),
        reply="worker.inbox.test.error",
    )

    published_messages = []

    async def mock_publish(subject: str, payload: dict):
        published_messages.append((subject, payload))
        print(f"📥 [NATS INTERCEPTOR] Error message published to '{subject}'")

    with patch("app.nats_client.publish", side_effect=mock_publish):
        await handler(fake_msg, get_openai_client())

    assert len(published_messages) == 1
    assert published_messages[0][1]["type"] == "FAILURE"
    print("✅ Scenario 4 Complete & Verified!")


async def main():
    api_key = os.environ.get("OPENAI_API_KEY", "")
    if not api_key:
        print("❌ Error: OPENAI_API_KEY is not set. Please set OPENAI_API_KEY in environment or .env file.")
        sys.exit(1)

    print(f"🔑 Using OpenAI API Key: {api_key[:8]}...{api_key[-4:]}")
    print("🚀 Launching Real Reconciliation Worker Test Suite...\n")

    await test_scenario_1_exact_match()
    await test_scenario_2_grouped_match()
    await test_scenario_3_unresolved()
    await test_scenario_4_error_handling()

    print("\n" + "=" * 80)
    print("🎉 ALL REAL RECONCILIATION WORKER TESTS PASSED WITH LIVE OPENAI & CP-SAT!")
    print("=" * 80 + "\n")


if __name__ == "__main__":
    asyncio.run(main())


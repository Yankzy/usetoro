import asyncio
import json
import logging
import re
import sys
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Dict, List

from pydantic import BaseModel

# Adjust paths based on your repository structure
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from agent.reconciliation_agent import get_openai_client
from routing.orchestrator import run_multi_account_pipeline, MultiAccountReconciliationResponse

# Configure logging to highlight phase transitions
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    handlers=[logging.StreamHandler(sys.stdout)]
)
logger = logging.getLogger("ScenarioRunner")


class Scenario(BaseModel):
    name: str
    description: str
    book_items: List[BookItem]
    accounts_bank_items: Dict[str, List[BankItem]]
    account_metadata: Dict[str, str]
    evidence_text: str


def scenario_filename_component(name: str) -> str:
    """Return a portable filename component without changing the display name."""
    return re.sub(r"[^A-Za-z0-9._-]+", "_", name).strip("._") or "scenario"


def build_scenarios() -> List[Scenario]:
    return [
        # ----------------------------------------------------------------------
        # SCENARIO 1: The Date-Proximity Tie-Breaker (Cross-Account Swap Trap)
        # ----------------------------------------------------------------------
        Scenario(
            name="Scenario 1: Cross-Account Date Proximity",
            description=(
                "Two identical 10,000 MAD supplier invoices exist in different months. "
                "Account A has a payment in January, Account B has a payment in February. "
                "The system must route J1 -> Account A and J2 -> Account B without swapping."
            ),
            book_items=[
                BookItem(
                    id="J_JAN_SOMACA",
                    source_type=SourceType.POSTED_BOOK_ITEM,
                    origin_period="2026-01",
                    date=date(2026, 1, 10),
                    remaining_amount_units="100000000",  # 10,000.00 MAD
                    direction=Direction.BOOK_BANK_CREDIT,
                    currency="MAD",
                    reference="INV-SOMACA-01"
                ),
                BookItem(
                    id="J_FEB_SOMACA",
                    source_type=SourceType.POSTED_BOOK_ITEM,
                    origin_period="2026-02",
                    date=date(2026, 2, 10),
                    remaining_amount_units="100000000",  # 10,000.00 MAD
                    direction=Direction.BOOK_BANK_CREDIT,
                    currency="MAD",
                    reference="INV-SOMACA-02"
                )
            ],
            accounts_bank_items={
                "MAIN_CHECKING": [
                    BankItem(
                        id="B_JAN_OUT",
                        date=date(2026, 1, 12),
                        amount_units="100000000",
                        direction=Direction.BANK_OUTFLOW,
                        currency="MAD",
                        description="VIR SOMACA JANVIER"
                    )
                ],
                "SECONDARY_CHECKING": [
                    BankItem(
                        id="B_FEB_OUT",
                        date=date(2026, 2, 14),
                        amount_units="100000000",
                        direction=Direction.BANK_OUTFLOW,
                        currency="MAD",
                        description="VIR SOMACA FEVRIER"
                    )
                ]
            },
            account_metadata={
                "MAIN_CHECKING": "Primary operating bank account used for Q1 supplier payments.",
                "SECONDARY_CHECKING": "Secondary reserve account activated in February for vendor payments."
            },
            evidence_text="January invoice was settled via Main Checking on Jan 12. February invoice was paid from Secondary Checking on Feb 14."
        ),

        # ----------------------------------------------------------------------
        # SCENARIO 2: Grouped Capacity Split vs. Single Line Match
        # ----------------------------------------------------------------------
        Scenario(
            name="Scenario 2: Grouped Subset-Sum Partitioning",
            description=(
                "Two office supplies invoices (2,000 MAD & 3,000 MAD) must be matched. "
                "Account A has a single 5,000 MAD payment line. Account B has individual payments. "
                "The system must test subset-sum feasibility and assign both to Account A."
            ),
            book_items=[
                BookItem(
                    id="J_SUPPLIES_A",
                    source_type=SourceType.POSTED_BOOK_ITEM,
                    origin_period="2026-03",
                    date=date(2026, 3, 5),
                    remaining_amount_units="20000000",  # 2,000 MAD
                    direction=Direction.BOOK_BANK_CREDIT,
                    currency="MAD",
                    reference="FA-MARJANE-01"
                ),
                BookItem(
                    id="J_SUPPLIES_B",
                    source_type=SourceType.POSTED_BOOK_ITEM,
                    origin_period="2026-03",
                    date=date(2026, 3, 5),
                    remaining_amount_units="30000000",  # 3,000 MAD
                    direction=Direction.BOOK_BANK_CREDIT,
                    currency="MAD",
                    reference="FA-MARJANE-02"
                )
            ],
            accounts_bank_items={
                "OPS_ACCOUNT": [
                    BankItem(
                        id="B_GROUPED_5K",
                        date=date(2026, 3, 7),
                        amount_units="50000000",  # 5,000 MAD
                        direction=Direction.BANK_OUTFLOW,
                        currency="MAD",
                        description="VIR GROUPMENT MARJANE ACHATS"
                    )
                ],
                "PETTY_CASH_ACCOUNT": [
                    BankItem(
                        id="B_SMALL_1K",
                        date=date(2026, 3, 7),
                        amount_units="10000000",  # 1,000 MAD
                        direction=Direction.BANK_OUTFLOW,
                        currency="MAD",
                        description="RETRAIT CAISSE"
                    )
                ]
            },
            account_metadata={
                "OPS_ACCOUNT": "Operations account where bulk supplier transfers are issued.",
                "PETTY_CASH_ACCOUNT": "Minor local cash account."
            },
            evidence_text="Both Marjane invoices were consolidated into a single bank transfer of 5,000 MAD out of the Operations account."
        ),

        # ----------------------------------------------------------------------
        # SCENARIO 3: Orphaned Infeasible Invoice Detection
        # ----------------------------------------------------------------------
        Scenario(
            name="Scenario 3: Orphaned / Infeasible Invoice Guardrail",
            description=(
                "An invoice for 500,000 MAD is processed, but no bank account has enough capacity. "
                "Phase 1 should flag it as infeasible, Phase 3 should flag it as unroutable."
            ),
            book_items=[
                BookItem(
                    id="J_CAPEX_EQUIPMENT",
                    source_type=SourceType.POSTED_BOOK_ITEM,
                    origin_period="2026-03",
                    date=date(2026, 3, 1),
                    remaining_amount_units="5000000000",  # 500,000 MAD
                    direction=Direction.BOOK_BANK_CREDIT,
                    currency="MAD",
                    reference="INV-HEAVY-MACHINERY"
                )
            ],
            accounts_bank_items={
                "MAIN_CHECKING": [
                    BankItem(
                        id="B_SMALL_TX",
                        date=date(2026, 3, 2),
                        amount_units="100000000",  # 10,000 MAD
                        direction=Direction.BANK_OUTFLOW,
                        currency="MAD",
                        description="ACHAT DIVERS"
                    )
                ]
            },
            account_metadata={
                "MAIN_CHECKING": "Standard operating account."
            },
            evidence_text="Equipment purchase pending financing."
        )
    ]


async def run_and_report():
    # Reuse the shared client and repository-level credential loading used by
    # the reconciliation agent. This keeps routing and reconciliation on the
    # same configured OpenAI client.
    llm_client = get_openai_client()
    scenarios = build_scenarios()

    print("\n" + "=" * 80)
    print("      MULTI-ACCOUNT ROUTING ENGINE — SCENARIO EXECUTION REPORT")
    print("=" * 80 + "\n")

    for idx, sc in enumerate(scenarios, start=1):
        print(f"[{idx}/{len(scenarios)}] RUNNING: {sc.name}")
        print(f"     Description: {sc.description}")
        print("-" * 80)

        try:
            response: MultiAccountReconciliationResponse = await run_multi_account_pipeline(
                book_items=sc.book_items,
                accounts_bank_items=sc.accounts_bank_items,
                account_metadata=sc.account_metadata,
                evidence_text=sc.evidence_text,
                llm_client=llm_client,
                currency="MAD",
                model_name="gpt-5.6-sol",
                use_optimizer=True
            )

            # --- ROUTING SUMMARY ---
            print("\n  [PHASE 3 ROUTING ASSIGNMENTS]")
            if response.routing_state.assignments:
                for assign in response.routing_state.assignments:
                    print(
                        f"  ├─ Book Item: '{assign.book_item_id}' "
                        f"---> Assigned Account: '{assign.assigned_account_id}' "
                        f"(Score: {assign.utility_score}/1000)"
                    )
                    if assign.rationale:
                        print(f"  │  Rationale: {assign.rationale}")
            else:
                print("  ├─ No assignments made.")

            if response.routing_state.unroutable_book_ids:
                for unassigned_id in response.routing_state.unroutable_book_ids:
                    print(f"  ├─ UNROUTABLE ITEM DETECTED: '{unassigned_id}' (Successfully Isolated)")

            # --- RECONCILIATION SUMMARY ---
            print("\n  [PHASE 4 ISOLATED RECONCILIATION EXECUTION]")
            for acc_res in response.account_results:
                state = acc_res.proposed_state
                print(f"  ├─ Account '{acc_res.account_id}':")
                print(f"  │  ├─ Routed Book Items: {acc_res.routed_book_item_ids}")
                print(f"  │  ├─ Matches Found: {len(state.matches)}")
                for group in state.matches:
                    print(f"  │  │  └─ Match Group: {group.group_id}")
                    for alloc in group.bank_allocations:
                        print(
                            f"  │  │     ├─ Bank Item: {alloc.bank_item_id} | "
                            f"Allocated Amount: {alloc.amount_units}"
                        )
                    for alloc in group.book_allocations:
                        print(
                            f"  │  │     └─ Book Item: {alloc.book_item_id} | "
                            f"Allocated Amount: {alloc.amount_units}"
                        )
                print(f"  │  └─ Unresolved Bank IDs: {state.unresolved_bank_ids}")
            
            # --- SAVE ROUTING EVAL TO JSON ---
            runs_dir = Path("logs/runs")
            runs_dir.mkdir(parents=True, exist_ok=True)
            timestamp = datetime.now(UTC).strftime("%Y%m%d_%H%M%S")
            report_path = runs_dir / (
                f"routing_report_{scenario_filename_component(sc.name)}_{timestamp}.json"
            )
            
            with open(report_path, "w") as f:
                json.dump(response.model_dump(), f, indent=2, default=str)
            print(f"\n  [✔] Saved JSON report to: {report_path}")
            
        except Exception as e:
            logger.error(f"Scenario failed with error: {str(e)}", exc_info=True)

        print("\n" + "=" * 80 + "\n")


if __name__ == "__main__":
    asyncio.run(run_and_report())

import asyncio
import sys
from collections.abc import Awaitable, Callable

from evaluation.runner import run_scenario_evaluation
from scenarios.atlas_construction.scenario import (
    atlas_const_bank_items as construction_bank_items, 
    atlas_const_book_items as construction_book_items, 
    atlas_const_evidence as construction_evidence, 
    atlas_const_ground_truth as construction_ground_truth
)
from scenarios.atlas_station.scenario import (
    atlas_bank_items as station_bank_items,
    atlas_book_items as station_book_items,
    atlas_evidence as station_evidence,
    atlas_ground_truth as station_ground_truth,
)
from scenarios.maghreb_distribution.scenario import (
    maghreb_bank_items,
    maghreb_book_items,
    maghreb_evidence,
    maghreb_ground_truth,
)
from scenarios.atlas_office.scenario import (
    atlas_office_bank_items, 
    atlas_office_book_items, 
    atlas_office_evidence, 
    atlas_office_ground_truth
)
from scenarios.complex_settlements.scenario import (
    complex_settlements_bank_items,
    complex_settlements_book_items,
    complex_settlements_evidence,
    complex_settlements_ground_truth,
)

RUNS = 2
MODEL_NAME = "gpt-5.6-sol"


async def run_atlas_construction() -> None:
    await run_scenario_evaluation(
        scenario_id="atlas_construction_sarl", currency="MAD",
        bank_items=construction_bank_items, book_items=construction_book_items,
        evidence=construction_evidence, truth=construction_ground_truth,
        runs=RUNS, model_name=MODEL_NAME,
    )


async def run_atlas_station() -> None:
    await run_scenario_evaluation(
        scenario_id="atlas_station_sarl", currency="MAD",
        bank_items=station_bank_items, book_items=station_book_items,
        evidence=station_evidence, truth=station_ground_truth,
        runs=RUNS, model_name=MODEL_NAME,
    )


async def run_maghreb_distribution() -> None:
    await run_scenario_evaluation(
        scenario_id="maghreb_distribution_sarl", currency="MAD",
        bank_items=maghreb_bank_items, book_items=maghreb_book_items,
        evidence=maghreb_evidence, truth=maghreb_ground_truth,
        runs=RUNS, model_name=MODEL_NAME,
    )


async def run_atlas_office() -> None:
    await run_scenario_evaluation(
        scenario_id="atlas_office_sarl", currency="MAD",
        bank_items=atlas_office_bank_items, book_items=atlas_office_book_items,
        evidence=atlas_office_evidence, truth=atlas_office_ground_truth,
        runs=RUNS, model_name=MODEL_NAME,
    )


async def run_complex_settlements() -> None:
    await run_scenario_evaluation(
        scenario_id="complex_settlements", currency="MAD",
        bank_items=complex_settlements_bank_items,
        book_items=complex_settlements_book_items,
        evidence=complex_settlements_evidence,
        truth=complex_settlements_ground_truth,
        runs=RUNS, model_name=MODEL_NAME,
    )


SCENARIOS: dict[str, Callable[[], Awaitable[None]]] = {
    "atlas_construction": run_atlas_construction,
    "atlas_station": run_atlas_station,
    "maghreb_distribution": run_maghreb_distribution,
    "atlas_office": run_atlas_office,
    "complex_settlements": run_complex_settlements,
}


def main() -> None:
    from pathlib import Path
    runs_dir = Path("logs/runs")
    if runs_dir.exists():
        for f in runs_dir.glob("eval_report_*.json"):
            try:
                f.unlink()
            except Exception as e:
                print(f"Failed to delete {f}: {e}")

    scenario_id = sys.argv[1] if len(sys.argv) > 1 else None
    
    if scenario_id is not None:
        if scenario_id not in SCENARIOS:
            print(f"Scenario '{scenario_id}' not found. Available: {', '.join(SCENARIOS)}")
            raise SystemExit(2)
        print(f"--- Running Scenario: {scenario_id} ---")
        asyncio.run(SCENARIOS[scenario_id]())
    else:
        print(f"No scenario specified. Running all {len(SCENARIOS)} scenarios sequentially...")
        for s_id, s_func in SCENARIOS.items():
            print(f"\n=== Starting Scenario: {s_id} ===")
            # Use a fresh event loop for each scenario to guarantee isolated contexts
            asyncio.run(s_func())
            print(f"=== Finished Scenario: {s_id} ===\n")

if __name__ == "__main__":
    main()

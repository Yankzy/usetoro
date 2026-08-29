import asyncio
import argparse
from pathlib import Path

from app.pcm_dag_eval.scenarios.all_101_mock_transactions import (
    load_all_scenarios,
    get_inflows_suite,
    get_outflows_suite,
    get_hold_ambiguity_suite,
    get_tax_compliance_suite
)
from app.pcm_dag_eval.evaluation.runner import run_scenario_evaluation

def main():
    parser = argparse.ArgumentParser(description="Run PCM DAG automated evaluations")
    parser.add_argument("--scenario", type=str, default="all_101", 
                        choices=["all_101", "inflows", "outflows", "holds", "tax"],
                        help="The scenario suite to execute")
    parser.add_argument("-edge", "--edge", type=str, default=None,
                        help="Filter scenarios by specific edge_key (e.g. SOCIAL_CONTRIBUTION_PAYMENT_444X)")
    parser.add_argument("--output", type=str, default="logs/runs", help="Output directory for reports")
    args = parser.parse_args()
    
    # Load scenarios
    if args.scenario == "all_101":
        scenarios = load_all_scenarios()
    elif args.scenario == "inflows":
        scenarios = get_inflows_suite()
    elif args.scenario == "outflows":
        scenarios = get_outflows_suite()
    elif args.scenario == "holds":
        scenarios = get_hold_ambiguity_suite()
    elif args.scenario == "tax":
        scenarios = get_tax_compliance_suite()
    else:
        scenarios = load_all_scenarios()

    if args.edge:
        scenarios = [s for s in scenarios if s.edge_key == args.edge]
        if not scenarios:
            print(f"Warning: No scenarios found matching edge '{args.edge}' in suite '{args.scenario}'.")
            return

    runs_dir = Path(__file__).resolve().parents[1] / args.output
    
    print(f"Starting scenario: {args.scenario} with {len(scenarios)} transactions...")
    asyncio.run(run_scenario_evaluation(
        scenarios=scenarios,
        scenario_name=args.scenario if not args.edge else f"{args.scenario}_{args.edge}",
        runs_dir=runs_dir
    ))

if __name__ == "__main__":
    main()

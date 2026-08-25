# test_multi_account_pipeline.py
import asyncio
import os
import openai
from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from routing.orchestrator import run_multi_account_pipeline

async def main():
    llm_client = openai.AsyncOpenAI(api_key=os.getenv("OPENAI_API_KEY"))

    # Account 1: Operating Checking Account
    bank_checking = [
        BankItem(id="B_CHK_1", date=date(2026, 7, 2), amount_units="120000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Loyer Anfa Immobilier")
    ]

    # Account 2: Payroll & Tax Dedicated Account
    bank_payroll = [
        BankItem(id="B_PAY_1", date=date(2026, 7, 18), amount_units="68000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Prelevement Mensuel CNSS")
    ]

    accounts_bank_data = {
        "CHECKING_ACC": bank_checking,
        "PAYROLL_ACC": bank_payroll
    }

    account_metadata = {
        "CHECKING_ACC": "Primary operating bank account used for rent, supplies, and general vendors.",
        "PAYROLL_ACC": "Dedicated bank account strictly used for CNSS taxes, salaries, and social charges."
    }

    # Open Invoices (Unrouted)
    inv_rent = BookItem(id="J_RENT", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 1), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="FA-LOYER")
    inv_cnss = BookItem(id="J_CNSS", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 18), remaining_amount_units="68000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="DECL-CNSS")

    evidence = "Rent is paid from the main operating account. CNSS social taxes are automatically debited from the payroll account."

    response = await run_multi_account_pipeline(
        book_items=[inv_rent, inv_cnss],
        accounts_bank_items=accounts_bank_data,
        account_metadata=account_metadata,
        evidence_text=evidence,
        llm_client=llm_client,
        currency="MAD",
        use_optimizer=True
    )

    print("\n================ ROUTING RESULTS ================")
    for assignment in response.routing_state.assignments:
        print(f"Book Item '{assignment.book_item_id}' ---> Routed to '{assignment.assigned_account_id}' (Utility: {assignment.utility_score})")

    print("\n================ RECONCILIATION RESULTS ================")
    for result in response.account_results:
        print(f"\nAccount '{result.account_id}':")
        print(f"  Matches Found: {len(result.proposed_state.matches)}")
        print(f"  Unresolved Bank IDs: {result.proposed_state.unresolved_bank_ids}")

if __name__ == "__main__":
    asyncio.run(main())
# test_feasibility.py
from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from routing.feasibility import build_feasibility_matrix

# Account A has 10,000 MAD Bank line
account_a_bank = [
    BankItem(id="BA_1", date=date(2026, 7, 1), amount_units="100000000", direction=Direction.BANK_INFLOW, currency="MAD", description="CP_1")
]

# Account B has 5,000 MAD + 5,000 MAD Bank lines
account_b_bank = [
    BankItem(id="BB_1", date=date(2026, 7, 1), amount_units="50000000", direction=Direction.BANK_INFLOW, currency="MAD", description="CP_1"),
    BankItem(id="BB_2", date=date(2026, 7, 2), amount_units="50000000", direction=Direction.BANK_INFLOW, currency="MAD", description="CP_2")
]

# Invoice 1: 10,000 MAD (Feasible in BOTH Account A and Account B via group sum)
inv1 = BookItem(id="J1", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 1), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD")

# Invoice 2: 7,000 MAD (Impossible in Account A, Impossible in Account B)
inv2 = BookItem(id="J2", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 1), remaining_amount_units="70000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD")

accounts_data = {
    "ACCOUNT_A": account_a_bank,
    "ACCOUNT_B": account_b_bank
}

results = build_feasibility_matrix([inv1, inv2], accounts_data)

for node in results:
    print(f"\nInvoice {node.book_item_id} ({node.amount_units} units):")
    for f in node.feasibility_matrix:
        print(f"  Account {f.account_id}: Feasible = {f.is_feasible} | Plausible Bank IDs = {f.plausible_bank_item_ids}")
from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from evaluation.ground_truth import GroundTruth, ExpectedMatchGroup

# ---------------------------------------------------------------------------
# BANK MOVEMENTS (The Observed Side)
# ---------------------------------------------------------------------------
atlas_bank_items = [
    # 1. Straightforward fuel supplier payment
    BankItem(id="B1", date=date(2026, 8, 2), amount_units="1500000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR AFRIQUIA FUEL"),
    
    # 2 & 3. TPE/Card settlement & Batching (B2 matches two slips, B3 matches one pre-batched entry)
    BankItem(id="B2", date=date(2026, 8, 3), amount_units="50000000", direction=Direction.BANK_INFLOW, currency="MAD", description="REMISE TPE CMI 0802"),
    BankItem(id="B3", date=date(2026, 8, 4), amount_units="120000000", direction=Direction.BANK_INFLOW, currency="MAD", description="REMISE TPE CMI 0803"),
    
    # 4. Cash deposit
    BankItem(id="B4", date=date(2026, 8, 5), amount_units="80000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VERSEMENT ESPECES AGENCE"),
    
    # 5. Bank fee
    BankItem(id="B5", date=date(2026, 8, 5), amount_units="1500000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="FRAIS TENUE DE COMPTE"),
    
    # 6. Prior-period outstanding cheque clearing
    BankItem(id="B6", date=date(2026, 8, 6), amount_units="180000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="CHQ ENCAISSE 008741", reference="008741"),
    
    # 7. Duplicate monetary amounts (10,000 MAD each) for different counterparties
    BankItem(id="B7", date=date(2026, 8, 8), amount_units="100000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR MAINTENANCE ASCENSEUR"),
    BankItem(id="B8", date=date(2026, 8, 8), amount_units="100000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR FOURNITURES BUREAU"),
    
    # 8. Unresolved line (Designed HOLD trap)
    BankItem(id="B9", date=date(2026, 8, 10), amount_units="25000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR INCONNU CLIENT X")
]

# ---------------------------------------------------------------------------
# BOOK ITEMS (The Canonical Accounting Side)
# ---------------------------------------------------------------------------
atlas_book_items = [
    # J1 matches B1
    BookItem(id="J1", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 1), remaining_amount_units="1500000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-AFRIQUIA"),
    
    # J2 + J3 match B2 (The TPE batch of multiple slips)
    BookItem(id="J2", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 2), remaining_amount_units="20000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="TPE-SLIP-1"),
    BookItem(id="J3", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 2), remaining_amount_units="30000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="TPE-SLIP-2"),
    
    # J4 matches B3
    BookItem(id="J4", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 3), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="BATCH-0803"),
    
    # J5 matches B4
    BookItem(id="J5", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 5), remaining_amount_units="80000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="CASH-DEP"),
    
    # J6 matches B5
    BookItem(id="J6", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 5), remaining_amount_units="1500000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="FEE-08"),
    
    # O1 (Opening State) matches B6 (The prior-period cheque)
    BookItem(id="O1", source_type=SourceType.OPENING_STATE_ITEM, origin_period="2026-07", date=date(2026, 7, 28), remaining_amount_units="180000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="008741"),
    
    # J7 matches B7, J8 matches B8 (The identical amounts)
    BookItem(id="J7", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 7), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-MAINT", reference="ASCENSEUR"),
    BookItem(id="J8", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 7), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-BUREAU", reference="FOURNITURES"),
]

atlas_evidence = """
1. Supplier AFRIQUIA provides our fuel.
2. We have a maintenance contract for the 'Ascenseur' (Elevator) with SUP-MAINT.
3. Office supplies (Fournitures) are bought from SUP-BUREAU.
4. TPE CMI 0802 deposit matches two individual card slips (TPE-SLIP-1 and TPE-SLIP-2) processed on Aug 2.
5. Cheque 008741 was issued last month and was outstanding.
6. The 2500 MAD inflow from 'CLIENT X' is unrecognized. Operations is investigating. Do not reconcile it until we have an invoice.
"""

# ---------------------------------------------------------------------------
# GROUND TRUTH
# ---------------------------------------------------------------------------
atlas_ground_truth = GroundTruth(
    matches=[
        ExpectedMatchGroup(bank_ids={"B1"}, book_ids={"J1"}),
        ExpectedMatchGroup(bank_ids={"B2"}, book_ids={"J2", "J3"}),
        ExpectedMatchGroup(bank_ids={"B3"}, book_ids={"J4"}),
        ExpectedMatchGroup(bank_ids={"B4"}, book_ids={"J5"}),
        ExpectedMatchGroup(bank_ids={"B5"}, book_ids={"J6"}),
        ExpectedMatchGroup(bank_ids={"B6"}, book_ids={"O1"}),
        ExpectedMatchGroup(bank_ids={"B7"}, book_ids={"J7"}),
        ExpectedMatchGroup(bank_ids={"B8"}, book_ids={"J8"}),
    ],
    unresolved_bank_ids={"B9"}, # The designed HOLD
    book_residuals={
        # All items are fully consumed in this specific scenario
        "J1": 0, "J2": 0, "J3": 0, "J4": 0, "J5": 0, "J6": 0, "O1": 0, "J7": 0, "J8": 0
    }
)


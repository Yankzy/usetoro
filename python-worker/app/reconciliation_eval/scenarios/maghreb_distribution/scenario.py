from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from evaluation.ground_truth import GroundTruth, ExpectedMatchGroup

# ---------------------------------------------------------------------------
# BANK MOVEMENTS (The Observed Side)
# ---------------------------------------------------------------------------
maghreb_bank_items = [
    # 1. The Global Assignment Trap (B1 & B2)
    # B1 has no reference, B2 explicitly claims Invoice 101. Both are 10,000 MAD.
    BankItem(id="B1", date=date(2026, 8, 10), amount_units="100000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR CLIENT ALPHA"),
    BankItem(id="B2", date=date(2026, 8, 12), amount_units="100000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR CLIENT ALPHA FACT 101", reference="101"),
    
    # 2. Customer paying several invoices together
    BankItem(id="B3", date=date(2026, 8, 13), amount_units="250000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR CLIENT BETA REGLEMENT 201 ET 202"),
    
    # 3. Supplier receiving several invoices in one payment
    BankItem(id="B4", date=date(2026, 8, 14), amount_units="400000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR FOURNISSEUR GAMMA"),
    
    # 4. Partial settlement (8,000 MAD payment against a 20,000 MAD invoice)
    BankItem(id="B5", date=date(2026, 8, 15), amount_units="80000000", direction=Direction.BANK_INFLOW, currency="MAD", description="ACOMPTE CLIENT DELTA"),
    
    # 5. Supplier credit/refund (Inflow from a supplier)
    BankItem(id="B6", date=date(2026, 8, 16), amount_units="50000000", direction=Direction.BANK_INFLOW, currency="MAD", description="RMB FOURNISSEUR EPSILON AVOIR CN-1"),
    
    # 6. Two identical amounts (12,000 MAD) belonging to different counterparties
    BankItem(id="B7", date=date(2026, 8, 17), amount_units="120000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR OMEGA SERVICES"),
    BankItem(id="B8", date=date(2026, 8, 17), amount_units="120000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="VIR ZETA LOGISTICS"),
    
    # 7. Bank fees
    BankItem(id="B9", date=date(2026, 8, 18), amount_units="30000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="FRAIS DE VIREMENTS")
]

# ---------------------------------------------------------------------------
# BOOK ITEMS (The Canonical Accounting Side)
# ---------------------------------------------------------------------------
maghreb_book_items = [
    # J1 & J2: The Trap Targets. Both 10,000 MAD for Client Alpha.
    BookItem(id="J1", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 1), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CUST-ALPHA", reference="101"),
    BookItem(id="J2", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 5), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CUST-ALPHA", reference="102"),
    
    # J3 & J4: Grouped customer payment targets (15k + 10k = 25k)
    BookItem(id="J3", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 6), remaining_amount_units="150000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CUST-BETA", reference="201"),
    BookItem(id="J4", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 7), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CUST-BETA", reference="202"),
    
    # J5 & J6: Grouped supplier payment targets (30k + 10k = 40k)
    BookItem(id="J5", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 8), remaining_amount_units="300000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-GAMMA", reference="F-GAM-1"),
    BookItem(id="J6", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 9), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-GAMMA", reference="F-GAM-2"),
    
    # J7: Target for partial settlement (20,000 MAD capacity)
    BookItem(id="J7", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 10), remaining_amount_units="200000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CUST-DELTA", reference="301"),
    
    # J8: Supplier refund (Debit balance representing a receivable from a supplier)
    BookItem(id="J8", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 11), remaining_amount_units="50000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="SUP-EPSILON", reference="CN-1"),
    
    # J9 & J10: Duplicate amounts for different suppliers
    BookItem(id="J9", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 12), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-OMEGA"),
    BookItem(id="J10", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 12), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="SUP-ZETA"),
    
    # J11: Bank fees
    BookItem(id="J11", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 18), remaining_amount_units="30000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="FEE-08")
]

maghreb_evidence = """
1. Client Alpha has two outstanding invoices of 10,000 MAD each: 101 and 102. 
2. Bank item B1 is from Client Alpha but has no specific reference. 
3. Bank item B2 is from Client Alpha and explicitly mentions invoice 101.
4. Client Beta's 25,000 MAD payment covers invoices 201 (15,000) and 202 (10,000).
5. We paid Supplier Gamma 40,000 MAD to settle invoices F-GAM-1 and F-GAM-2.
6. Client Delta sent an acompte (partial payment) of 8,000 MAD against invoice 301.
7. Supplier Epsilon refunded us 5,000 MAD for credit note CN-1.
8. We paid exactly 12,000 MAD each to Omega Services and Zeta Logistics.
"""

# ---------------------------------------------------------------------------
# GROUND TRUTH
# ---------------------------------------------------------------------------
maghreb_ground_truth = GroundTruth(
    matches=[
        ExpectedMatchGroup(bank_ids={"B1"}, book_ids={"J2"}), # B1 must take J2...
        ExpectedMatchGroup(bank_ids={"B2"}, book_ids={"J1"}), # ...so that B2 can take J1.
        ExpectedMatchGroup(bank_ids={"B3"}, book_ids={"J3", "J4"}),
        ExpectedMatchGroup(bank_ids={"B4"}, book_ids={"J5", "J6"}),
        ExpectedMatchGroup(bank_ids={"B5"}, book_ids={"J7"}), # Partial match
        ExpectedMatchGroup(bank_ids={"B6"}, book_ids={"J8"}),
        ExpectedMatchGroup(bank_ids={"B7"}, book_ids={"J9"}),
        ExpectedMatchGroup(bank_ids={"B8"}, book_ids={"J10"}),
        ExpectedMatchGroup(bank_ids={"B9"}, book_ids={"J11"}),
    ],
    unresolved_bank_ids=set(),
    book_residuals={
        "J1": 0,
        "J2": 0,
        "J3": 0,
        "J4": 0,
        "J5": 0,
        "J6": 0,
        "J7": 120000000, # 20,000 starting - 8,000 consumed = 12,000 remaining
        "J8": 0,
        "J9": 0,
        "J10": 0,
        "J11": 0
    }
)

from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from evaluation.ground_truth import GroundTruth, ExpectedMatchGroup

# ---------------------------------------------------------------------------
# BANK MOVEMENTS (The Observed Side)
# ---------------------------------------------------------------------------
atlas_const_bank_items = [
    # 1. Subcontractor payment (Cheque)
    BankItem(id="B1", date=date(2026, 8, 5), amount_units="150000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="CHQ 001122 SOUS-TRAITANT ALPHA", reference="001122"),
    
    # 2. Customer advance (Messy label)
    BankItem(id="B2", date=date(2026, 8, 12), amount_units="500000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR AVANCE CHANTIER Z"),
    
    # 3. The Cross-Period Trap
    # Client X pays 40,000 MAD. There is a new invoice for 40k, and an old invoice for 40k.
    BankItem(id="B3", date=date(2026, 8, 15), amount_units="400000000", direction=Direction.BANK_INFLOW, currency="MAD", description="REGLEMENT FACTURE CLIENT X"),
    
    # 4. Partial client payment
    BankItem(id="B4", date=date(2026, 8, 20), amount_units="200000000", direction=Direction.BANK_INFLOW, currency="MAD", description="VIR PARTIEL CLIENT Y PROJET B"),
    
    # 5. Long payment delay (Supplier cashing an old cheque)
    BankItem(id="B5", date=date(2026, 8, 28), amount_units="120000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="CHQ ENCAISSE 999888", reference="999888"),
    
    # 6. Unresolved messy bank label
    BankItem(id="B6", date=date(2026, 8, 30), amount_units="75000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="PRELEVEMENT INCONNU")
]

# ---------------------------------------------------------------------------
# BOOK ITEMS (The Canonical Accounting Side)
# ---------------------------------------------------------------------------
atlas_const_book_items = [
    # O1: Old subcontractor cheque from July
    BookItem(id="O1", source_type=SourceType.OPENING_STATE_ITEM, origin_period="2026-07", date=date(2026, 7, 20), remaining_amount_units="150000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="001122", counterparty_id="SUB-ALPHA"),
    
    # J1: Customer advance request for Chantier Z
    BookItem(id="J1", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 10), remaining_amount_units="500000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="ADV-CHANTIER-Z"),
    
    # O2 & J2: The Cross-Period Trap Targets
    # O2 is an old unpaid invoice. J2 is a brand new invoice.
    BookItem(id="O2", source_type=SourceType.OPENING_STATE_ITEM, origin_period="2025-12", date=date(2025, 12, 15), remaining_amount_units="400000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="INV-2025-99", counterparty_id="CLIENT-X"),
    BookItem(id="J2", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 14), remaining_amount_units="400000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", reference="INV-2026-45", counterparty_id="CLIENT-X"),
    
    # J3: Large invoice for Client Y (100,000 MAD total, will be partially settled)
    BookItem(id="J3", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-08", date=date(2026, 8, 18), remaining_amount_units="1000000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="CLIENT-Y"),
    
    # O3: Very old cheque issued in January 2026, finally cleared in August.
    BookItem(id="O3", source_type=SourceType.OPENING_STATE_ITEM, origin_period="2026-01", date=date(2026, 1, 10), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="999888"),
    
    # O4: Old outstanding cheque that MUST carry forward (Unresolved)
    BookItem(id="O4", source_type=SourceType.OPENING_STATE_ITEM, origin_period="2026-06", date=date(2026, 6, 25), remaining_amount_units="50000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="777666")
]

atlas_const_evidence = """
1. Cheque 001122 was issued to Subcontractor Alpha last month.
2. We requested a 50,000 MAD advance for 'Chantier Z'.
3. CLIENT ACCOUNTING POLICY: Always apply customer payments to the oldest outstanding invoice first unless explicitly referenced otherwise. Client X has an invoice from December 2025 and a new one from August 2026.
4. Client Y is paying a 20,000 MAD 'acompte' (installment) towards their balance.
5. Cheque 999888 is a very old supplier cheque from January that was delayed.
6. Cheque 777666 has still not cleared the bank. Leave it unresolved to carry forward.
7. We have no record of the 7,500 MAD 'PRELEVEMENT INCONNU'.
"""

# ---------------------------------------------------------------------------
# GROUND TRUTH
# ---------------------------------------------------------------------------
atlas_const_ground_truth = GroundTruth(
    matches=[
        ExpectedMatchGroup(bank_ids={"B1"}, book_ids={"O1"}),
        ExpectedMatchGroup(bank_ids={"B2"}, book_ids={"J1"}),
        ExpectedMatchGroup(bank_ids={"B3"}, book_ids={"O2"}), # Correctly navigates the cross-period trap
        ExpectedMatchGroup(bank_ids={"B4"}, book_ids={"J3"}), # Partial settlement
        ExpectedMatchGroup(bank_ids={"B5"}, book_ids={"O3"}),
    ],
    unresolved_bank_ids={"B6"},
    book_residuals={
        "O1": 0,
        "J1": 0,
        "O2": 0,
        "J2": 400000000,   # Carries forward completely
        "J3": 800000000,   # 100,000 starting - 20,000 paid = 80,000 remaining
        "O3": 0,
        "O4": 50000000     # The required carry-forward item from Section 47
    }
)
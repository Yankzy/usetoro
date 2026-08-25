from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from evaluation.ground_truth import GroundTruth, ExpectedMatchGroup

# ---------------------------------------------------------------------------
# BANK MOVEMENTS (Extracted from "releve_bancaire.pdf")
# ---------------------------------------------------------------------------
atlas_office_bank_items = [
    BankItem(id="B1", date=date(2026, 7, 2), amount_units="120000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Loyer Juil. Soc. Immobiliere Anfa"),
    BankItem(id="B2", date=date(2026, 7, 5), amount_units="250000000", direction=Direction.BANK_INFLOW, currency="MAD", description="Virement Client ABC Construction SARL"),
    BankItem(id="B3", date=date(2026, 7, 8), amount_units="8500000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Paiement CB Station Afriquia Oasis"),
    BankItem(id="B4", date=date(2026, 7, 10), amount_units="24000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Achats Fournitures Marjane Business HQ"),
    BankItem(id="B5", date=date(2026, 7, 12), amount_units="420000000", direction=Direction.BANK_INFLOW, currency="MAD", description="Virement Client XYZ Industrie SA"),
    BankItem(id="B6", date=date(2026, 7, 15), amount_units="12000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Prelevement Facture Orange Maroc SA"),
    BankItem(id="B7", date=date(2026, 7, 18), amount_units="68000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Prelevement Mensuel CNSS"),
    BankItem(id="B8", date=date(2026, 7, 20), amount_units="14500000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Prelevement Facture Lydec Casablanca"),
    BankItem(id="B9", date=date(2026, 7, 22), amount_units="35000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Honoraires Cabinet Comptable El Fassi"),
    BankItem(id="B10", date=date(2026, 7, 25), amount_units="185000000", direction=Direction.BANK_INFLOW, currency="MAD", description="Virement Recu Technopark IT Solutions"),
    BankItem(id="B11", date=date(2026, 7, 27), amount_units="9500000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Prelevement Maroc Telecom (IAM)"), # The Trap: Missing book item!
    BankItem(id="B12", date=date(2026, 7, 28), amount_units="18000000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Paiement Imprimerie Moderne SARL"),
    BankItem(id="B13", date=date(2026, 7, 30), amount_units="1650000", direction=Direction.BANK_OUTFLOW, currency="MAD", description="Frais Tenue de Compte Attijariwafa Bank"),
]

# ---------------------------------------------------------------------------
# BOOK ITEMS (Simulated Accounting Ledger)
# ---------------------------------------------------------------------------
atlas_office_book_items = [
    BookItem(id="J1", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 1), remaining_amount_units="120000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="FA-LOYER-07"),
    BookItem(id="J2", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-06", date=date(2026, 6, 20), remaining_amount_units="150000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="ABC-CONST", reference="INV-100"),
    BookItem(id="J3", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 2), remaining_amount_units="100000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="ABC-CONST", reference="INV-101"),
    BookItem(id="J4", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 8), remaining_amount_units="8500000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="NDF-ESSENCE"),
    BookItem(id="J5", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 10), remaining_amount_units="24000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="FA-FOURNITURES"),
    BookItem(id="J6", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 11), remaining_amount_units="420000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="XYZ-IND", reference="INV-105"),
    BookItem(id="J7", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 15), remaining_amount_units="12000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="ORANGE", reference="FA-TEL-07"),
    BookItem(id="J8", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 18), remaining_amount_units="68000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="DECL-CNSS"),
    BookItem(id="J9", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 20), remaining_amount_units="14500000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="LYDEC", reference="FA-EAU-ELEC"),
    BookItem(id="J10", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 21), remaining_amount_units="35000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="COMPTABLE", reference="HON-07"),
    BookItem(id="J11", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 25), remaining_amount_units="185000000", direction=Direction.BOOK_BANK_DEBIT, currency="MAD", counterparty_id="TECHNOPARK", reference="INV-108"),
    BookItem(id="J12", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 28), remaining_amount_units="18000000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", counterparty_id="IMPRIMERIE", reference="FA-PRINT"),
    BookItem(id="J13", source_type=SourceType.POSTED_BOOK_ITEM, origin_period="2026-07", date=date(2026, 7, 30), remaining_amount_units="1650000", direction=Direction.BOOK_BANK_CREDIT, currency="MAD", reference="AGIOS-07"),
]

atlas_office_evidence = """
1. Client ABC Construction paid two invoices together: INV-100 (15,000 MAD) and INV-101 (10,000 MAD).
2. The accountant has confirmed that the Maroc Telecom (IAM) invoice for 950 MAD has NOT been entered into the ERP yet. Leave this bank line unresolved.
3. All other transactions correspond directly to their respective suppliers or clients.
"""

# ---------------------------------------------------------------------------
# GROUND TRUTH
# ---------------------------------------------------------------------------
atlas_office_ground_truth = GroundTruth(
    matches=[
        ExpectedMatchGroup(bank_ids={"B1"}, book_ids={"J1"}),
        ExpectedMatchGroup(bank_ids={"B2"}, book_ids={"J2", "J3"}), # Grouped match
        ExpectedMatchGroup(bank_ids={"B3"}, book_ids={"J4"}),
        ExpectedMatchGroup(bank_ids={"B4"}, book_ids={"J5"}),
        ExpectedMatchGroup(bank_ids={"B5"}, book_ids={"J6"}),
        ExpectedMatchGroup(bank_ids={"B6"}, book_ids={"J7"}),
        ExpectedMatchGroup(bank_ids={"B7"}, book_ids={"J8"}),
        ExpectedMatchGroup(bank_ids={"B8"}, book_ids={"J9"}),
        ExpectedMatchGroup(bank_ids={"B9"}, book_ids={"J10"}),
        ExpectedMatchGroup(bank_ids={"B10"}, book_ids={"J11"}),
        ExpectedMatchGroup(bank_ids={"B12"}, book_ids={"J12"}),
        ExpectedMatchGroup(bank_ids={"B13"}, book_ids={"J13"}),
    ],
    unresolved_bank_ids={"B11"}, # Maroc Telecom intentionally left unresolved
    book_residuals={
        j.id: 0 for j in atlas_office_book_items # All book items should be fully consumed
    }
)
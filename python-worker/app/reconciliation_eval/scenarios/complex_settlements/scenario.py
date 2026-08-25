from datetime import date

from domain.bank import BankItem
from domain.base import Direction, SourceType
from domain.books import BookItem
from evaluation.ground_truth import ExpectedMatchGroup, GroundTruth


complex_settlements_bank_items = [
    BankItem(
        id="B_SETTLEMENT_1",
        date=date(2026, 8, 20),
        amount_units="2000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Settlement tranche 1",
    ),
    BankItem(
        id="B_SETTLEMENT_2",
        date=date(2026, 8, 20),
        amount_units="3000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Settlement tranche 2",
    ),
    BankItem(
        id="B_SETTLEMENT_3",
        date=date(2026, 8, 20),
        amount_units="5000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Settlement tranche 3",
    ),
]

complex_settlements_book_items = [
    BookItem(
        id="J_SETTLEMENT_1",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-08",
        date=date(2026, 8, 20),
        remaining_amount_units="10000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        counterparty_id="SETTLEMENT-CUSTOMER",
        reference="SETTLEMENT-10000",
    )
]

complex_settlements_evidence = (
    "The customer settled invoice SETTLEMENT-10000 through three bank transfers "
    "of 2,000, 3,000, and 5,000 MAD on the same day."
)

complex_settlements_ground_truth = GroundTruth(
    matches=[
        ExpectedMatchGroup(
            bank_ids={"B_SETTLEMENT_1", "B_SETTLEMENT_2", "B_SETTLEMENT_3"},
            book_ids={"J_SETTLEMENT_1"},
        )
    ],
    unresolved_bank_ids=set(),
    book_residuals={"J_SETTLEMENT_1": 0},
)

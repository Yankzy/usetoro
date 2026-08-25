"""Tests for reconciliation domain models."""

from domain.patch import ExpectedResidual


def test_zero_residual_is_valid_for_fully_consumed_book_item() -> None:
    residual = ExpectedResidual(book_item_id="J1", residual_amount_units="0")
    assert residual.residual_amount_units == "0"

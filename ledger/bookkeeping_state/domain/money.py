from __future__ import annotations

from decimal import Decimal, InvalidOperation
from typing import Annotated, Final

from pydantic import StringConstraints


SOLVER_UNITS_PER_MAJOR_UNIT: Final[int] = 10_000
SOLVER_DECIMAL_PLACES: Final[int] = 4

_AMOUNT_PATTERN: Final[str] = r"^[1-9][0-9]*$"
_RESIDUAL_AMOUNT_PATTERN: Final[str] = r"^(0|[1-9][0-9]*)$"


AmountUnits = Annotated[
    str,
    StringConstraints(pattern=_AMOUNT_PATTERN),
]
"""
Positive monetary amount represented as integer solver units encoded as a
decimal digit string.

Example:
    "125000" == 12.5000 MAD

Zero is intentionally forbidden. A transaction, allocation, invoice amount,
or hypothesis allocation must represent positive economic value.
"""


ResidualAmountUnits = Annotated[
    str,
    StringConstraints(pattern=_RESIDUAL_AMOUNT_PATTERN),
]
"""
Non-negative residual monetary amount represented as integer solver units
encoded as a decimal digit string.

Zero is allowed because a fully consumed book item has no remaining balance.
"""


def amount_units_to_int(value: AmountUnits | ResidualAmountUnits) -> int:
    """
    Convert the canonical string representation into an integer suitable
    for arithmetic and CP-SAT.

    No floating-point conversion occurs.
    """
    return int(value)


def major_units_to_solver_units(value: str | Decimal) -> int:
    """
    Convert a human-readable major-unit amount into exact solver units.

    Examples:
        "1"        -> 10000
        "12.5"     -> 125000
        "12.5000"  -> 125000

    Raises:
        ValueError:
            If the input is invalid, negative, or has more than four
            non-zero decimal places.

    This function never uses binary floating-point arithmetic.
    """
    try:
        decimal_value = value if isinstance(value, Decimal) else Decimal(value)
    except (InvalidOperation, ValueError) as exc:
        raise ValueError(f"Invalid monetary value: {value!r}") from exc

    if not decimal_value.is_finite():
        raise ValueError(f"Monetary value must be finite: {value!r}")

    if decimal_value < 0:
        raise ValueError(f"Monetary value cannot be negative: {value!r}")

    scaled = decimal_value * SOLVER_UNITS_PER_MAJOR_UNIT
    integral = scaled.to_integral_value()

    if scaled != integral:
        raise ValueError(
            f"Monetary value {value!r} exceeds "
            f"{SOLVER_DECIMAL_PLACES} decimal places"
        )

    return int(integral)


def major_units_to_amount_units(value: str | Decimal) -> str:
    """
    Convert a strictly positive major-unit amount into canonical AmountUnits.
    """
    units = major_units_to_solver_units(value)

    if units <= 0:
        raise ValueError("AmountUnits must represent a value greater than zero")

    return str(units)


def major_units_to_residual_units(value: str | Decimal) -> str:
    """
    Convert a non-negative major-unit amount into canonical ResidualAmountUnits.
    """
    units = major_units_to_solver_units(value)

    if units < 0:
        raise ValueError("ResidualAmountUnits cannot be negative")

    return str(units)


def solver_units_to_decimal(
    value: int | AmountUnits | ResidualAmountUnits,
) -> Decimal:
    """
    Convert solver units into an exact Decimal major-unit value.

    Example:
        125000 -> Decimal("12.5")
    """
    units = int(value)

    if units < 0:
        raise ValueError("Solver-unit amount cannot be negative")

    return Decimal(units) / Decimal(SOLVER_UNITS_PER_MAJOR_UNIT)


def format_solver_units(
    value: int | AmountUnits | ResidualAmountUnits,
) -> str:
    """
    Render solver units using the canonical four-decimal representation.

    Example:
        125000 -> "12.5000"
        10000  -> "1.0000"
    """
    decimal_value = solver_units_to_decimal(value)
    return f"{decimal_value:.{SOLVER_DECIMAL_PLACES}f}"


def format_money(
    value: int | AmountUnits | ResidualAmountUnits | Decimal | None,
    currency: str = "MAD",
) -> str:
    """
    Format solver units or Decimal into standard human-readable currency display.

    Examples:
        78_500_000  -> "7,850.00 MAD"
        1_500_000   -> "150.00 MAD"
        34_000_000  -> "3,400.00 MAD"
        100_000_000 -> "10,000.00 MAD"
    """
    if value is None:
        dec = Decimal("0")
    elif isinstance(value, Decimal):
        dec = value
    else:
        dec = solver_units_to_decimal(value)

    if currency:
        return f"{dec:,.2f} {currency}".strip()
    return f"{dec:,.2f}"
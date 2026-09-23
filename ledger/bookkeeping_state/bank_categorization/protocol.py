from __future__ import annotations

from typing import Protocol, runtime_checkable

from bookkeeping_state.bank_categorization.transport_models import (
    BankCategorizeResponseEnvelope,
)
from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationView,
)


from bookkeeping_state.dag.errors import AseTransportError


class MissingResidualBankCategorizerError(AseTransportError):
    """Raised when production session requires a residual bank categorizer but none is configured."""
    error_code = "MISSING_RESIDUAL_BANK_CATEGORIZER"


@runtime_checkable
class ResidualBankCategorizer(Protocol):
    """
    Protocol defining the contract for ASE residual bank categorization.

    Consuming components (such as BookkeepingSession) depend on this protocol,
    allowing clean injection of NatsAseBankCategorizer or test mocks.
    """

    def categorize_view(
        self,
        view: ResidualBankCategorizationView,
    ) -> BankCategorizeResponseEnvelope:
        ...

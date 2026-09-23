from enum import StrEnum


class Direction(StrEnum):
    """
    Direction of value movement from the perspective of the originating
    bookkeeping artifact.

    The BANK_* and BOOK_BANK_* values preserve the exact reconciliation
    semantics already proven by the reconciliation eval.

    INFLOW and OUTFLOW are retained because the existing reconciliation
    implementation also uses them in simplified scenarios and validation.
    """

    BANK_INFLOW = "BANK_INFLOW"
    BANK_OUTFLOW = "BANK_OUTFLOW"

    BOOK_BANK_DEBIT = "BOOK_BANK_DEBIT"
    BOOK_BANK_CREDIT = "BOOK_BANK_CREDIT"

    INFLOW = "INFLOW"
    OUTFLOW = "OUTFLOW"


class SourceType(StrEnum):
    """
    Durable origin of a bookkeeping item.

    These values intentionally preserve the source taxonomy already used
    by the routing and reconciliation implementation.
    """

    BANK_STATEMENT_LINE = "BANK_STATEMENT_LINE"
    STAGING_BOOK_ITEM = "STAGING_BOOK_ITEM"
    POSTED_BOOK_ITEM = "POSTED_BOOK_ITEM"
    OPENING_STATE_ITEM = "OPENING_STATE_ITEM"


class SourceArtifactKind(StrEnum):
    """
    Authoritative durable source artifact that originated a bookkeeping item.
    """

    INVOICE = "INVOICE"
    BILL = "BILL"
    TRANSACTION = "TRANSACTION"


class BookkeepingRole(StrEnum):
    """
    Accounting lifecycle role of a bookkeeping item.

    OPEN_RECEIVABLE:
        Open receivable obligation from an approved customer invoice.
        Awaiting settlement evidence in Stage 1. NOT a settlement.

    OPEN_PAYABLE:
        Open payable obligation from an approved vendor bill.
        Awaiting settlement evidence in Stage 1. NOT a settlement.

    POSTED_CASH_MOVEMENT:
        Finalized cash-account transaction in the general ledger.
        Awaiting bank reconciliation in Stage 2.
    """

    OPEN_RECEIVABLE = "OPEN_RECEIVABLE"
    OPEN_PAYABLE = "OPEN_PAYABLE"
    POSTED_CASH_MOVEMENT = "POSTED_CASH_MOVEMENT"


class Eligibility(StrEnum):
    """
    Whether a reconciliation hypothesis may participate in final selection.

    SELECTABLE:
        May be chosen by the optimizer.

    INELIGIBLE:
        Excluded from the optimizer's selectable set (e.g. insufficient or
        contradictory semantic evidence).

    COUNTERFACTUAL_ONLY:
        May exist for diagnostics/evaluation but must not become part of
        the selected bookkeeping result.
    """

    SELECTABLE = "SELECTABLE"
    INELIGIBLE = "INELIGIBLE"
    COUNTERFACTUAL_ONLY = "COUNTERFACTUAL_ONLY"


class SemanticAdmissibility(StrEnum):
    """
    Qualitative evidence state of a reconciliation candidate or pairwise relationship.

    SUPPORTED:
        Affirmative, uncontradicted evidence of economic identity connects the
        records (e.g. matching normalized reference, verified counterparty correspondence).
        Eligible for global CP-SAT selection.

    INSUFFICIENT_EVIDENCE:
        Candidate is mathematically/directionally feasible, but lacks affirmative
        evidence of economic identity. Ineligible for selection.

    CONTRADICTED:
        Active counter-evidence exists (e.g. conflicting explicit business references
        or incompatible counterparties). Ineligible for selection.
    """

    SUPPORTED = "SUPPORTED"
    INSUFFICIENT_EVIDENCE = "INSUFFICIENT_EVIDENCE"
    CONTRADICTED = "CONTRADICTED"


class AllocationSupport(StrEnum):
    """
    Evidentiary basis for allocating cash flow to specific BookItem obligations.

    EXPLICIT_EVIDENCE:
        Direct evidence identifies the specific obligation allocation (e.g. matching
        reference, explicit reference in narration, shared structured batch token).

    UNIQUE_INFERENCE:
        No explicit allocation evidence exists, but deterministic state analysis
        establishes exactly one allocation consistent with supported identity and
        all hard constraints (currency, account, date, direction, residual capacity).

    INSUFFICIENT_EVIDENCE:
        More than one allocation remains possible, or no unique allocation can be
        established.

    CONTRADICTED:
        Explicit evidence conflicts with the proposed obligation allocation.
    """

    EXPLICIT_EVIDENCE = "EXPLICIT_EVIDENCE"
    UNIQUE_INFERENCE = "UNIQUE_INFERENCE"
    INSUFFICIENT_EVIDENCE = "INSUFFICIENT_EVIDENCE"
    CONTRADICTED = "CONTRADICTED"
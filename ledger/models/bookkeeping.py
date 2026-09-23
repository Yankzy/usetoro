"""
Bookkeeping Decision and Provenance Models
-------------------------------------------
Durable persistence models for BookkeepingState transitions, optimistic concurrency (OCC),
Stage-1 payment application provenance, and Stage-2 bank reconciliation.

These models store accepted bookkeeping decisions and execution provenance without
duplicating the double-entry accounting truth owned by the Django ledger.
"""

from __future__ import annotations

from typing import Any, cast
from uuid import uuid4

from django.core.validators import MaxValueValidator, MinValueValidator
from django.db import models, transaction
from django.db.models import Q
from django.utils import timezone


class BookkeepingRevision(models.Model):
    """
    Authoritative optimistic concurrency (OCC) head per legal entity.
    """

    entity = models.OneToOneField(
        "ledger.EntityModel",
        primary_key=True,
        on_delete=models.CASCADE,
        related_name="bookkeeping_revision",
    )
    revision = models.BigIntegerField(
        default=0,  # type: ignore
        validators=[MinValueValidator(0)],
        help_text="Monotonically increasing durable persistence revision.",
    )
    updated_at = models.DateTimeField(auto_now=True)

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_revision"
        verbose_name = "Bookkeeping Revision"
        verbose_name_plural = "Bookkeeping Revisions"

    def __str__(self) -> str:
        return f"BookkeepingRevision(entity={self.entity}, revision={self.revision})"


# ======================================================================
# Stage 1: Payment Application / Settlement Execution Provenance
# ======================================================================


class BookkeepingPaymentApplication(models.Model):
    """
    Durable execution record of a Stage-1 payment application.

    Records the provenance linking an external BankItem to the executed
    Django accounting settlement.
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_payment_applications",
    )

    objects = models.Manager()

    # Bank movement source: strictly one of PlaidTransaction or StagedTransactionModel
    plaid_transaction = models.ForeignKey(
        "ledger.PlaidTransaction",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_payment_applications",
    )
    staged_transaction = models.ForeignKey(
        "ledger.StagedTransactionModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_payment_applications",
    )

    total_amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Total payment amount in integer solver units (scale factor 10,000).",
    )
    direction = models.CharField(
        max_length=20,
        help_text="Bank movement direction (e.g. BANK_INFLOW, BANK_OUTFLOW).",
    )
    currency = models.CharField(max_length=3)

    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision = models.IntegerField(
        validators=[MinValueValidator(0)],
        help_text="Local state revision at payment execution.",
    )
    created_at = models.DateTimeField()

    entity_id: int
    plaid_transaction_id: Any
    staged_transaction_id: Any
    allocations: Any

    class Meta:
        db_table = "ledger_bookkeeping_payment_application"
        verbose_name = "Bookkeeping Payment Application"
        verbose_name_plural = "Bookkeeping Payment Applications"
        constraints = [
            models.CheckConstraint(
                condition=(
                    Q(plaid_transaction__isnull=False, staged_transaction__isnull=True)
                    | Q(plaid_transaction__isnull=True, staged_transaction__isnull=False)
                ),
                name="check_payment_app_bank_source_xor",
            ),
            models.CheckConstraint(
                condition=Q(total_amount_units__gt=0),
                name="check_payment_app_amount_positive",
            ),
            models.UniqueConstraint(
                fields=["plaid_transaction"],
                condition=Q(plaid_transaction__isnull=False),
                name="uniq_payment_app_plaid",
            ),
            models.UniqueConstraint(
                fields=["staged_transaction"],
                condition=Q(staged_transaction__isnull=False),
                name="uniq_payment_app_staged",
            ),
        ]
        indexes = [
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingPaymentApplication({self.id}, amount={self.total_amount_units})"


class BookkeepingPaymentApplicationAllocation(models.Model):
    """
    Allocation leg of a payment application to a specific obligation (Invoice or Bill),
    linking directly to the resulting posted cash TransactionModel row.
    """

    id = models.BigAutoField(primary_key=True)
    payment_application = models.ForeignKey(
        "BookkeepingPaymentApplication",
        on_delete=models.CASCADE,
        related_name="allocations",
    )

    objects = models.Manager()

    # Obligation target: strictly one of InvoiceModel or BillModel
    invoice = models.ForeignKey(
        "ledger.InvoiceModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="payment_application_allocations",
    )
    bill = models.ForeignKey(
        "ledger.BillModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="payment_application_allocations",
    )

    amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Allocated amount in integer solver units (scale factor 10,000).",
    )

    # Resulting real accounting cash transaction created by Django payment posting
    # Note: JournalEntryModel is reached via cash_transaction.journal_entry.
    cash_transaction = models.ForeignKey(
        "ledger.TransactionModel",
        on_delete=models.PROTECT,
        related_name="payment_application_allocations",
    )

    payment_application_id: str
    invoice_id: Any
    bill_id: Any
    cash_transaction_id: Any

    class Meta:
        db_table = "ledger_bookkeeping_payment_application_allocation"
        verbose_name = "Bookkeeping Payment Application Allocation"
        verbose_name_plural = "Bookkeeping Payment Application Allocations"
        constraints = [
            models.CheckConstraint(
                condition=(
                    Q(invoice__isnull=False, bill__isnull=True)
                    | Q(invoice__isnull=True, bill__isnull=False)
                ),
                name="check_payment_app_alloc_target_xor",
            ),
            models.CheckConstraint(
                condition=Q(amount_units__gt=0),
                name="check_payment_app_alloc_amount_positive",
            ),
        ]
        indexes = [
            models.Index(fields=["payment_application"]),
            models.Index(fields=["cash_transaction"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingPaymentApplicationAllocation(app={self.payment_application}, amount={self.amount_units})"


# ======================================================================
# Routing Decisions & Invalidations
# ======================================================================


class BookkeepingRoutingDecision(models.Model):
    """
    Append-only durable record of an accepted bank routing decision.
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_routing_decisions",
    )

    objects = models.Manager()

    # Obligation / Item Target: strictly one of Invoice, Bill, or Transaction
    invoice = models.ForeignKey(
        "ledger.InvoiceModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_routing_decisions",
    )
    bill = models.ForeignKey(
        "ledger.BillModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_routing_decisions",
    )
    transaction = models.ForeignKey(
        "ledger.TransactionModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_routing_decisions",
    )

    bank_account = models.ForeignKey(
        "ledger.BankAccountModel",
        on_delete=models.PROTECT,
        related_name="bookkeeping_routed_decisions",
    )
    source = models.CharField(max_length=50)
    utility = models.IntegerField(
        default=cast(Any, 1000),
        validators=[MinValueValidator(0), MaxValueValidator(1000)],
    )
    solver_run_id = models.CharField(max_length=255, null=True, blank=True)

    supersedes = models.ForeignKey(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="superseded_by",
    )
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_creation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    entity_id: int
    invoice_id: Any
    bill_id: Any
    transaction_id: Any
    bank_account_id: Any
    supersedes_id: Any

    class Meta:
        db_table = "ledger_bookkeeping_routing_decision"
        verbose_name = "Bookkeeping Routing Decision"
        verbose_name_plural = "Bookkeeping Routing Decisions"
        constraints = [
            models.CheckConstraint(
                condition=Q(
                    Q(invoice__isnull=False, bill__isnull=True, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=False, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=True, transaction__isnull=False),
                    _connector=Q.OR,
                ),
                name="check_routing_target_xor",
            ),
        ]
        indexes = [
            models.Index(fields=["entity", "invoice"]),
            models.Index(fields=["entity", "bill"]),
            models.Index(fields=["entity", "transaction"]),
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingRoutingDecision({self.id}, account={self.bank_account})"


class BookkeepingRoutingInvalidation(models.Model):
    """
    Append-only durable invalidation of a prior routing decision.
    """

    id = models.CharField(max_length=255, primary_key=True)
    routing_decision = models.ForeignKey(
        "BookkeepingRoutingDecision",
        on_delete=models.PROTECT,
        related_name="invalidations",
    )
    reason = models.TextField()
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_invalidation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    routing_decision_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_routing_invalidation"
        verbose_name = "Bookkeeping Routing Invalidation"
        verbose_name_plural = "Bookkeeping Routing Invalidations"
        indexes = [
            models.Index(fields=["routing_decision"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingRoutingInvalidation({self.id}, target={self.routing_decision})"


# ======================================================================
# Classification Decisions & Invalidations
# ======================================================================


class BookkeepingClassificationDecision(models.Model):
    """
    Append-only durable record of an accepted account classification decision.
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_classification_decisions",
    )

    objects = models.Manager()

    # Obligation / Item Target: strictly one of Invoice, Bill, or Transaction
    invoice = models.ForeignKey(
        "ledger.InvoiceModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_classification_decisions",
    )
    bill = models.ForeignKey(
        "ledger.BillModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_classification_decisions",
    )
    transaction = models.ForeignKey(
        "ledger.TransactionModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_classification_decisions",
    )

    account_code = models.CharField(max_length=50)
    account = models.ForeignKey(
        "ledger.AccountModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_classification_decisions",
    )
    source = models.CharField(max_length=50)
    confidence = models.FloatField(default=cast(Any, 1.0))
    rationale = models.TextField(null=True, blank=True)
    evidence_refs = models.JSONField(default=list)

    supersedes = models.ForeignKey(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="superseded_by",
    )
    session_id = models.CharField(max_length=255, null=True, blank=True)
    dag_run_id = models.CharField(max_length=255, null=True, blank=True)
    ase_node_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_decision = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    entity_id: int
    invoice_id: Any
    bill_id: Any
    transaction_id: Any
    account_id: Any
    supersedes_id: Any

    class Meta:
        db_table = "ledger_bookkeeping_classification_decision"
        verbose_name = "Bookkeeping Classification Decision"
        verbose_name_plural = "Bookkeeping Classification Decisions"
        constraints = [
            models.CheckConstraint(
                condition=Q(
                    Q(invoice__isnull=False, bill__isnull=True, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=False, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=True, transaction__isnull=False),
                    _connector=Q.OR,
                ),
                name="check_classification_target_xor",
            ),
        ]
        indexes = [
            models.Index(fields=["entity", "invoice"]),
            models.Index(fields=["entity", "bill"]),
            models.Index(fields=["entity", "transaction"]),
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingClassificationDecision({self.id}, code={self.account_code})"


class BookkeepingClassificationInvalidation(models.Model):
    """
    Append-only durable invalidation of a prior classification decision.
    """

    id = models.CharField(max_length=255, primary_key=True)
    classification = models.ForeignKey(
        "BookkeepingClassificationDecision",
        on_delete=models.PROTECT,
        related_name="invalidations",
    )
    reason = models.TextField()
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_invalidation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    classification_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_classification_invalidation"
        verbose_name = "Bookkeeping Classification Invalidation"
        verbose_name_plural = "Bookkeeping Classification Invalidations"
        indexes = [
            models.Index(fields=["classification"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingClassificationInvalidation({self.id}, target={self.classification})"


# ======================================================================
# Evidence Assertions & Invalidations
# ======================================================================


class BookkeepingEvidenceAssertion(models.Model):
    """
    Append-only durable assertion of semantic evidence for an obligation or transaction.
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_evidence_assertions",
    )

    objects = models.Manager()

    # Obligation / Item Target: strictly one of Invoice, Bill, or Transaction
    invoice = models.ForeignKey(
        "ledger.InvoiceModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_evidence_assertions",
    )
    bill = models.ForeignKey(
        "ledger.BillModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_evidence_assertions",
    )
    transaction = models.ForeignKey(
        "ledger.TransactionModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="bookkeeping_evidence_assertions",
    )

    evidence_type = models.CharField(max_length=50)
    value = models.TextField(null=True, blank=True)
    document_ids = models.JSONField(default=list)
    source = models.CharField(max_length=50)

    supersedes = models.ForeignKey(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="superseded_by",
    )
    reason = models.TextField(null=True, blank=True)
    confidence = models.FloatField(default=cast(Any, 1.0))
    metadata = models.JSONField(default=dict)
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_creation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    entity_id: int
    invoice_id: Any
    bill_id: Any
    transaction_id: Any
    supersedes_id: Any

    class Meta:
        db_table = "ledger_bookkeeping_evidence_assertion"
        verbose_name = "Bookkeeping Evidence Assertion"
        verbose_name_plural = "Bookkeeping Evidence Assertions"
        constraints = [
            models.CheckConstraint(
                condition=Q(
                    Q(invoice__isnull=False, bill__isnull=True, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=False, transaction__isnull=True),
                    Q(invoice__isnull=True, bill__isnull=True, transaction__isnull=False),
                    _connector=Q.OR,
                ),
                name="check_evidence_target_xor",
            ),
        ]
        indexes = [
            models.Index(fields=["entity", "invoice"]),
            models.Index(fields=["entity", "bill"]),
            models.Index(fields=["entity", "transaction"]),
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingEvidenceAssertion({self.id}, type={self.evidence_type})"


class BookkeepingEvidenceInvalidation(models.Model):
    """
    Append-only durable invalidation of a prior evidence assertion.
    """

    id = models.CharField(max_length=255, primary_key=True)
    assertion = models.ForeignKey(
        "BookkeepingEvidenceAssertion",
        on_delete=models.PROTECT,
        related_name="invalidations",
    )
    reason = models.TextField()
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_invalidation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    assertion_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_evidence_invalidation"
        verbose_name = "Bookkeeping Evidence Invalidation"
        verbose_name_plural = "Bookkeeping Evidence Invalidations"
        indexes = [
            models.Index(fields=["assertion"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingEvidenceInvalidation({self.id}, target={self.assertion})"


# ======================================================================
# Stage 2: Bank Reconciliation Relationships & Invalidations
# ======================================================================


class BookkeepingReconciliation(models.Model):
    """
    Accepted Stage-2 bank reconciliation header.

    Records the accepted matching relationship between external BankItems
    and posted Cash/Bank GL transactions (POSTED_BOOK_ITEM).
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_reconciliations",
    )

    objects = models.Manager()
    evidence_refs = models.JSONField(default=list)

    source_hypothesis_id = models.CharField(max_length=255, null=True, blank=True)
    source_hypothesis_state_revision = models.IntegerField(
        null=True,
        blank=True,
        validators=[MinValueValidator(0)],
    )
    source_hypothesis_utility = models.IntegerField(
        null=True,
        blank=True,
        validators=[MinValueValidator(0), MaxValueValidator(1000)],
    )
    source_hypothesis_generated_at = models.DateTimeField(null=True, blank=True)
    source_hypothesis_admissibility = models.CharField(max_length=50, null=True, blank=True)
    source_hypothesis_allocation_support = models.CharField(max_length=50, null=True, blank=True)

    semantic_rationale = models.TextField(null=True, blank=True, max_length=4000)
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_creation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    entity_id: int
    bank_allocations: Any
    book_allocations: Any

    class Meta:
        db_table = "ledger_bookkeeping_reconciliation"
        verbose_name = "Bookkeeping Reconciliation"
        verbose_name_plural = "Bookkeeping Reconciliations"
        indexes = [
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingReconciliation({self.id})"


class BookkeepingReconciliationBookAllocation(models.Model):
    """
    Book allocation leg of a Stage-2 reconciliation.
    Strictly targets a real posted cash TransactionModel row.
    """

    id = models.BigAutoField(primary_key=True)
    reconciliation = models.ForeignKey(
        "BookkeepingReconciliation",
        on_delete=models.CASCADE,
        related_name="book_allocations",
    )

    objects = models.Manager()
    transaction = models.ForeignKey(
        "ledger.TransactionModel",
        on_delete=models.PROTECT,
        related_name="reconciliation_allocations",
    )
    amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Allocated amount in integer solver units (scale factor 10,000).",
    )

    reconciliation_id: str
    transaction_id: Any

    class Meta:
        db_table = "ledger_bookkeeping_reconciliation_book_allocation"
        verbose_name = "Bookkeeping Reconciliation Book Allocation"
        verbose_name_plural = "Bookkeeping Reconciliation Book Allocations"
        constraints = [
            models.CheckConstraint(
                condition=Q(amount_units__gt=0),
                name="check_rec_book_alloc_amount_positive",
            ),
            models.UniqueConstraint(
                fields=["reconciliation", "transaction"],
                name="uniq_rec_book_alloc",
            ),
        ]
        indexes = [
            models.Index(fields=["reconciliation"]),
            models.Index(fields=["transaction"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingReconciliationBookAllocation(rec={self.reconciliation}, tx={self.transaction}, amount={self.amount_units})"


class BookkeepingReconciliationBankAllocation(models.Model):
    """
    Bank allocation leg of a Stage-2 reconciliation.
    Strictly targets one PlaidTransaction or StagedTransactionModel.
    """

    id = models.BigAutoField(primary_key=True)
    reconciliation = models.ForeignKey(
        "BookkeepingReconciliation",
        on_delete=models.CASCADE,
        related_name="bank_allocations",
    )

    objects = models.Manager()
    plaid_transaction = models.ForeignKey(
        "ledger.PlaidTransaction",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="reconciliation_allocations",
    )
    staged_transaction = models.ForeignKey(
        "ledger.StagedTransactionModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="reconciliation_allocations",
    )
    amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Allocated amount in integer solver units (scale factor 10,000).",
    )

    reconciliation_id: str
    plaid_transaction_id: Any
    staged_transaction_id: str | None

    class Meta:
        db_table = "ledger_bookkeeping_reconciliation_bank_allocation"
        verbose_name = "Bookkeeping Reconciliation Bank Allocation"
        verbose_name_plural = "Bookkeeping Reconciliation Bank Allocations"
        constraints = [
            models.CheckConstraint(
                condition=(
                    Q(plaid_transaction__isnull=False, staged_transaction__isnull=True)
                    | Q(plaid_transaction__isnull=True, staged_transaction__isnull=False)
                ),
                name="check_rec_bank_alloc_target_xor",
            ),
            models.CheckConstraint(
                condition=Q(amount_units__gt=0),
                name="check_rec_bank_alloc_amount_positive",
            ),
            models.UniqueConstraint(
                fields=["reconciliation", "plaid_transaction"],
                name="uniq_rec_plaid_alloc",
            ),
            models.UniqueConstraint(
                fields=["reconciliation", "staged_transaction"],
                name="uniq_rec_staged_alloc",
            ),
        ]
        indexes = [
            models.Index(fields=["reconciliation"]),
            models.Index(fields=["plaid_transaction"]),
            models.Index(fields=["staged_transaction"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingReconciliationBankAllocation(rec={self.reconciliation}, amount={self.amount_units})"


class BookkeepingReconciliationInvalidation(models.Model):
    """
    Append-only durable record invalidating an accepted reconciliation.
    """

    id = models.CharField(max_length=255, primary_key=True)
    reconciliation = models.ForeignKey(
        "BookkeepingReconciliation",
        on_delete=models.PROTECT,
        related_name="invalidations",
    )
    reason = models.TextField(max_length=4000)
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_invalidation = models.IntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    reconciliation_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_reconciliation_invalidation"
        verbose_name = "Bookkeeping Reconciliation Invalidation"
        verbose_name_plural = "Bookkeeping Reconciliation Invalidations"
        indexes = [
            models.Index(fields=["reconciliation"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingReconciliationInvalidation({self.id}, target={self.reconciliation})"


# ======================================================================
# Residual Bank Classification Decisions & Invalidations
# ======================================================================


class BookkeepingResidualBankClassificationDecision(models.Model):
    """
    Append-only durable record of a residual bank classification decision.

    Strictly targets an authoritative StagedTransactionModel movement that remains
    unmatched after Stage 1 (payment application) and Stage 2 (reconciliation).

    Immutability is an application/writer invariant enforced by the TransitionEngine
    and persistence repository; this model does not mutate existing decisions in-place.
    """

    class StatusChoices(models.TextChoices):
        CLASSIFIED = "CLASSIFIED", "Classified"
        HOLD = "HOLD", "Hold"

    class DirectionChoices(models.TextChoices):
        INFLOW = "INFLOW", "Inflow"
        OUTFLOW = "OUTFLOW", "Outflow"

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_residual_bank_classification_decisions",
    )

    objects = models.Manager()

    staged_transaction = models.ForeignKey(
        "ledger.StagedTransactionModel",
        on_delete=models.PROTECT,
        related_name="residual_bank_classification_decisions",
    )
    bank_account = models.ForeignKey(
        "ledger.BankAccountModel",
        on_delete=models.PROTECT,
        related_name="residual_bank_classification_decisions",
    )

    status = models.CharField(
        max_length=20,
        choices=StatusChoices.choices,
    )
    account_code = models.CharField(max_length=50, null=True, blank=True)
    account = models.ForeignKey(
        "ledger.AccountModel",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="residual_bank_classification_decisions",
    )

    original_amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Original bank statement movement amount in integer solver units (scale factor 10,000).",
    )
    residual_amount_units = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Residual unmatched movement amount in integer solver units (scale factor 10,000).",
    )
    direction = models.CharField(
        max_length=20,
        choices=DirectionChoices.choices,
        help_text="Normalized cash movement direction (INFLOW or OUTFLOW).",
    )
    currency = models.CharField(max_length=3)

    confidence = models.FloatField(
        null=True,
        blank=True,
        validators=[MinValueValidator(0.0), MaxValueValidator(1.0)],
        help_text="Inference confidence reported by the model/evaluator. Policy threshold (0.98) is enforced by application logic.",
    )
    rationale = models.TextField(null=True, blank=True)
    evidence_refs = models.JSONField(default=list)
    hold_reason = models.TextField(null=True, blank=True)
    required_evidence = models.JSONField(default=list)

    # Evaluation & request provenance
    schema_version = models.CharField(max_length=100)
    dag_id = models.CharField(max_length=100)
    request_semantic_digest = models.CharField(
        max_length=64,
        help_text="SHA-256 digest of canonical semantic bank categorization request payload.",
    )
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_decision = models.IntegerField(validators=[MinValueValidator(0)])
    persistence_revision_at_decision = models.BigIntegerField(validators=[MinValueValidator(0)])
    ase_node_id = models.CharField(max_length=255, null=True, blank=True)
    terminal_property = models.CharField(max_length=255, null=True, blank=True)

    # Linear history chain
    supersedes = models.OneToOneField(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="successor",
    )

    created_at = models.DateTimeField()

    entity_id: int
    staged_transaction_id: str
    bank_account_id: str
    account_id: str | None
    supersedes_id: str | None

    class Meta:
        db_table = "ledger_bookkeeping_residual_bank_classification_decision"
        verbose_name = "Bookkeeping Residual Bank Classification Decision"
        verbose_name_plural = "Bookkeeping Residual Bank Classification Decisions"
        constraints = [
            models.CheckConstraint(
                condition=Q(status__in=["CLASSIFIED", "HOLD"]),
                name="check_res_cls_status",
            ),
            models.CheckConstraint(
                condition=Q(direction__in=["INFLOW", "OUTFLOW"]),
                name="check_res_cls_direction",
            ),
            models.CheckConstraint(
                condition=(
                    Q(status="CLASSIFIED")
                    & Q(account__isnull=False)
                    & Q(account_code__isnull=False)
                    & Q(hold_reason__isnull=True)
                    & Q(confidence__isnull=False)
                    & Q(confidence__gte=0.0)
                    & Q(confidence__lte=1.0)
                )
                | (
                    Q(status="HOLD")
                    & Q(account__isnull=True)
                    & Q(account_code__isnull=True)
                    & Q(hold_reason__isnull=False)
                    & (
                        Q(confidence__isnull=True)
                        | (Q(confidence__gte=0.0) & Q(confidence__lte=1.0))
                    )
                ),
                name="check_res_cls_status_invariants",
            ),
            models.CheckConstraint(
                condition=(
                    Q(original_amount_units__gt=0)
                    & Q(residual_amount_units__gt=0)
                    & Q(residual_amount_units__lte=models.F("original_amount_units"))
                ),
                name="check_res_cls_amounts",
            ),
            models.UniqueConstraint(
                fields=["staged_transaction"],
                condition=Q(supersedes__isnull=True),
                name="uniq_res_cls_root",
            ),
        ]
        indexes = [
            models.Index(fields=["entity", "staged_transaction"]),
            models.Index(fields=["entity", "bank_account"]),
            models.Index(fields=["staged_transaction", "request_semantic_digest"]),
            models.Index(fields=["entity", "created_at"]),
        ]

    def __str__(self) -> str:
        return (
            f"BookkeepingResidualBankClassificationDecision({self.id}, "
            f"staged={getattr(self, 'staged_transaction_id', None)}, status={self.status})"
        )


class BookkeepingResidualBankClassificationInvalidation(models.Model):
    """
    Append-only durable invalidation of a prior residual bank classification decision.

    One durable invalidation retires exactly one decision. A subsequent valid evaluation
    may append a new successor after the invalidated decision.
    """

    id = models.CharField(max_length=255, primary_key=True)
    classification = models.OneToOneField(
        "BookkeepingResidualBankClassificationDecision",
        on_delete=models.PROTECT,
        related_name="invalidation",
    )
    reason = models.TextField(max_length=4000)
    session_id = models.CharField(max_length=255, null=True, blank=True)
    state_revision_at_invalidation = models.IntegerField(validators=[MinValueValidator(0)])
    persistence_revision_at_invalidation = models.BigIntegerField(validators=[MinValueValidator(0)])
    created_at = models.DateTimeField()

    classification_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_residual_bank_classification_invalidation"
        verbose_name = "Bookkeeping Residual Bank Classification Invalidation"
        verbose_name_plural = "Bookkeeping Residual Bank Classification Invalidations"
        indexes = [  # noqa: RUF012
            models.Index(fields=["classification"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingResidualBankClassificationInvalidation({self.id}, target={self.classification_id})"


class BookkeepingResidualBankPosting(models.Model):
    """
    Immutable provenance record binding an accepted residual bank classification decision
    to its committed general ledger JournalEntry, transaction legs, and direct reconciliation closure.
    """

    id = models.CharField(max_length=255, primary_key=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="residual_bank_postings",
    )
    classification = models.OneToOneField(
        "BookkeepingResidualBankClassificationDecision",
        on_delete=models.PROTECT,
        related_name="posting",
    )
    staged_transaction = models.ForeignKey(
        "ledger.StagedTransactionModel",
        on_delete=models.PROTECT,
        related_name="residual_bank_postings",
    )
    journal_entry = models.OneToOneField(
        "ledger.JournalEntryModel",
        on_delete=models.PROTECT,
        related_name="residual_bank_posting",
    )
    bank_cash_transaction = models.OneToOneField(
        "ledger.TransactionModel",
        on_delete=models.PROTECT,
        related_name="residual_posting_bank_leg",
    )
    contra_transaction = models.OneToOneField(
        "ledger.TransactionModel",
        on_delete=models.PROTECT,
        related_name="residual_posting_contra_leg",
    )
    reconciliation = models.OneToOneField(
        "BookkeepingReconciliation",
        null=False,
        blank=False,
        on_delete=models.PROTECT,
        related_name="residual_bank_posting",
    )
    persistence_revision = models.BigIntegerField(
        validators=[MinValueValidator(1)],
        help_text="Authoritative persistence revision when this posting was committed.",
    )
    posted_at = models.DateTimeField()

    entity_id: int
    classification_id: str
    staged_transaction_id: str
    journal_entry_id: Any
    bank_cash_transaction_id: Any
    contra_transaction_id: Any
    reconciliation_id: str

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_residual_bank_posting"
        verbose_name = "Bookkeeping Residual Bank Posting"
        verbose_name_plural = "Bookkeeping Residual Bank Postings"
        indexes = [  # noqa: RUF012
            models.Index(fields=["entity", "posted_at"]),
            models.Index(fields=["staged_transaction"]),
        ]
        constraints = [  # noqa: RUF012
            models.CheckConstraint(
                condition=models.Q(persistence_revision__gt=0),
                name="check_res_bank_posting_persistence_rev_positive",
            ),
            models.CheckConstraint(
                condition=~models.Q(bank_cash_transaction=models.F("contra_transaction")),
                name="check_res_bank_posting_distinct_tx_legs",
            ),
        ]

    def __str__(self) -> str:
        return f"BookkeepingResidualBankPosting({self.id})"


# ======================================================================
# Inbound Document Intake & Accounting Provenance
# ======================================================================


class BookkeepingDocumentIntake(models.Model):
    """
    Durable provenance and idempotency claim ledger for inbound documents.

    Ensures that any document processed by the perception pipeline (Go OCR)
    is converted into Django accounting authority at most once.
    """

    STATUS_PENDING = "PENDING"
    STATUS_PROCESSED = "PROCESSED"
    STATUS_NEEDS_REVIEW = "NEEDS_REVIEW"
    STATUS_FAILED = "FAILED"

    STATUS_CHOICES = [
        (STATUS_PENDING, "Pending"),
        (STATUS_PROCESSED, "Processed"),
        (STATUS_NEEDS_REVIEW, "Needs Review"),
        (STATUS_FAILED, "Failed"),
    ]

    id = models.UUIDField(primary_key=True, default=uuid4, editable=False)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="document_intakes",
    )
    source_document_id = models.UUIDField(
        unique=True,
        db_index=True,
        help_text="UUID referencing toro_core.documents.id",
    )
    source_session_id = models.CharField(max_length=255, blank=True, default="")
    source_message_id = models.CharField(max_length=255, blank=True, default="")
    doc_type = models.CharField(max_length=50, blank=True, default="")
    status = models.CharField(
        max_length=20,
        choices=STATUS_CHOICES,
        default=STATUS_PENDING,
        db_index=True,
    )
    accounting_artifact_type = models.CharField(
        max_length=50,
        blank=True,
        default="",
        help_text="e.g. IMPORT_JOB, BILL, SUPPORTING_DOCUMENT, NONE",
    )
    accounting_artifact_id = models.CharField(
        max_length=255,
        blank=True,
        default="",
        help_text="Primary key of created accounting record",
    )
    error_code = models.CharField(max_length=100, blank=True, default="")
    error_detail = models.TextField(blank=True, default="")
    metadata = models.JSONField(blank=True, default=dict)
    created_at = models.DateTimeField(auto_now_add=True)
    processed_at = models.DateTimeField(null=True, blank=True)

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_document_intake"
        verbose_name = "Bookkeeping Document Intake"
        verbose_name_plural = "Bookkeeping Document Intakes"
        indexes = [
            models.Index(fields=["entity", "status"]),
            models.Index(fields=["source_document_id"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingDocumentIntake({self.source_document_id}, status={self.status})"


# ======================================================================
# Entity Bookkeeping Trigger & Coalescing State
# ======================================================================


class BookkeepingEntityTrigger(models.Model):
    """
    Durable entity-level trigger coalescing state for BookkeepingSession execution.

    Guarantees:
    - Bursts of triggers for the same entity coalesce into a single execution.
    - Different entities execute independently.
    - Triggers arriving during an active session execution are not lost (marked pending).
    - Prevents concurrent sessions on the same entity (avoiding OCC revision lock collisions).
    """

    entity = models.OneToOneField(
        "ledger.EntityModel",
        primary_key=True,
        on_delete=models.CASCADE,
        related_name="bookkeeping_trigger_state",
    )
    pending = models.BooleanField(default=False, db_index=True)
    running = models.BooleanField(default=False, db_index=True)
    last_trigger_id = models.CharField(max_length=255, blank=True, default="")
    last_trigger_source = models.CharField(max_length=100, blank=True, default="")
    last_triggered_at = models.DateTimeField(null=True, blank=True)
    last_run_at = models.DateTimeField(null=True, blank=True)
    last_run_session_id = models.CharField(max_length=255, blank=True, default="")
    updated_at = models.DateTimeField(auto_now=True)

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_entity_trigger"
        verbose_name = "Bookkeeping Entity Trigger"
        verbose_name_plural = "Bookkeeping Entity Triggers"

    def __str__(self) -> str:
        return f"BookkeepingEntityTrigger(entity={self.entity}, pending={self.pending}, running={self.running})"

    @classmethod
    def record_trigger(cls, entity: Any, trigger_id: str, source: str) -> "BookkeepingEntityTrigger":
        with transaction.atomic():
            obj, _ = cls.objects.select_for_update().get_or_create(
                entity=entity,
                defaults={
                    "pending": True,
                    "running": False,
                    "last_trigger_id": trigger_id,
                    "last_trigger_source": source,
                    "last_triggered_at": timezone.now(),
                },
            )
            obj.pending = True
            obj.last_trigger_id = trigger_id
            obj.last_trigger_source = source
            obj.last_triggered_at = timezone.now()
            obj.save(update_fields=["pending", "last_trigger_id", "last_trigger_source", "last_triggered_at", "updated_at"])
            return obj


# ======================================================================
# Bookkeeping Notification Delivery & Idempotency
# ======================================================================


class BookkeepingNotificationDelivery(models.Model):
    """
    Durable record ensuring exactly-once notification delivery for unresolved bookkeeping events.
    Keyed by (event_type, event_id).
    """

    EVENT_TYPE_RESIDUAL_HOLD = "residual_hold"
    EVENT_TYPE_DOCUMENT_NEEDS_REVIEW = "document_needs_review"
    EVENT_TYPE_CHOICES = [
        (EVENT_TYPE_RESIDUAL_HOLD, "Residual Hold"),
        (EVENT_TYPE_DOCUMENT_NEEDS_REVIEW, "Document Needs Review"),
    ]

    STATUS_SENT = "SENT"
    STATUS_FAILED = "FAILED"
    STATUS_SKIPPED = "SKIPPED"
    STATUS_CHOICES = [
        (STATUS_SENT, "Sent"),
        (STATUS_FAILED, "Failed"),
        (STATUS_SKIPPED, "Skipped"),
    ]

    event_type = models.CharField(max_length=64, choices=EVENT_TYPE_CHOICES)
    event_id = models.CharField(max_length=255, db_index=True)
    entity = models.ForeignKey(
        "ledger.EntityModel",
        on_delete=models.CASCADE,
        related_name="bookkeeping_notifications",
    )
    recipient = models.EmailField()
    subject = models.CharField(max_length=255)
    body = models.TextField()
    postmark_message_id = models.CharField(max_length=255, blank=True, default="")
    status = models.CharField(max_length=32, choices=STATUS_CHOICES, default=STATUS_SENT)
    error_message = models.TextField(blank=True, default="")
    sent_at = models.DateTimeField(auto_now_add=True)

    objects = models.Manager()

    class Meta:
        db_table = "ledger_bookkeeping_notification_delivery"
        verbose_name = "Bookkeeping Notification Delivery"
        verbose_name_plural = "Bookkeeping Notification Deliveries"
        constraints = [
            models.UniqueConstraint(
                fields=["event_type", "event_id"],
                name="unique_bookkeeping_notification_event",
            )
        ]
        indexes = [
            models.Index(fields=["entity", "event_type"]),
        ]

    def __str__(self) -> str:
        return f"BookkeepingNotificationDelivery({self.event_type}:{self.event_id}, status={self.status}, to={self.recipient})"

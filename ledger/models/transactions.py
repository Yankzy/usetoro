"""


The TransactionModel serves as the foundational accounting entity where all financial transactions are recorded.
Every transaction must be associated with a JournalEntryModel, which represents a collection
of related transactions. This strict association ensures that standalone TransactionModels—or orphan transactions—do not
exist, a constraint enforced at the database level.

Each transaction performs either a CREDIT or a DEBIT operation on the designated AccountModel, upholding standard
accounting principles. The TransactionModel API integrates the IOMixIn, a critical component for generating financial
statements. This mixin facilitates efficient querying and aggregation directly at the database level, eliminating the need
to load all TransactionModels into memory. This database-driven approach significantly improves performance and simplifies
the process of generating accurate financial reports.

The TransactionModel, together with the IOMixIn, is essential for ensuring seamless, efficient, and reliable
financial statement production in the Ledger framework.
"""

from typing import Any, List, Optional, Set, Dict, Tuple, Union
from uuid import uuid4, UUID
from django.contrib.auth import get_user_model
from django.core.exceptions import ValidationError
from django.core.validators import MinValueValidator
from django.db import models
from django.db.models import Q, F, QuerySet, Manager, Prefetch
from django.db.models.signals import pre_save
from django.utils.translation import gettext_lazy as _
from ledger.io.io_core import validate_io_timestamp
from ledger.models import AccountModel, BillModel, EntityModel, InvoiceModel, LedgerModel
from ledger.models.mixins import CreateUpdateMixIn
from ledger.models.unit import EntityUnitModel
from ledger.models.utils import lazy_loader
import re, logging, json
from datetime import date, datetime, time
from django.utils.dateparse import parse_datetime, parse_date, parse_time
from decimal import Decimal, InvalidOperation



logger = logging.getLogger(__name__)


UserModel = get_user_model()


class TransactionModelValidationError(ValidationError):
    pass


class TransactionModelQuerySet(QuerySet):
    """
    A custom QuerySet class tailored for `TransactionModel` objects. It includes a collection
    of methods to efficiently and safely retrieve and filter transactions from the database
    based on common use cases.
    """

    def posted(self) -> QuerySet:
        """
        Retrieves transactions that are part of a posted journal entry and ledger.

        A transaction is considered "posted" if:
        - It belongs to a journal entry marked as *posted*.
        - Its associated journal entry is part of a ledger marked as *posted*.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet containing only transactions that meet the "posted" criteria.
        """
        return self.filter(
            Q(journal_entry__posted=True) &
            Q(journal_entry__ledger__posted=True)
        )

    def for_accounts(self, account_list: List[Union[AccountModel, str, UUID]]):
        """
        Filters transactions based on the accounts they are associated with.

        Parameters
        ----------
        account_list : list of str or AccountModel
            A list containing account codes (strings) or `AccountModel` instances.
            Transactions will be filtered to match these accounts.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet filtered for transactions associated with the specified accounts.
        """

        if not isinstance(account_list, list) or not len(account_list) > 0:
            raise TransactionModelValidationError(
                message=_('Account list must be a list of AccountModel, UUID or str objects (codes).')
            )
        if isinstance(account_list[0], str):
            return self.filter(account__code__in=account_list)
        elif isinstance(account_list[0], UUID):
            return self.filter(account__uuid__in=account_list)
        elif isinstance(account_list[0], AccountModel):
            return self.filter(account__in=account_list)
        raise TransactionModelValidationError(
            message=_('Account list must be a list of AccountModel, UUID or str objects (codes).')
        )

    def for_roles(self, role_list: Union[str, List[str], Set[str]]):
        """
        Fetches a QuerySet of TransactionModels which AccountModel has a specific role.

        Parameters
        ----------
        role_list: str or list
            A string or list of strings representing the roles to be used as filter.

        Returns
        -------
        TransactionModelQuerySet
            Returns a TransactionModelQuerySet with applied filters.
        """
        if isinstance(role_list, str):
            return self.filter(account__role__in=[role_list])
        return self.filter(account__role__in=role_list)

    def for_unit(self, unit_slug: Union[str, EntityUnitModel]):
        """
        Filters transactions based on their associated entity unit.

        Parameters
        ----------
        unit_slug : str or EntityUnitModel
            A string representing the slug of the entity unit or an `EntityUnitModel` instance.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet filtered for transactions linked to the specified unit.
        """
        if isinstance(unit_slug, EntityUnitModel):
            return self.filter(journal_entry__entity_unit=unit_slug)
        return self.filter(journal_entry__entity_unit__slug__exact=unit_slug)

    def for_activity(self, activity_list: Union[str, List[str], Set[str]]):
        """
        Filters transactions based on their associated activity or activities.

        Parameters
        ----------
        activity_list : str or list of str
            A single activity or a list of activities to filter transactions by.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet filtered for transactions linked to the specified activity or activities.
        """
        if isinstance(activity_list, str):
            return self.filter(journal_entry__activity__in=[activity_list])
        return self.filter(journal_entry__activity__in=activity_list)

    def to_date(self, to_date: Union[str, date, datetime]):
        """
        Filters transactions occurring on or before a specific date or timestamp.

        If `to_date` is a naive datetime (no timezone), it is assumed to be in local time
        based on Django settings.

        Parameters
        ----------
        to_date : str, date, or datetime
            The maximum date or timestamp for filtering. When using a date (not datetime),
            the filter is inclusive (e.g., "2022-12-20" includes all transactions from that day).

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet filtered to include transactions up to the specified date or timestamp.
        """

        if isinstance(to_date, str):
            to_date = validate_io_timestamp(to_date)

        if isinstance(to_date, date):
            return self.filter(journal_entry__timestamp__date__lte=to_date)
        return self.filter(journal_entry__timestamp__lte=to_date)

    def from_date(self, from_date: Union[str, date, datetime]):
        """
        Filters transactions occurring on or after a specific date or timestamp.

        If `from_date` is a naive datetime (no timezone), it is assumed to be in local time
        based on Django settings.

        Parameters
        ----------
        from_date : str, date, or datetime
            The minimum date or timestamp for filtering. When using a date (not datetime),
            the filter is inclusive (e.g., "2022-12-20" includes all transactions from that day).

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet filtered to include transactions from the specified date or timestamp onwards.
        """
        if isinstance(from_date, str):
            from_date = validate_io_timestamp(from_date)

        if isinstance(from_date, date):
            return self.filter(journal_entry__timestamp__date__gte=from_date)

        return self.filter(journal_entry__timestamp__gte=from_date)

    def not_closing_entry(self):
        """
        Filters transactions that are *not* part of a closing journal entry.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet with transactions where the `journal_entry__is_closing_entry` field is False.
        """
        return self.filter(journal_entry__is_closing_entry=False)

    def is_closing_entry(self):
        """
        Filters transactions that are part of a closing journal entry.

        Returns
        -------
        TransactionModelQuerySet
            A QuerySet with transactions where the `journal_entry__is_closing_entry` field is True.
        """
        return self.filter(journal_entry__is_closing_entry=True)

    def for_ledger(self, ledger_model: Union[LedgerModel, UUID, str]):
        """
        Filters transactions for a specific ledger under a given entity.

        Parameters
        ----------
        ledger_model : Union[LedgerModel, UUID]
            The ledger model or its UUID to filter by.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions associated with the given ledger and entity.
        """
        if isinstance(ledger_model, UUID):
            return self.filter(journal_entry__ledger__uuid__exact=ledger_model)
        return self.filter(journal_entry__ledger=ledger_model)

    def for_journal_entry(self, je_model):
        """
        Filters transactions for a specific journal entry under a given ledger and entity.

        Parameters
        ----------
        je_model : Union[JournalEntryModel, UUID]
            The journal entry model or its UUID to filter by.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions associated with the given journal entry.
        """
        if isinstance(je_model, lazy_loader.get_journal_entry_model()):
            return self.filter(journal_entry=je_model)
        return self.filter(journal_entry__uuid__exact=je_model)

    def for_bill(self, bill_model: Union[BillModel, str, UUID]):
        """
        Filters transactions for a specific bill under a given entity.

        Parameters
        ----------
        bill_model : Union[BillModel, str, UUID]
            The bill model or its UUID to filter by.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions related to the specified bill.
        """
        if isinstance(bill_model, BillModel):
            return self.filter(journal_entry__ledger__billmodel=bill_model)
        return self.filter(journal_entry__ledger__billmodel__uuid__exact=bill_model)

    def for_invoice(self, invoice_model: Union[InvoiceModel, str, UUID]):
        """
        Filters transactions for a specific invoice under a given entity.

        Parameters
        ----------
        invoice_model : Union[InvoiceModel, str, UUID]
            The invoice model or its UUID to filter by.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions related to the specified invoice.
        """
        if isinstance(invoice_model, InvoiceModel):
            return self.filter(journal_entry__ledger__invoicemodel=invoice_model)
        return self.filter(journal_entry__ledger__invoicemodel__uuid__exact=invoice_model)

    def with_annotated_details(self):
        return self.annotate(
            entity_unit_name=F('journal_entry__entity_unit__name'),
            account_code=F('account__code'),
            account_name=F('account__name'),
            timestamp=F('journal_entry__timestamp'),
        )

    def is_cleared(self):
        return self.filter(cleared=True)

    def not_cleared(self):
        return self.filter(cleared=False)

    def is_reconciled(self):
        return self.filter(reconciled=True)

    def not_reconciled(self):
        return self.filter(reconciled=False)


class TransactionModelManager(Manager):
    """
    A custom manager for `TransactionModel` designed to add helper methods for
    querying and filtering `TransactionModel` objects efficiently based on use cases like
    user permissions, associated entities, ledgers, journal entries, and more.

    This manager leverages `TransactionModelQuerySet` for complex query construction and
    integrates advanced filtering options based on user roles, entities, and other relationships.
    """

    def get_queryset(self) -> TransactionModelQuerySet:
        """
        Retrieves the base queryset for `TransactionModel`, annotated and pre-loaded
        with commonly used related fields.

        Returns
        -------
        TransactionModelQuerySet
            A custom queryset with essential annotations and relationships preloaded.
        """
        qs = TransactionModelQuerySet(self.model, using=self._db)
        return qs.annotate(
            timestamp=F('journal_entry__timestamp'),
            _coa_id=F('account__coa_model_id')  # Annotates the `coa_model_id` from the related `account`.
        ).select_related(
            'journal_entry',  # Pre-loads the related Journal Entry.
            'account',  # Pre-loads the Account associated with the Transaction.
            'account__coa_model',  # Pre-loads the Chart of Accounts related to the Account.
        )

    def for_user(self, user_model) -> TransactionModelQuerySet:
        """
        Filters transactions accessible to a specific user based on their permissions.

        Parameters
        ----------
        user_model : UserModel
            The user object for which the transactions should be filtered.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions filtered by the user's access level.

        Description
        -----------
        - Returns all `TransactionModel` objects for superusers.
        - For regular users, it filters transactions where:
          - The user is an admin of the entity associated with the ledger in the transaction.
          - The user is a manager of the entity associated with the ledger in the transaction.
        """
        qs = self.get_queryset()
        return qs.filter(
            Q(journal_entry__ledger__entity__admin=user_model) |
            Q(journal_entry__ledger__entity__managers__in=[user_model])
        )

    def for_entity(self,
                   entity_slug: Union[EntityModel, str, UUID],
                   user_model: Optional[UserModel] = None) -> TransactionModelQuerySet:
        """
        Filters transactions for a specific entity, optionally scoped to a specific user.

        Parameters
        ----------
        entity_slug : Union[EntityModel, str, UUID]
            Identifier for the entity. This can be an `EntityModel` object, a slug (str), or a UUID.
        user_model : Optional[UserModel], optional
            The user for whom transactions should be filtered. If provided, applies user-specific
            filtering. Defaults to None.

        Returns
        -------
        TransactionModelQuerySet
            A queryset containing transactions associated with the specified entity.

        Notes
        -----
        - If `user_model` is provided, only transactions accessible by the user are included.
        - Supports flexible filtering by accepting different forms of `entity_slug`.
        """
        if user_model:
            qs = self.for_user(user_model=user_model)
        else:
            qs = self.get_queryset()

        if isinstance(entity_slug, EntityModel):
            return qs.filter(journal_entry__ledger__entity=entity_slug)
        elif isinstance(entity_slug, UUID):
            return qs.filter(journal_entry__ledger__entity_id=entity_slug)
        return qs.filter(journal_entry__ledger__entity__slug__exact=entity_slug)


class TransactionModelAbstract(CreateUpdateMixIn):
    """
    Abstract model for representing a financial transaction in the ledger system.

    This model defines the core structure and behavior that every transaction record is
    expected to have, including fields like transaction type, associated account, amount,
    and additional metadata used for validation and functionality.

    Attributes:
    -----------
    Constants:
    - CREDIT: Constant representing a credit transaction.
    - DEBIT: Constant representing a debit transaction.
    - TX_TYPE: A list of choices providing options for transaction types, including CREDIT and DEBIT.

    Fields:
    - uuid (UUIDField): The unique identifier for the transaction. Automatically generated, non-editable, and primary key.
    - tx_type (CharField): Specifies the transaction type (CREDIT or DEBIT). Choices are based on the TX_TYPE constant. Maximum length is 10 characters.
    - journal_entry (ForeignKey): References the related journal entry from the `ledger.JournalEntryModel`.
      This field is not editable and is essential for linking transactions to journal entries.
    - account (ForeignKey): References the associated account from `ledger.AccountModel`. Protected from being deleted.
    - amount (DecimalField): Represents the transaction amount, up to 20 digits and 2 decimal places.
      The default value is 0.00, and it enforces a minimum value of 0.
    - description (CharField): Optional field for a brief description of the transaction.
      The maximum length is 100 characters.
    - cleared (BooleanField): Indicates whether the transaction has been cleared. Defaults to False.
    - reconciled (BooleanField): Indicates whether the transaction has been reconciled. Defaults to False.
    - objects (TransactionModelManager): Custom model manager providing advanced helper methods for querying and filtering transactions.
    """

    CREDIT = 'credit'
    DEBIT = 'debit'
    TX_TYPE = [
        (CREDIT, _('Credit')),
        (DEBIT, _('Debit'))
    ]

    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    tx_type = models.CharField(max_length=10, choices=TX_TYPE, verbose_name=_('Transaction Type'))

    journal_entry = models.ForeignKey(
        'ledger.JournalEntryModel',
        editable=False,
        verbose_name=_('Journal Entry'),
        help_text=_('Journal Entry to be associated with this transaction.'),
        on_delete=models.CASCADE
    )
    account = models.ForeignKey(
        'ledger.AccountModel',
        verbose_name=_('Account'),
        help_text=_('Account from Chart of Accounts to be associated with this transaction.'),
        on_delete=models.PROTECT
    )
    amount = models.DecimalField(
        decimal_places=2,
        max_digits=20,
        default=0.00,
        verbose_name=_('Amount'),
        help_text=_('Amount of the transaction.'),
        validators=[MinValueValidator(0)]
    )
    description = models.CharField(
        max_length=100,
        null=True,
        blank=True,
        verbose_name=_('Transaction Description'),
        help_text=_('A description to be included with this individual transaction.')
    )
    cleared = models.BooleanField(default=False, verbose_name=_('Cleared'))
    reconciled = models.BooleanField(default=False, verbose_name=_('Reconciled'))
    objects = TransactionModelManager()

    class Meta:
        abstract = True
        ordering = ['-created']
        verbose_name = _('Transaction')
        verbose_name_plural = _('Transactions')
        indexes = [
            models.Index(fields=['tx_type']),
            models.Index(fields=['account']),
            models.Index(fields=['journal_entry']),
            models.Index(fields=['created']),
            models.Index(fields=['updated']),
            models.Index(fields=['cleared']),
            models.Index(fields=['reconciled']),
        ]

    def __str__(self):
        return '{code}-{name}/{balance_type}: {amount}/{tx_type}'.format(
            code=self.account.code,
            name=self.account.name,
            balance_type=self.account.balance_type,
            amount=self.amount,
            tx_type=self.tx_type
        )

    @property
    def coa_id(self):
        """
        Fetch the Chart of Accounts (CoA) ID associated with the transaction's account.
        Returns `None` if the account is not set.
        """
        try:
            return getattr(self, '_coa_id')
        except AttributeError:
            if self.account is None:
                return None
            return self.account.coa_model_id

    def is_debit(self):
        return self.tx_type == self.DEBIT

    def is_credit(self):
        return self.tx_type == self.CREDIT


class TransactionModel(TransactionModelAbstract):
    """
    Base Transaction Model From Abstract.
    """

    class Meta(TransactionModelAbstract.Meta):
        abstract = False


def transactionmodel_presave(instance: TransactionModel, **kwargs):
    """
    Pre-save validation for the TransactionModel instance.

    This function is executed before saving a `TransactionModel` instance,
    ensuring that certain conditions are met to maintain data integrity.

    Parameters
    ----------
    instance : TransactionModel
        The `TransactionModel` instance that is about to be saved.
    kwargs : dict
        Additional keyword arguments, such as the optional `bypass_account_state`.

    Validations
    -----------
    The function performs the following validations:
    1. **Account Transactionality**:
       If the `bypass_account_state` flag is not provided or set to `False`,
       it verifies whether the associated account can process transactions
       by calling `instance.account.can_transact()`. If the account cannot
       process transactions, the save operation is interrupted to prevent
       invalid data.

    2. **Journal Entry Lock**:
       If the associated journal entry (`instance.journal_entry`) is locked,
       the transaction cannot be modified. The save process is halted if the
       journal entry is marked as locked.

    Raises
    ------
    TransactionModelValidationError
        Raised in the following scenarios:
        - **Account Transactionality Failure**:
          When `bypass_account_state` is `False` or not provided, and the
          associated account (`instance.account`) cannot process transactions.
          The exception contains a message identifying the account.

        - **Locked Journal Entry**:
          When the associated journal entry (`instance.journal_entry`) is locked,
          preventing modification of any related transactions. The error message
          describes the locked journal entry constraint.

    Example
    -------
    ```python
    instance = TransactionModel(...)
    try:
        transactionmodel_presave(instance)
        instance.save()  # Save proceeds if no validation error occurs
    except TransactionModelValidationError as e:
        handle_error(str(e))  # Handle validation exception
    ```
    """
    bypass_account_state = kwargs.get('bypass_account_state', False)

    if instance.account_id and instance.account.is_root_account():
        raise TransactionModelValidationError(
            message=_('Transactions cannot be linked to root accounts.')
        )

    if all([
        not bypass_account_state,
        not instance.account.can_transact()
    ]):
        raise TransactionModelValidationError(
            message=_(f'Cannot create or modify transactions on account model {instance.account}.')
        )
    if instance.journal_entry.is_locked():
        raise TransactionModelValidationError(
            message=_('Cannot modify transactions on locked journal entries.')
        )


pre_save.connect(transactionmodel_presave, sender=TransactionModel)

# Models for imported transactions pointing to their s3 files


class ImportedJobModel(models.Model):
    """
    Model for imported jobs.
    """

    class StatusChoices(models.TextChoices):
        PENDING = 'pending', 'Pending'
        PROCESSING = 'processing', 'Processing'
        COMPLETED = 'completed', 'Completed'
        FAILED = 'failed', 'Failed'

    class FileTypeChoices(models.TextChoices):
        CSV = 'csv', 'csv'
        XLSX = 'xlsx', 'xlsx'
        XLS = 'xls', 'xls'
        PDF = 'pdf', 'pdf'
        DOCX = 'docx', 'docx'
        DOC = 'doc', 'doc'
        TXT = 'txt', 'txt'
        JSON = 'json', 'json'
        XML = 'xml', 'xml'
        HTML = 'html', 'html'

    class SourceChoices(models.TextChoices):
        BANK_STATEMENT = 'bank_statement', 'bank Statement'
        CREDIT_CARD_STATEMENT = 'credit_card_statement', 'credit card statement'
        DEBIT_CARD_STATEMENT = 'debit_card_statement', 'debit card statement'
        QUICKBOOKS = 'quickbooks', 'quickbooks'
        SAP = 'sap', 'sap'
        XEROX = 'xerox', 'xerox'
        OTHER = 'other', 'other'

    class OperationTypeChoices(models.TextChoices):
        BANK_STATEMENT = 'bank_statement', 'bank statement'
        RECEIPT = 'receipt', 'receipt'
        INVOICE = 'invoice', 'invoice'
        TRANSACTION_UPLOAD = 'transaction_upload', 'transaction upload'
        DEPOSIT = 'deposit', 'deposit'
        WITHDRAWAL = 'withdrawal', 'withdrawal'
        TRANSFER = 'transfer', 'transfer'
        PAYMENT = 'payment', 'payment'
        
    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    entity = models.ForeignKey('ledger.EntityModel', on_delete=models.CASCADE)
    source = models.CharField(max_length=255, choices=SourceChoices.choices, null=False)
    file_type = models.CharField(max_length=255, choices=FileTypeChoices.choices, null=False)
    operation_type = models.CharField(max_length=255, choices=OperationTypeChoices.choices, null=False)
    file_name = models.CharField(max_length=255)
    status = models.CharField(max_length=255, choices=StatusChoices.choices, default=StatusChoices.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)


    def __str__(self):
        return f'{self.file_name} -> {self.file_type} -> {self.operation_type}'

    class Meta:
        ordering = ['-created_at']
        indexes = [
            models.Index(fields=['entity']),
        ]
        verbose_name = _('Imported Job')
        verbose_name_plural = _('Imported Jobs')


class ImportedTransactionModel(models.Model):
    """
    Model for imported transactions that are yet to be reconciled and categorized in the TransactionModel.
    """

    class StatusChoices(models.TextChoices):
        PENDING = 'pending', 'Pending'
        PROCESSING = 'processing', 'Processing'
        COMPLETED = 'completed', 'Completed'
        FAILED = 'failed', 'Failed'

    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    imported_job = models.ForeignKey(ImportedJobModel, on_delete=models.CASCADE, null=False, blank=False)
    mapping = models.JSONField(default=dict)
    status = models.CharField(max_length=255, choices=StatusChoices.choices, default=StatusChoices.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f'{self.imported_job.file_name} -> {self.status}'

    class Meta:
        ordering = ['-created_at']
        indexes = [
            models.Index(fields=['imported_job']),
        ]
        verbose_name = _('Imported Transaction')
        verbose_name_plural = _('Imported Transactions')

# RuleEngine models for transaction categorization in Django
# ----------------------------------------------------------
# These models allow for highly flexible, explainable, and trackable rule-based bookkeeping automation.
# Each RuleGroup specifies a logic/priority and belongs to many RuleConditions.
# Audit log captures what rules were applied to which transactions and for what reason (full "why" transparency).



class RuleCondition(models.Model):
    """
    Defines a single, atomic condition for matching a transaction.
    Robust, well-typed, and validated. Operators and fields map to clear handlers.
    """

    class FieldChoices(models.TextChoices):
        DESCRIPTION = 'description', 'Description'
        VENDOR = 'vendor', 'Vendor'
        CUSTOMER = 'customer', 'Customer'
        MEMO = 'memo', 'Memo'
        AMOUNT = 'amount', 'Amount'
        ROLE = 'role', 'Account Role'
        UUID = 'uuid', 'Account UUID'
        DATE = 'date', 'Transaction Date'
        TIME = 'time', 'Transaction Time'
        MCC = 'mcc', 'Merchant Category Code'
        CATEGORY = 'category', 'Bank Category'
        INVOICE_TEXT = 'invoice_text', 'Invoice/Receipt Text'

    class OperatorChoices(models.TextChoices):
        # String Operators (Case-Insensitive)
        CONTAINS = 'contains', 'Contains'
        NOT_CONTAINS = 'not_contains', 'Does Not Contain'
        EQUALS = 'equals', 'Equals'
        IN = 'in', 'In List'
        NOT_IN = 'not_in', 'Not In List'
        REGEX = 'regex', 'Regex'
        STARTSWITH = 'startswith', 'Starts With'
        ENDSWITH = 'endswith', 'Ends With'
        # String Operators (Case-Sensitive)
        CONTAINS_CS = 'contains_cs', 'Contains (Case-Sensitive)'
        EQUALS_CS = 'equals_cs', 'Equals (Case-Sensitive)'
        # Existence
        IS_NULL = 'is_null', 'Is Null'
        IS_NOT_NULL = 'is_not_null', 'Is Not Null'
        # Numeric/Date Operators
        GT = 'gt', 'Greater Than'
        GTE = 'gte', 'Greater Than or Equal' # NEW
        LT = 'lt', 'Less Than'
        LTE = 'lte', 'Less Than or Equal' # NEW

    group = models.ForeignKey('RuleGroup', related_name="conditions", on_delete=models.CASCADE)
    field = models.CharField(max_length=32, choices=FieldChoices.choices)
    operator = models.CharField(max_length=16, choices=OperatorChoices.choices)
    value = models.TextField(blank=True, help_text="Condition value. Use JSON array for IN/NOT_IN, e.g., [\"val1\", \"val2\"]")

    # --- Validation ----------------------------------------------------------
    def clean(self):
        """
        Validate configuration early so misconfigured rules do not silently fail at runtime.
        """
        # Existence checks don't need a value
        if self.operator in {self.OperatorChoices.IS_NULL, self.OperatorChoices.IS_NOT_NULL}:
            return

        numeric_ops = {
            self.OperatorChoices.GT, self.OperatorChoices.GTE,
            self.OperatorChoices.LT, self.OperatorChoices.LTE,
            self.OperatorChoices.EQUALS
        }

        # For numeric comparisons, ensure value can be converted to float
        if self.field == self.FieldChoices.AMOUNT and self.operator in numeric_ops:
            try:
                Decimal(self.value)
            except (TypeError, ValueError, InvalidOperation):
                raise ValidationError({'value': "For numeric comparisons on 'amount', 'value' must be a valid number."})

        # For date/time fields, ensure parseable
        if self.field == self.FieldChoices.DATE and self.operator in numeric_ops:
            if not self._safe_parse_date(self.value):
                raise ValidationError({'value': "For date comparisons, 'value' must be an ISO date or datetime string."})

        if self.field == self.FieldChoices.TIME and self.operator in numeric_ops:
            if not self._safe_parse_time(self.value):
                raise ValidationError({'value': "For time comparisons, 'value' must be a time string (HH:MM[:SS])."})

        # JSON validation for IN/NOT_IN operators
        if self.operator in {self.OperatorChoices.IN, self.OperatorChoices.NOT_IN}:
            try:
                parsed_value = json.loads(self.value)
                if not isinstance(parsed_value, list):
                    raise ValidationError({'value': "For 'IN'/'NOT IN' operators, the value must be a valid JSON list."})
            except json.JSONDecodeError:
                raise ValidationError({'value': 'Value must be a valid JSON-formatted list, e.g., ["apple", "banana"]'})

        # Regex sanity check
        if self.operator == self.OperatorChoices.REGEX and self.value:
            try:
                re.compile(self.value)
            except re.error as exc:
                raise ValidationError({'value': f"Invalid regex pattern: {exc}"})

    # --- Helpers for parsing -----------------------------------------------
    @staticmethod
    def _safe_parse_date(value: str) -> Optional[date]:
        if not value: return None
        dt = parse_datetime(value)
        return dt.date() if dt else parse_date(value)

    @staticmethod
    def _safe_parse_time(value: str) -> Optional[time]:
        if not value: return None
        return parse_time(value)

    # Normalize IN/NOT_IN comparison set from a JSON list
    @staticmethod
    def _normalize_json_list_value(value: str) -> Set[str]:
        try:
            items = json.loads(value)
            return {str(item).strip().lower() for item in items if str(item).strip()}
        except (json.JSONDecodeError, TypeError):
            return set()

    # --- Evaluation entry point --------------------------------------------
    def evaluate(self, tx: Any) -> bool:
        """
        Robustly evaluates the condition against a transaction with proper type handling.
        Returns True/False, never raises for bad data. Misconfigured conditions are logged.
        """
        tx_val = getattr(tx, self.field, None)
        op = self.operator

        if op == self.OperatorChoices.IS_NULL: return tx_val is None
        if op == self.OperatorChoices.IS_NOT_NULL: return tx_val is not None

        if tx_val is None: return False

        try:
            if self.field == self.FieldChoices.AMOUNT:
                return self._evaluate_numeric(tx_val)
            if self.field == self.FieldChoices.DATE:
                return self._evaluate_date(tx_val)
            if self.field == self.FieldChoices.TIME:
                return self._evaluate_time(tx_val)
            return self._evaluate_string(tx_val)
        except Exception as exc:
            logger.exception("RuleCondition.evaluate error: %s (condition id=%s)", exc, getattr(self, 'pk', None))
            return False

    # --- Type-specific evaluators -----------------------------------------
    def _evaluate_numeric(self, tx_val: Any) -> bool:
        try:
            val = Decimal(tx_val)
            cmp = Decimal(self.value)
        except (ValueError, TypeError, InvalidOperation) as exc:
            logger.debug("Decimal conversion failed: %s (tx_val=%r, value=%r)", exc, tx_val, self.value)
            return False

        op_map = {
            self.OperatorChoices.GT: val > cmp,
            self.OperatorChoices.GTE: val >= cmp, # NEW
            self.OperatorChoices.LT: val < cmp,
            self.OperatorChoices.LTE: val <= cmp, # NEW
            self.OperatorChoices.EQUALS: val == cmp
        }
        return op_map.get(self.operator, False)

    def _evaluate_date(self, tx_val: Any) -> bool:
        val_date = tx_val.date() if isinstance(tx_val, datetime) else (tx_val if isinstance(tx_val, date) else self._safe_parse_date(str(tx_val)))
        cmp_date = self._safe_parse_date(self.value)
        if val_date is None or cmp_date is None: return False

        op_map = {
            self.OperatorChoices.GT: val_date > cmp_date,
            self.OperatorChoices.GTE: val_date >= cmp_date, # NEW
            self.OperatorChoices.LT: val_date < cmp_date,
            self.OperatorChoices.LTE: val_date <= cmp_date, # NEW
            self.OperatorChoices.EQUALS: val_date == cmp_date
        }
        return op_map.get(self.operator, False)

    def _evaluate_time(self, tx_val: Any) -> bool:
        val_time = tx_val.time() if isinstance(tx_val, datetime) else (tx_val if isinstance(tx_val, time) else self._safe_parse_time(str(tx_val)))
        cmp_time = self._safe_parse_time(self.value)
        if val_time is None or cmp_time is None: return False

        op_map = {
            self.OperatorChoices.GT: val_time > cmp_time,
            self.OperatorChoices.GTE: val_time >= cmp_time, # NEW
            self.OperatorChoices.LT: val_time < cmp_time,
            self.OperatorChoices.LTE: val_time <= cmp_time, # NEW
            self.OperatorChoices.EQUALS: val_time == cmp_time
        }
        return op_map.get(self.operator, False)

    def _evaluate_string(self, tx_val: Any) -> bool:
        val_raw = str(tx_val)
        cmp_raw = self.value or ''
        op = self.operator

        case_sensitive_ops = {
            self.OperatorChoices.EQUALS_CS,
            self.OperatorChoices.CONTAINS_CS,
            self.OperatorChoices.REGEX
        }

        # Normalize to lowercase unless it's a case-sensitive operation
        val = val_raw if op in case_sensitive_ops else val_raw.lower()
        cmp_norm = cmp_raw if op in case_sensitive_ops else cmp_raw.lower()

        try:
            if op == self.OperatorChoices.CONTAINS: return cmp_norm in val
            if op == self.OperatorChoices.NOT_CONTAINS: return cmp_norm not in val
            if op == self.OperatorChoices.EQUALS: return val == cmp_norm
            if op == self.OperatorChoices.STARTSWITH: return val.startswith(cmp_norm)
            if op == self.OperatorChoices.ENDSWITH: return val.endswith(cmp_norm)
            # **NEW**: Case-sensitive operators
            if op == self.OperatorChoices.EQUALS_CS: return val_raw == cmp_raw
            if op == self.OperatorChoices.CONTAINS_CS: return cmp_raw in val_raw
            # **UPDATED**: Use JSON list helper
            if op == self.OperatorChoices.IN:
                allowed = self._normalize_json_list_value(cmp_raw)
                return val in allowed
            if op == self.OperatorChoices.NOT_IN:
                allowed = self._normalize_json_list_value(cmp_raw)
                return val not in allowed
            if op == self.OperatorChoices.REGEX:
                try:
                    # REGEX is inherently case-sensitive unless flags are used
                    pattern = re.compile(cmp_raw)
                    return bool(pattern.search(val_raw))
                except re.error:
                    logger.debug("Invalid regex in condition id=%s pattern=%r", getattr(self, 'pk', None), cmp_raw)
                    return False
        except Exception:
            logger.exception("Unexpected error in string evaluation for condition id=%s", getattr(self, 'pk', None))
            return False

        return False


# ==============================================================================
#  RuleGroup: Nested, performant, and deterministic (with Tenancy)
# ==============================================================================
class RuleGroup(models.Model):
    """
    Organizes conditions and other rule groups to build complex, nested logic.
    This version adds multi-tenancy to ensure rules are isolated per entity.
    """

    class LogicChoices(models.TextChoices):
        AND = 'AND', 'All (AND)'
        OR = 'OR', 'Any (OR)'

    entity = models.ForeignKey(
        'EntityModel', on_delete=models.CASCADE,
        null=True, blank=True,
        related_name='rule_groups', 
        help_text="The entity that owns this rule."
    )
    name = models.CharField(max_length=128)
    logic = models.CharField(max_length=3, choices=LogicChoices.choices, default=LogicChoices.AND)
    priority = models.PositiveIntegerField(default=100, help_text="Lower values run first.")
    active = models.BooleanField(default=True)
    target_account = models.ForeignKey('AccountModel', on_delete=models.CASCADE, null=True, blank=True)
    parent = models.ForeignKey('self', on_delete=models.CASCADE, null=True, blank=True, related_name='child_groups')
    keywords = models.TextField(blank=True, help_text="Space-separated keywords for fast rule retrieval. Can be auto-generated.")
    match_count = models.PositiveIntegerField(default=0)

    NESTED_PREFETCH_DEPTH = 4

    class Meta:
        constraints = [models.UniqueConstraint(fields=['entity', 'name'], name='unique_rule_name_per_entity')]
        ordering = ['priority', 'pk']

    # --- Evaluation & Explanation --------------------------------
    def evaluate(self, tx) -> bool:
        condition_results = [c.evaluate(tx) for c in self.conditions.all()]
        child_group_results = [child.evaluate(tx) for child in self.child_groups.filter(active=True)]
        all_results = condition_results + child_group_results

        if not all_results: return False
        return all(all_results) if self.logic == self.LogicChoices.AND else any(all_results)

    def _evaluate_cached(self, tx) -> bool:
        if not hasattr(self, '_cached_children'): return self.evaluate(tx) # Fallback

        condition_results = [c.evaluate(tx) for c in self.conditions.all()]
        child_results = [child._evaluate_cached(tx) for child in self._cached_children if child.active]
        all_results = condition_results + child_results

        if not all_results: return False
        return all(all_results) if self.logic == self.LogicChoices.AND else any(all_results)

    # Method to explain match logic
    def explain_match(self, tx) -> Dict[str, Any]:
        """Returns a nested dictionary explaining the match result."""
        conditions_exp = [
            {
                'condition_id': c.pk, 'field': c.field, 'operator': c.operator,
                'value': c.value, 'tx_value': getattr(tx, c.field, None),
                'result': c.evaluate(tx)
            } for c in self.conditions.all()
        ]
        children_exp = [
            child.explain_match(tx) for child in getattr(self, '_cached_children', self.child_groups.all()) if child.active
        ]
        all_results = [c['result'] for c in conditions_exp] + [c['final_result'] for c in children_exp]
        final_result = False
        if all_results:
            final_result = all(all_results) if self.logic == self.LogicChoices.AND else any(all_results)

        return {
            'group_id': self.pk, 'group_name': self.name, 'logic': self.logic,
            'conditions': conditions_exp, 'child_groups': children_exp,
            'final_result': final_result
        }

    # --- Prefetch building
    @classmethod
    def _build_prefetch_children(cls, depth: int) -> List[Prefetch]:
        prefetches: List[Prefetch] = [Prefetch('conditions')]
        path = 'child_groups'
        for _ in range(depth):
            prefetches.append(Prefetch(path + '__conditions'))
            prefetches.append(Prefetch(path, queryset=cls.objects.filter(active=True)))
            path += '__child_groups'
        return prefetches

    # --- Candidate selection
    @staticmethod
    def _extract_tx_keywords(tx) -> Set[str]:
        tokens: Set[str] = set()
        for f in ['vendor', 'description', 'category']:
            val = getattr(tx, f, None)
            if val:
                tokens.update(t for t in re.split(r'\W+', str(val).lower()) if len(t) >= 2)
        return tokens

    @classmethod
    def _candidate_top_level_rules(cls, tx, entity, bypass_prefilter: bool = False):
        base_q = Q(parent__isnull=True, active=True, entity=entity)
        if bypass_prefilter:
            return cls.objects.filter(base_q)

        tx_keywords = cls._extract_tx_keywords(tx)
        if not tx_keywords:
            return cls.objects.filter(base_q & Q(keywords=''))

        keyword_q = Q(keywords='')
        for kw in tx_keywords:
            keyword_q |= Q(keywords__iregex=rf'\b{re.escape(kw)}\b')
        return cls.objects.filter(base_q & keyword_q)

    @classmethod
    def _prefetch_candidate_tree(cls, candidate_qs):
        top_rules = list(candidate_qs.prefetch_related(*cls._build_prefetch_children(depth=cls.NESTED_PREFETCH_DEPTH)))
        id_to_group = {g.pk: g for g in top_rules}
        queue = list(top_rules)
        while queue:
            parent = queue.pop(0)
            children = list(parent.child_groups.all())
            parent._cached_children = children # Attach prefetched children
            for child in children:
                if child.pk not in id_to_group:
                    id_to_group[child.pk] = child
                    queue.append(child)
        return top_rules

    # --- Matching interface
    @classmethod
    def find_matches(cls, tx, entity, bypass_prefilter: bool = False, explain: bool = False) -> List[Any]:
        candidate_qs = cls._candidate_top_level_rules(tx, entity, bypass_prefilter=bypass_prefilter)
        top_rules = cls._prefetch_candidate_tree(candidate_qs)
        if explain:
            explanations = [rule.explain_match(tx) for rule in top_rules]
            return [ (top_rules[i], exp) for i, exp in enumerate(explanations) if exp['final_result'] ]
        else:
            return [rule for rule in top_rules if rule._evaluate_cached(tx)]

    @classmethod
    def simulate(cls, tx, entity, bypass_prefilter: bool = False) -> List['RuleGroup']:
        return cls.find_matches(tx, entity, bypass_prefilter=bypass_prefilter, explain=False)

    @classmethod
    def simulate_with_explanation(cls, tx, entity, bypass_prefilter: bool = False) -> List[Tuple['RuleGroup', Dict]]:
        return cls.find_matches(tx, entity, bypass_prefilter=bypass_prefilter, explain=True)

    @classmethod
    def get_first_match(cls, tx, entity, bypass_prefilter: bool = False) -> Optional['RuleGroup']:
        matches = cls.find_matches(tx, entity, bypass_prefilter=bypass_prefilter)
        if not matches: return None
        winner = matches[0]
        cls.objects.filter(pk=winner.pk).update(match_count=F('match_count') + 1)
        return winner

    # --- Validation and Save Logic ----------------
    def _derive_keywords_from_conditions(self) -> Set[str]:
        """Automatically extract keywords from relevant string-based conditions."""
        keyword_tokens = set()
        string_fields = {
            RuleCondition.FieldChoices.DESCRIPTION, RuleCondition.FieldChoices.VENDOR,
            RuleCondition.FieldChoices.CATEGORY, RuleCondition.FieldChoices.MEMO
        }
        string_operators = {
            RuleCondition.OperatorChoices.CONTAINS, RuleCondition.OperatorChoices.EQUALS,
            RuleCondition.OperatorChoices.STARTSWITH, RuleCondition.OperatorChoices.ENDSWITH
        }
        for cond in self.conditions.filter(field__in=string_fields, operator__in=string_operators):
            if cond.value:
                keyword_tokens.update(t for t in re.split(r'\W+', cond.value.lower()) if len(t) >= 2)
        return keyword_tokens

    def clean(self):
        # **NEW**: Prevent circular dependencies
        if self.parent:
            parent_node = self.parent
            while parent_node is not None:
                if parent_node.pk == self.pk:
                    raise ValidationError({'parent': 'Circular dependency detected. A rule group cannot be its own ancestor.'})
                parent_node = parent_node.parent
        # Guardrail against empty top-level rules
        if self.parent is None and self.active:
            is_empty = self.pk is None or (not self.conditions.exists() and not self.child_groups.exists())
            if is_empty:
                raise ValidationError("An active, top-level rule cannot be empty. Add conditions or child groups, or set it to inactive.")

    def save(self, *args, **kwargs):
        self.full_clean()  # Run validation before saving
        super().save(*args, **kwargs)

        # **NEW**: Automatic Keyword Generation
        manual_keywords = set(t.strip().lower() for t in re.split(r'\W+', self.keywords) if t.strip())
        derived_keywords = self._derive_keywords_from_conditions()
        all_keywords = sorted(manual_keywords.union(derived_keywords))
        new_keywords_str = ' '.join(all_keywords)

        if self.keywords != new_keywords_str:
            # Update without triggering save signals again to prevent recursion
            RuleGroup.objects.filter(pk=self.pk).update(keywords=new_keywords_str)


# ==============================================================================
#  RuleAuditLog: For tracking which rule applied to each transaction
# ==============================================================================
class RuleAuditLog(models.Model):
    """
    Records categorization runs for transactions for auditing and debugging.
    """
    tx = models.ForeignKey('TransactionModel', on_delete=models.CASCADE, help_text="Categorized transaction")
    rule_group = models.ForeignKey(RuleGroup, on_delete=models.SET_NULL, null=True, help_text="Matched rule group (if any)")
    matched = models.BooleanField(help_text="Did any rule match?")
    # Docstring to reference the RuleGroup.explain_match() method
    match_info = models.JSONField(
        default=dict,
        help_text="Verbose match explanation: output of RuleGroup.explain_match()"
    )
    created_at = models.DateTimeField(auto_now_add=True)


## **How this works in practice**

# - **RuleCondition.evaluate(tx):** Evaluates a single logic condition on a transaction (e.g., does `amount > 1000`?).
# - **RuleCondition.explain(tx):** Returns an object explaining the result of that test (expected value, tx value, pass/fail).
# - **RuleGroup.evaluate(tx):** Evaluates all conditions for the group with AND/OR logic.
# - **RuleGroup.explain_match(tx):** Returns detailed explanation if grouped conditions hit, for UX/audit.
# - **RuleGroup.match_any_verbose(tx):** Tries all (active, priority-ordered) rules for the transaction, returns first match's explanation (and analytic counters).
# - **RuleAuditLog:** Stores the explanation and match result for every transaction processed, for stats/analytics/troubleshooting.

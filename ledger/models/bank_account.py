"""
A Bank Account refers to the financial institution which holds financial assets for the EntityModel.
A bank account usually holds cash, which is a Current Asset. Transactions may be imported using the open financial
format specification OFX into a staging area for final disposition into the EntityModel ledger.
"""
import datetime
from typing import Optional
from uuid import uuid4
from django.contrib.auth import get_user_model
from django.core.exceptions import ValidationError
from django.db import models
from django.db.models import Q, QuerySet
from django.shortcuts import get_object_or_404
from django.utils.translation import gettext_lazy as _
from typing import Type, Tuple, Any, Dict
from ledger.models import CreateUpdateMixIn, FinancialAccountInfoMixin, ContactInfoMixIn
from ledger.models.utils import lazy_loader
from decimal import Decimal as _Decimal
from django.db import transaction


UserModel = get_user_model()


class BankAccountValidationError(ValidationError):
    pass


class BankAccountModelQuerySet(QuerySet):
    """
    A custom defined QuerySet for the BankAccountModel.
    """

    def active(self) -> QuerySet:
        """
        Active bank accounts which can be used to create new transactions.

        Returns
        _______
        BankAccountModelQuerySet
            A filtered BankAccountModelQuerySet of active accounts.
        """
        return self.filter(active=True)

    def hidden(self) -> QuerySet:
        """
        Hidden bank accounts which can be used to create new transactions. but will not show in drop down menus
        in the UI.

        Returns
        _______
        BankAccountModelQuerySet
            A filtered BankAccountModelQuerySet of active accounts.
        """
        return self.filter(hidden=True)


class BankAccountModelManager(models.Manager):
    """
    Custom defined Model Manager for the BankAccountModel.
    """

    def get_queryset(self) -> BankAccountModelQuerySet:
        return BankAccountModelQuerySet(self.model, using=self._db)

    def for_user(self, user_model):
        qs = self.get_queryset()
        if user_model.is_superuser:
            return qs
        return qs.filter(
            Q(entity_model__admin=user_model) |
            Q(entity_model__managers__in=[user_model])
        )

    def for_entity(self, entity_slug, user_model) -> BankAccountModelQuerySet:
        """
        Allows only the authorized user to query the BankAccountModel for a given EntityModel.
        This is the recommended initial QuerySet.

        Parameters
        __________
        entity_slug: str or EntityModel
            The entity slug or EntityModel used for filtering the QuerySet.
        user_model
            Logged in and authenticated django UserModel instance.
        """
        qs = self.for_user(user_model)
        if isinstance(entity_slug, lazy_loader.get_entity_model()):
            return qs.filter(
                Q(entity_model=entity_slug)
            )
        return qs.filter(
            Q(entity_model__slug__exact=entity_slug)
        )


class BankAccountModelAbstract(
    FinancialAccountInfoMixin, 
    CreateUpdateMixIn,
    ContactInfoMixIn
):
    """
    This is the main abstract class which the BankAccountModel database will inherit from.
    The BankAccountModel inherits functionality from the following MixIns:

        1. :func:`BankAccountInfoMixIn <ledger.models.mixins.BankAccountInfoMixIn>`
        2. :func:`CreateUpdateMixIn <ledger.models.mixins.CreateUpdateMixIn>`


    Attributes
    ----------
    uuid : UUID
        This is a unique primary key generated for the table. The default value of this field is uuid4().
    name: str
        A user defined name for the bank account as a String.
    entity_model: EntityModel
        The EntityModel associated with the BankAccountModel instance.
    account_model: AccountModel
        The AccountModel associated with the BankAccountModel instance. Must be an account with role ASSET_CA_CASH.
    active: bool
        Determines whether the BackAccountModel instance bank account is active. Defaults to True.
    hidden: bool
        Determines whether the BackAccountModel instance bank account is hidden. Defaults to False.
    """


    class ConnectionType(models.TextChoices):
        PLAID = 'plaid', 'Plaid'
        MANUAL = 'manual', 'Manual'


    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    user = models.ForeignKey(UserModel, on_delete=models.CASCADE, null=True)
    connection_type = models.CharField(
        max_length=20,
        choices=ConnectionType.choices,
        default=ConnectionType.MANUAL
    )
    module_keys = models.JSONField(default=list)
    plaid_item = models.ForeignKey(
        'ledger.PlaidItem',
        on_delete=models.SET_NULL,
        null=True, blank=True,
        related_name='bank_connections'
    )
    metadata = models.JSONField(default=dict, blank=True, null=True)
    balance = models.DecimalField(max_digits=12, decimal_places=2, default=0.00)
    # todo: rename to account_name?...
    name = models.CharField(max_length=150, null=True, blank=True)
    entity_model = models.ForeignKey('ledger.EntityModel',
                                     on_delete=models.CASCADE,
                                     null=True,
                                     blank=True,
                                     verbose_name=_('Entity Model'))
    account_model = models.ForeignKey('ledger.AccountModel',
                                      on_delete=models.RESTRICT,
                                      null=True,
                                      help_text=_(
                                          'Account model be used to map transactions from financial institution'),
                                      verbose_name=_('Associated Account Model'))
    active = models.BooleanField(default=False)
    hidden = models.BooleanField(default=False)
    is_verified = models.BooleanField(default=False)
    objects = BankAccountModelManager()

    def configure(self,
                  entity_slug,
                  user_model: Optional[UserModel],
                  commit: bool = False):

        EntityModel = lazy_loader.get_entity_model()
        if isinstance(entity_slug, str):
            if not user_model:
                raise BankAccountValidationError(_('Must pass user_model when using entity_slug.'))
            entity_model_qs = EntityModel.objects.for_user(user_model=user_model)
            entity_model = get_object_or_404(entity_model_qs, slug__exact=entity_slug)
        elif isinstance(entity_slug, EntityModel):
            entity_model = entity_slug
        else:
            raise BankAccountValidationError('entity_slug must be an instance of str or EntityModel')

        self.entity_model = entity_model
        self.clean()
        if commit:
            self.save(update_fields=[
                'entity_model',
                'updated'
            ])
        return self, entity_model

    def is_active(self):
        return self.active is True

    class Meta:
        abstract = True
        verbose_name = _('Bank Account')
        indexes = [
            models.Index(fields=['account_type']),
            models.Index(fields=['account_model'])
        ]
        unique_together = [
            ('entity_model', 'account_number'),
            ('entity_model', 'account_model', 'account_number', 'routing_number')
        ]

    def __str__(self):
        return f'{self.account_type} Bank Account: {self.name}'

    def can_activate(self) -> bool:
        return self.active is False

    def can_inactivate(self) -> bool:
        return self.active is True

    def mark_as_active(self, commit: bool = False, raise_exception: bool = True, **kwargs):
        if not self.can_activate():
            if raise_exception:
                raise BankAccountValidationError('Bank Account cannot be activated.')
        self.active = True
        if commit:
            self.save(update_fields=[
                'active',
                'updated'
            ])

    def mark_as_inactive(self, commit: bool = False, raise_exception: bool = True, **kwargs):
        if not self.can_inactivate():
            if raise_exception:
                raise BankAccountValidationError('Bank Account cannot be deactivated.')
        self.active = False
        if commit:
            self.save(update_fields=[
                'active',
                'updated'
            ])

    
    def save(self, *args, **kwargs):
        valid_types = [
            'checking', 'savings', 'cash_management', 'credit_card',
            'loan', 'mortgage', 'investment', 'retirement', 
            self.LEDGER_CASH, self.BUDGETING_CASH
        ]
        if self.account_type.lower() not in valid_types:
            self.account_type = 'other'
        super().save(*args, **kwargs)

    @property
    def is_manual(self):
        return self.connection_type == self.ConnectionType.MANUAL
    

    @property
    def is_plaid_account(self):
        return self.connection_type == self.ConnectionType.PLAID and self.plaid_item is not None



    @classmethod
    def get_or_create_cash_account(cls, user, amount=None, account_type=None):
        """
        Ensure both LEDGER_CASH and BUDGETING_CASH accounts exist for `user`.
        If `amount` is provided it is added to BOTH accounts' balances.

        Returns the LEDGER_CASH account instance.
        """
        amt = None
        account_to_update = None
        if amount is not None:
            try:
                amt = _Decimal(amount)
            except Exception:
                raise BankAccountValidationError(_("Invalid amount provided."))
            if amt <= 0:
                raise BankAccountValidationError(_("Amount must be positive."))

        with transaction.atomic():
            # this loop make sure there's a cash account for ledger and budgeting
            for acc_type, module_key in [
                (cls.LEDGER_CASH, "ledger"),
                (cls.BUDGETING_CASH, "budgeting")
            ]:
                account, _ = cls.objects.get_or_create(
                    user=user,
                    account_type=acc_type,
                    defaults={"name": "Cash Account"}
                )


                # Add module_key to module_keys
                if module_key not in account.module_keys:
                    account.module_keys.append(module_key.lower())
                    account.save(update_fields=['module_keys'])

                # Set account_to_update to be used later
                if account_type and account_type.lower() == acc_type:
                    account_to_update = account


            # This updates the balance depending on account_type
            if amt and account_type and account_to_update:
                account_to_update.balance +=  amt
                account_to_update.save(update_fields=['balance'])


        return account_to_update or account


    @classmethod
    def update_or_create_bank_account(
        cls,
        user: UserModel,
        account_number: str,
        routing_number: str,
        **kwargs: Dict[str, Any]
    ) -> Tuple["BankAccountModel", bool]:
        try:
            # Accept either top-level kwargs or a single 'defaults' dict (signals pass 'defaults')
            data: Dict[str, Any] = {}
            if 'defaults' in kwargs and isinstance(kwargs['defaults'], dict):
                data.update(kwargs['defaults'])
            # Merge other kwargs without overwriting keys from 'defaults'
            for k, v in kwargs.items():
                if k != 'defaults' and k not in data:
                    data[k] = v

            conn_type = data.get('connection_type')

            # Normalize name fields: prefer explicit bank_name, then name
            name = data.get('bank_name') or data.get('name') 

            # Handle address mapping from generic or list inputs
            addr1 = data.get('address_1')
            addr2 = data.get('address_2')
            street_address = data.get('street_address')

            if isinstance(street_address, list):
                if not addr1 and len(street_address) > 0:
                    addr1 = street_address[0]
                if not addr2 and len(street_address) > 1:
                    addr2 = street_address[1]
            elif not addr1 and street_address:
                addr1 = street_address

            create_defaults = {
                'name': name,
                'country': data.get('country'),
                'account_type': data.get('account_type'),
                'address_1': addr1,
                'address_2': addr2,
                'city': data.get('city'),
                'state': data.get('state') or data.get('state_province'),
                'zip_code': data.get('zip_code') or data.get('postal_code'),
                'is_verified': data.get('is_verified', True),
                'metadata': data.get('metadata') or {},
                'connection_type': conn_type if conn_type else cls.ConnectionType.MANUAL,
            }
            if data.get('plaid_item') is not None:
                create_defaults['plaid_item'] = data.get('plaid_item')

            return cls.objects.update_or_create(
                user=user,
                account_number=account_number,
                routing_number=routing_number,
                defaults=create_defaults
            )

        except Exception as e:
            raise BankAccountValidationError(str(e))
    
    
    @classmethod
    def get_by_account_id(cls, account_id):
        if not account_id:
            return None
        return cls.objects.get_queryset().filter(metadata__account_id=account_id).first()


    @classmethod
    def create_transaction_for_account(cls, amount, account_id, timestamp: str = None, description: str = None, duplicate_window_seconds: int = 60):
        """
        Create a single TransactionModel for the bank account found by `account_id`.

        - `amount` may be numeric or Decimal; must be non-zero.
        - If amount > 0 -> tx_type = CREDIT; if amount < 0 -> tx_type = DEBIT.
        - Uses the bank account's `account_model` as the TransactionModel.account.
        - Prevents duplicate transactions by checking for an existing transaction with the
          same account, amount, tx_type, description within `duplicate_window_seconds` of `timestamp`
          AND on the same calendar date (prevents recurring-monthly txs from being mistaken as duplicates).
        - Returns the created TransactionModel instance or the existing transaction if duplicate detected.
        """
        TransactionModel = lazy_loader.get_txs_model()
        JournalEntryModel = lazy_loader.get_journal_entry_model()

        # amount validation
        try:
            amt = _Decimal(amount)
        except Exception:
            raise BankAccountValidationError(_('Invalid amount provided.'))

        if amt == 0:
            raise BankAccountValidationError(_('Amount must be non-zero.'))

        # locate bank account + related models
        bank_acc = cls.get_by_account_id(account_id)
        if not bank_acc:
            raise BankAccountValidationError(_('Bank account not found for account_id.'))

        account = getattr(bank_acc, 'account_model', None)
        if not account:
            raise BankAccountValidationError(_('Bank account has no linked AccountModel.'))

        entity = getattr(bank_acc, 'entity_model', None)
        if not entity:
            raise BankAccountValidationError(_('Bank account is not linked to an EntityModel.'))

        ledger = entity.get_ledgers().first()
        if not ledger:
            raise BankAccountValidationError(_('No LedgerModel found for entity.'))

        tx_type = TransactionModel.CREDIT if amt > 0 else TransactionModel.DEBIT
        amt_abs = abs(amt)

        # timestamp handling
        from ledger.io.io_core import get_localtime, validate_io_timestamp
        if timestamp is None:
            ts = get_localtime()
        elif isinstance(timestamp, str):
            try:
                ts = validate_io_timestamp(timestamp)
            except Exception:
                raise BankAccountValidationError(_('Invalid timestamp provided.'))
        elif isinstance(timestamp, datetime.datetime):
            ts = timestamp
        else:
            raise BankAccountValidationError(_('Invalid timestamp type provided.'))

        # duplicate detection window (and require same calendar date)
        window = datetime.timedelta(seconds=int(duplicate_window_seconds or 60))

        existing = TransactionModel.objects.filter(
            account=account,
            amount=amt_abs,
            tx_type=tx_type,
            description=(description or '')
        ).filter(
            journal_entry__ledger=ledger,
            journal_entry__timestamp__date=ts.date(),  # ensure same calendar date to avoid matching recurring txs
            journal_entry__timestamp__gte=(ts - window),
            journal_entry__timestamp__lte=(ts + window)
        ).select_related('journal_entry').first()

        if existing:
            # Duplicate detected — return existing transaction instead of creating another
            return existing

        # create JE + tx atomically
        with transaction.atomic():
            je = JournalEntryModel.objects.create(
                ledger=ledger,
                description=description or f'Bank transaction: {bank_acc.name}',
                timestamp=ts
            )

            tx = TransactionModel.objects.create(
                journal_entry=je,
                account=account,
                amount=amt_abs,
                tx_type=tx_type,
                description=description or ''
            )

        return tx



class BankAccountModel(BankAccountModelAbstract):
    """
    Base Bank Account Model Implementation
    """

    class Meta(BankAccountModelAbstract.Meta):
        abstract = False

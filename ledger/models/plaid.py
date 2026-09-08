from django.db import models
from django.utils.functional import cached_property
from uuid import uuid4
from ledger.models.mixins import ContactInfoMixIn, CreateUpdateMixIn
from django.utils.translation import gettext_lazy as _
from django.db.models import Q, F, Count, Manager, QuerySet
from asgiref.sync import sync_to_async
from decimal import Decimal
from datetime import datetime, date
from django.contrib.auth import get_user_model



UserModel = get_user_model()


class PlaidItemManager(models.Manager):
    """
    Manager responsible for robust PlaidItem creation/lookup.
    Deduping strategy:
      1) Exact item_id match.
      2) Best-effort match by overlap of account_ids in item metadata for the same user.
      3) Create new PlaidItem if no match found.
    Returns (plaid_item, created_bool).
    """
    def get_or_create_from_metadata(self, item_id: str, access_token: str, user, metadata: dict, module_key: str = None):
        meta_dict = metadata or {}
        account_ids = {a.get('account_id') for a in meta_dict.get('accounts', []) if a.get('account_id')}
        try:
            plaid_item = self.get(item_id=item_id)
            created = False
            updated = False
            if plaid_item.access_token != access_token:
                plaid_item.access_token = access_token
                updated = True
            if plaid_item.user != user:
                plaid_item.user = user
                updated = True
            # Replace metadata with latest authoritative metadata
            if plaid_item.metadata != meta_dict:
                plaid_item.metadata = meta_dict
                updated = True
            if updated:
                plaid_item.save(update_fields=['access_token', 'user', 'metadata', 'updated_at'])
        except PlaidItem.DoesNotExist:
            # Try to find an existing item for the same user that shares any account_id
            plaid_item = None
            if account_ids:
                candidates = self.filter(user=user)
                for c in candidates:
                    existing_accounts = {a.get('account_id') for a in c.metadata.get('accounts', []) if a.get('account_id')}
                    if existing_accounts & account_ids:
                        plaid_item = c
                        break

            if plaid_item:
                created = False
                # adopt the new item_id/access_token and update metadata
                plaid_item.item_id = item_id
                plaid_item.access_token = access_token
                plaid_item.metadata = meta_dict
                plaid_item.save(update_fields=['item_id', 'access_token', 'metadata', 'updated_at'])
            else:
                # No match -> create a new PlaidItem
                mk = [module_key.lower()] if module_key else []
                plaid_item = self.create(
                    item_id=item_id,
                    access_token=access_token,
                    user=user,
                    metadata=meta_dict,
                    module_keys=mk,
                )
                created = True

        # Ensure module_key is present
        if module_key:
            mk_lower = module_key.lower()
            if mk_lower not in (plaid_item.module_keys or []):
                plaid_item.module_keys.append(mk_lower)
                plaid_item.save(update_fields=['module_keys'])

        return plaid_item, created


class PlaidItem(models.Model):
    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    user = models.ForeignKey(UserModel, on_delete=models.CASCADE)
    access_token = models.CharField(max_length=100, blank=True, default='')
    item_id = models.CharField(max_length=100, blank=True, default='')
    module_keys = models.JSONField(default=list)
    metadata = models.JSONField(blank=True, default=dict)
    cursor = models.CharField(max_length=255, blank=True, default='')  
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    objects = PlaidItemManager()

    def __str__(self):
        # Optionally include institution name if available in metadata
        institution_name = self.metadata.get('institution', {}).get('name', 'Unknown Institution')
        return f"Plaid Item {self.item_id} ({institution_name})"
    

class Counterparty(models.Model):
    class ConfidenceLevelChoices(models.TextChoices):
        VERY_HIGH = 'VERY_HIGH', 'Very High'
        HIGH = 'HIGH', 'High'
        MEDIUM = 'MEDIUM', 'Medium'
        LOW = 'LOW', 'Low'

    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    name = models.CharField(max_length=255)
    type = models.CharField(max_length=50)
    logo_url = models.URLField(null=True, blank=True)
    website = models.URLField(null=True, blank=True)
    entity_id = models.CharField(max_length=255, unique=True)
    confidence_level = models.CharField(max_length=10, choices=ConfidenceLevelChoices.choices)

    def __str__(self):
        return f"{self.name} ({self.type})"



class PlaidTransactionManager(models.Manager):

    @staticmethod
    def _parse_date(val):
        if not val:
            return None
        if isinstance(val, date) and not isinstance(val, datetime):
            return val
        try:
            return date.fromisoformat(val)
        except Exception:
            try:
                return datetime.fromisoformat(val).date()
            except Exception:
                return None

    @staticmethod
    def _parse_datetime(val):
        if not val:
            return None
        if isinstance(val, datetime):
            return val
        try:
            return datetime.fromisoformat(val)
        except Exception:
            return None


    @staticmethod
    def sanitize_str(val, allow_none=False):
        if val is None:
            return None if allow_none else ''
        s = str(val).replace('\x00', '')
        return None if allow_none and s == '' else s


    @staticmethod
    def sanitize_name(val):
        if not val:
            return ''
        return str(val).replace('\x00', '')


    @staticmethod
    def sanitize_optional(val):
        if val is None:
            return None
        s = str(val).replace('\x00', '').strip()
        return s if s != '' else None
    
    async def abulk_create_from_plaid_data(self, plaid_data_list, plaid_item=None):
        "Async wrapper for bulk_create_from_plaid_data"
        return await sync_to_async(self.bulk_create_from_plaid_data)(plaid_data_list, plaid_item)

    async def abulk_update_from_plaid_data(self, plaid_data_list, plaid_item=None, sync_id=None):
        "Async wrapper for bulk_update_from_plaid_data"
        return await sync_to_async(self.bulk_update_from_plaid_data)(plaid_data_list, plaid_item, sync_id)


    def bulk_create_from_plaid_data(self, plaid_data_list, plaid_item):
        """
        Create multiple PlaidTransaction rows for the same `plaid_item`.
        Delegates to the model-level `create_many_from_plaid_data` which performs
        a performant bulk_create + counterparties handling.
        Returns list of created PlaidTransaction instances (only newly created ones).
        """
        if not plaid_data_list:
            return []


        # Collect ordered unique transaction_ids from payload
        tx_ids_all = []
        for pd in plaid_data_list:
            raw_txid = pd.get('transaction_id')
            tx_id = self.sanitize_str(raw_txid, allow_none=False)
            if tx_id and tx_id not in tx_ids_all:
                tx_ids_all.append(tx_id)
        if not tx_ids_all:
            return []

        # Find already-existing transactions for this plaid_item and skip them
        existing_tx_ids = set(self.filter(transaction_id__in=tx_ids_all, plaid_item=plaid_item).values_list('transaction_id', flat=True))

        # Build model instances (unsaved) only for tx_ids that do not already exist
        objs = []
        created_tx_ids = []
        seen = set()
        for pd in plaid_data_list:
            raw_txid = pd.get('transaction_id')
            tx_id = self.sanitize_str(raw_txid, allow_none=False)
            if not tx_id or tx_id in existing_tx_ids or tx_id in seen:
                continue
            seen.add(tx_id)
            created_tx_ids.append(tx_id)

            amount_raw = pd.get('amount', 0)
            amount = Decimal(str(amount_raw)) if amount_raw is not None else Decimal('0')
            iso_code = self.sanitize_str(pd.get('iso_currency_code') or pd.get('currency') or 'USD', allow_none=False)[:3]
            acct_id = self.sanitize_str(pd.get('account_id', ''), allow_none=False)
            name_val = self.sanitize_str(pd.get('name'), allow_none=False)
            mn_raw = pd.get('merchant_name')
            merchant_name_val = self.sanitize_str(mn_raw, allow_none=True)

            objs.append(self.model(
                transaction_id=tx_id,
                plaid_item=plaid_item,
                account_id=acct_id,
                amount=amount,
                iso_currency_code=iso_code,
                date=self._parse_date(pd.get('date')),
                datetime=self._parse_datetime(pd.get('datetime') or pd.get('date')),
                authorized_date=self._parse_date(pd.get('authorized_date')),
                authorized_datetime=self._parse_datetime(pd.get('authorized_datetime')),
                name=name_val,
                merchant_name=merchant_name_val,
                payment_channel=pd.get('payment_channel') or self.model.PaymentChannelChoices.OTHER,
                pending=bool(pd.get('pending', False)),
                transaction_type=pd.get('transaction_type') or self.model.TransactionTypeChoices.UNRESOLVED,
                personal_finance_category=pd.get('personal_finance_category') or pd.get('category'),
                location=pd.get('location'),
                payment_meta=pd.get('payment_meta'),
                is_removed=False,
            ))

        if not objs:
            return []

        # Bulk create only the new transactions
        self.bulk_create(objs)

        # Re-fetch created rows to get model instances (and m2m capability)
        created = list(self.filter(transaction_id__in=created_tx_ids, plaid_item=plaid_item))
        created_map = {t.transaction_id: t for t in created}

        # Batch-process counterparties (merchant)
        merchant_map = {}  # entity_id -> merchant_name
        for pd in plaid_data_list:
            txid = self.sanitize_str(pd.get('transaction_id'), allow_none=False)
            merchant_meta = pd.get('merchant_metadata') or {}
            entity_id_raw = merchant_meta.get('merchant_id')
            entity_id = self.sanitize_str(entity_id_raw, allow_none=True)
            if not entity_id:
                mn = self.sanitize_str(pd.get('merchant_name'), allow_none=True) or self.sanitize_str(pd.get('name'), allow_none=False)
                mn = mn.strip() if mn else ''
                entity_id = f"merchant:{mn}".lower() if mn else None
            if entity_id:
                merchant_map[entity_id] = merchant_map.get(entity_id) or (mn if 'mn' in locals() else '')

        if merchant_map:
            existing = Counterparty.objects.filter(entity_id__in=merchant_map.keys())
            existing_map = {c.entity_id: c for c in existing}
            to_create = []
            for eid, name in merchant_map.items():
                if eid not in existing_map:
                    to_create.append(Counterparty(entity_id=eid, name=name or '', type='merchant', confidence_level=Counterparty.ConfidenceLevelChoices.LOW))
            if to_create:
                Counterparty.objects.bulk_create(to_create)
                # refresh existing_map
                existing = Counterparty.objects.filter(entity_id__in=merchant_map.keys())
                existing_map = {c.entity_id: c for c in existing}

            # Attach counterparties only to newly created transactions
            for pd in plaid_data_list:
                txid = self.sanitize_str(pd.get('transaction_id'), allow_none=False)
                entity_id = self.sanitize_str((pd.get('merchant_metadata') or {}).get('merchant_id'), allow_none=True)
                if not entity_id:
                    mn = self.sanitize_str(pd.get('merchant_name'), allow_none=True) or self.sanitize_str(pd.get('name'), allow_none=False)
                    mn = mn.strip() if mn else ''
                    entity_id = f"merchant:{mn}".lower() if mn else None
                if not entity_id:
                    continue
                tx = created_map.get(txid)
                cp = existing_map.get(entity_id)
                if tx and cp:
                    tx.counterparties.add(cp)

        return created

    def bulk_update_from_plaid_data(self, plaid_data_list, plaid_item=None, sync_id=None):
        """
        Update/version existing PlaidTransaction rows based on incoming plaid_data_list.
        - Only touches transactions that already exist (matched by transaction_id and optional plaid_item).
        - Prefers creating a new version via manager.create_new_version.
        - Returns list of updated/versioned PlaidTransaction instances.
        """
        if not plaid_data_list:
            return []

        tx_ids = [d.get('transaction_id') for d in plaid_data_list if d.get('transaction_id')]
        if not tx_ids:
            return []

        user = plaid_item.user

        qs = self.select_for_update().filter(transaction_id__in=tx_ids)
        if plaid_item is not None:
            qs = qs.filter(plaid_item=plaid_item)
        existing_map = {t.transaction_id: t for t in qs}

        # Build merchant_map from incoming payloads (create/attach counterparties for updated rows)
        merchant_map = {}
        for pd in plaid_data_list:
            entity_id_raw = (pd.get('merchant_metadata') or {}).get('merchant_id')
            entity_id = self.sanitize_str(entity_id_raw, allow_none=True)
            mn = None
            if not entity_id:
                mn = self.sanitize_str(pd.get('merchant_name'), allow_none=True) or self.sanitize_str(pd.get('name'), allow_none=False)
                mn = mn.strip() if mn else ''
                entity_id = f"merchant:{mn}".lower() if mn else None
            if entity_id:
                merchant_map[entity_id] = merchant_map.get(entity_id) or (mn if mn is not None else '')

        existing_cp_map = {}
        if merchant_map:
            existing_cps = Counterparty.objects.filter(entity_id__in=merchant_map.keys())
            existing_cp_map = {c.entity_id: c for c in existing_cps}
            to_create = []
            for eid, name in merchant_map.items():
                if eid not in existing_cp_map:
                    to_create.append(Counterparty(entity_id=eid, name=name or '', type='merchant', confidence_level=Counterparty.ConfidenceLevelChoices.LOW))
            if to_create:
                Counterparty.objects.bulk_create(to_create)
                # refresh existing map
                existing_cps = Counterparty.objects.filter(entity_id__in=merchant_map.keys())
                existing_cp_map = {c.entity_id: c for c in existing_cps}

        updated_instances = []
        for pd in plaid_data_list:
            txid = pd.get('transaction_id')
            if not txid:
                continue
            existing = existing_map.get(txid)
            if not existing:
                continue

            try:
                changed = self._prepare_update_data(existing, pd) or {}
            except Exception:
                changed = {}

            needs_update = False
            for field, val in changed.items():
                try:
                    if getattr(existing, field) != val:
                        setattr(existing, field, val)
                        needs_update = True
                except Exception:
                    setattr(existing, field, val)
                    needs_update = True

            if not needs_update:
                continue

            # Prefer full versioning via manager
            try:
                new_tx = self.create_new_version(existing, pd, user, sync_id)
                # Attach any merchant Counterparty for this payload to the new version
                entity_id = self.sanitize_str((pd.get('merchant_metadata') or {}).get('merchant_id'), allow_none=True)
                if not entity_id:
                    mn_local = self.sanitize_str(pd.get('merchant_name'), allow_none=True) or self.sanitize_str(pd.get('name'), allow_none=False)
                    mn_local = mn_local.strip() if mn_local else ''
                    entity_id = f"merchant:{mn_local}".lower() if mn_local else None
                if entity_id:
                    cp = existing_cp_map.get(entity_id)
                    if cp:
                        new_tx.counterparties.add(cp)
                updated_instances.append(new_tx)
            except Exception:
                # fallback to in-place save if versioning fails
                existing.save()
                # Attach counterparty to existing row on fallback as well
                entity_id = self.sanitize_str((pd.get('merchant_metadata') or {}).get('merchant_id'), allow_none=True)
                if not entity_id:
                    mn_local = self.sanitize_str(pd.get('merchant_name'), allow_none=True) or self.sanitize_str(pd.get('name'), allow_none=False)
                    mn_local = mn_local.strip() if mn_local else ''
                    entity_id = f"merchant:{mn_local}".lower() if mn_local else None
                if entity_id:
                    cp = existing_cp_map.get(entity_id)
                    if cp:
                        existing.counterparties.add(cp)
                updated_instances.append(existing)

        return updated_instances

    def version_history(self, tx):
        """
        Return a QuerySet of previous versions for the given transaction `tx`.
        """
        pks = []
        current = tx
        while current.previous_version:
            current = current.previous_version
            pks.append(current.pk)
        if not pks:
            return self.none()
        return self.filter(pk__in=pks)

    def create_new_version(self, tx, plaid_data, user, sync_id):
        """
        Create a new version for transaction `tx` using `plaid_data`.
        Copies M2M counterparties, flips is_current on previous version,
        and writes an audit log.
        """
        prepared = self._prepare_update_data(tx, plaid_data) or {}
        new_tx = self.create(
            transaction_id=f"{tx.transaction_id}-v{tx.version + 1}",
            previous_version=tx,
            version=tx.version + 1,
            is_current=False,
            plaid_item=tx.plaid_item,
            **prepared
        )

        # Copy M2M relationships
        new_tx.counterparties.set(tx.counterparties.all())

        # Update current flag on previous version
        tx.is_current = False
        tx.save(update_fields=['is_current'])

        # Create audit log
        TransactionAuditLog.objects.create(
            transaction=new_tx,
            user=user,
            action=TransactionAuditLog.ActionChoices.MODIFIED,
            previous_state=self._snapshot_state(tx),
            new_state=self._snapshot_state(new_tx),
            plaid_sync_id=sync_id
        )

        return new_tx



    def _prepare_update_data(self, tx, plaid_data):
        """
        Map Plaid API data to model fields for transaction `tx`.
        Normalizes types so equality checks don't spuriously trigger updates.
        """

        # Normalize amount -> Decimal(2 dp)
        amt_raw = plaid_data.get('amount', tx.amount)
        amount = None
        if amt_raw is None:
            amount = tx.amount
        else:
            # preserve Decimal if already one, else convert via str for stable rounding
            amount = Decimal(str(amt_raw))

        iso_code_raw = plaid_data.get('iso_currency_code') or plaid_data.get('currency') or tx.iso_currency_code
        iso_code = self.sanitize_str(iso_code_raw, allow_none=False)[:3]

        return {
            'account_id': self.sanitize_str(plaid_data.get('account_id', tx.account_id), allow_none=False),
            'amount': amount,
            'iso_currency_code': iso_code,
            'date': self._parse_date(plaid_data.get('date') or tx.date),
            'datetime': self._parse_datetime(plaid_data.get('datetime') or plaid_data.get('date') or tx.datetime),
            'authorized_date': self._parse_date(plaid_data.get('authorized_date') or tx.authorized_date),
            'authorized_datetime': self._parse_datetime(plaid_data.get('authorized_datetime') or tx.authorized_datetime),
            'name': self.sanitize_str(plaid_data.get('name', tx.name), allow_none=False),
            'merchant_name': self.sanitize_str(plaid_data.get('merchant_name', tx.merchant_name), allow_none=True),
            'payment_channel': plaid_data.get('payment_channel', tx.payment_channel),
            'pending': bool(plaid_data.get('pending', tx.pending)),
            'transaction_type': plaid_data.get('transaction_type', tx.transaction_type),
            'personal_finance_category': plaid_data.get('personal_finance_category', tx.personal_finance_category),
            'location': plaid_data.get('location', tx.location),
            'payment_meta': plaid_data.get('payment_meta', tx.payment_meta),
            'is_removed': bool(plaid_data.get('is_removed', tx.is_removed)),
        }


    def _snapshot_state(self, tx):
        """
        Lightweight JSON-serializable snapshot for audit logs.
        """
        return {
            'transaction_id': tx.transaction_id,
            'amount': float(tx.amount) if tx.amount is not None else None,
            'date': tx.date.isoformat() if tx.date else None,
            'merchant_name': tx.merchant_name,
            'counterparties': list(tx.counterparties.values_list('entity_id', flat=True)),
        }

    def get_audit_timeline(self, tx):
        """
        Return the audit log queryset for a transaction, ready for UI/feed ordering.
        """
        return tx.audit_logs.select_related('user').order_by('-timestamp')

class PlaidTransaction(models.Model):
    class PaymentChannelChoices(models.TextChoices):
        ONLINE = 'online', 'Online'
        IN_STORE = 'in store', 'In Store'
        OTHER = 'other', 'Other'

    class TransactionTypeChoices(models.TextChoices):
        DIGITAL = 'digital', 'Digital'
        PLACE = 'place', 'Place'
        SPECIAL = 'special', 'Special'
        UNRESOLVED = 'unresolved', 'Unresolved'

    objects = PlaidTransactionManager()

    # Core fields
    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    transaction_id = models.CharField(max_length=255)
    previous_version = models.ForeignKey('self', null=True, blank=True, on_delete=models.DO_NOTHING)
    version = models.PositiveIntegerField(default=1)
    is_current = models.BooleanField(default=True)
    
    # Relationships
    plaid_item = models.ForeignKey('ledger.PlaidItem', on_delete=models.CASCADE)
    counterparties = models.ManyToManyField(Counterparty, blank=True)
    
    # Transaction data
    account_id = models.CharField(max_length=255)
    amount = models.DecimalField(max_digits=12, decimal_places=2)
    iso_currency_code = models.CharField(max_length=3)
    date = models.DateField(null=True, blank=True)
    datetime = models.DateTimeField(null=True, blank=True)
    authorized_date = models.DateField(null=True, blank=True)
    authorized_datetime = models.DateTimeField(null=True, blank=True)
    name = models.CharField(max_length=255)
    merchant_name = models.CharField(max_length=255, null=True, blank=True)
    payment_channel = models.CharField(max_length=20, choices=PaymentChannelChoices.choices)
    pending = models.BooleanField()
    transaction_type = models.CharField(max_length=20, choices=TransactionTypeChoices.choices)
    is_removed = models.BooleanField(default=False)
    
    # JSON fields
    personal_finance_category = models.JSONField(null=True, blank=True)
    location = models.JSONField(null=True, blank=True)
    payment_meta = models.JSONField(null=True, blank=True)
    
    # Metadata
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [
            models.Index(fields=['account_id']),
            models.Index(fields=['date']),
            models.Index(fields=['is_current']),
            models.Index(fields=['previous_version']),
        ]
        ordering = ['-date']

    def __str__(self):
        return f"{self.date} - {self.name} ({self.amount} {self.iso_currency_code}) v{self.version}"


    def get_version_options(self):
        "Get version history for ui dropdown"
        return self.__class__.objects.version_history(self).values_list(
            'version',
            'updated_at',
        ).order_by('-version')


class TransactionAuditLog(models.Model):
    class ActionChoices(models.TextChoices):
        ADDED = 'ADDED', 'Added'
        MODIFIED = 'MODIFIED', 'Modified'
        REMOVED = 'REMOVED', 'Removed'

    uuid = models.UUIDField(default=uuid4, editable=False, primary_key=True)
    transaction = models.ForeignKey(
        PlaidTransaction, 
        on_delete=models.SET_NULL, 
        null=True,
        related_name='audit_logs'
    )
    user = models.ForeignKey(UserModel, on_delete=models.CASCADE)
    action = models.CharField(max_length=8, choices=ActionChoices.choices)
    timestamp = models.DateTimeField(auto_now_add=True)
    previous_state = models.JSONField(default=dict, blank=True)
    new_state = models.JSONField(default=dict, blank=True)
    plaid_sync_id = models.CharField(max_length=255, db_index=True)

    class Meta:
        ordering = ['-timestamp']
        indexes = [
            models.Index(fields=['transaction_id', 'timestamp']),
            models.Index(fields=['plaid_sync_id']),
        ]

    def __str__(self):
        return f"{self.action} - {self.transaction_id} @ {self.timestamp}"



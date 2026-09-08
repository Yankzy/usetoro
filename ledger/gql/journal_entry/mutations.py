import graphene
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.gql.journal_entry.types import JournalEntryNode
from core.schema import BaseMutation
from django.db import transaction
from django.core.exceptions import ValidationError
from ledger.models.transactions import TransactionModel
from ledger.io.io_core import get_localtime
from ledger.models.accounts import AccountModel
from ledger.models.unit import EntityUnitModel
from ledger.models.entity import EntityModel
import logging



logger = logging.getLogger(__file__)

class TransactionInput(graphene.InputObjectType):
    account_uuid = graphene.UUID(required=True)
    amount = graphene.Float(required=True)
    tx_type = graphene.String(required=True, description="Type of transaction: 'debit' or 'credit'")
    description = graphene.String(required=True, description="Description for the transaction")

class CreateJournalEntryWithTransactions(BaseMutation):
    _mutation_module = "ledger"
    _mutation_class = "CreateJournalEntryWithTransactions"
    async_mutations = False

    journal_entry = graphene.Field(JournalEntryNode, description="The created journal entry object")

    class Input(BaseMutation.Input):
        timestamp = graphene.DateTime(required=False)
        entity_uuid = graphene.UUID(required=True, description="UUID of the entityModel")
        description = graphene.String(required=False, description="Description for the journal entry")
        transactions = graphene.List(TransactionInput, required=True)

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(journal_entry=None, success=False, message="Authentication required.")
        
        data['user'] = user
        return cls.create(**data)

    @classmethod
    def create(cls, **kwargs):

        with transaction.atomic():
            try:
                user = kwargs['user']
                transactions = kwargs['transactions']
                je_description = kwargs['description']
                entity_uuid = kwargs['entity_uuid']


                entity = EntityModel.objects.get(uuid=entity_uuid)
                # ledger = LedgerModel.objects.get(name='general_ledger')
                ledger = entity.ledgermodel_set.get(name='general_ledger')
                # entity = ledger.entity

                if not (
                    user.is_superuser
                    or entity.admin_id == user.pk
                    or entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(journal_entry=None, success=False, message="Permission denied.")

                if ledger.is_locked():
                    return cls(journal_entry=None, success=False, message="Cannot create new Journal Entries on a locked Ledger.")

                timestamp = get_localtime()

                entity_unit = None
                try:
                    # entity_unit = EntityUnitModel.objects.get(name='general_unit')
                    entity_unit = entity.entityunitmodel_set.get(name='general_unit')
                except EntityUnitModel.DoesNotExist:
                    return cls(journal_entry=None, success=False, message="Entity unit not found.")
                if entity_unit.entity_id != entity.uuid:
                    return cls(journal_entry=None, success=False, message="Entity unit does not belong to the same entity as the ledger.")

                if not transactions or len(transactions) < 2:
                    return cls(journal_entry=None, success=False, message="At least two transactions (debit/credit) are required.")

                tx_objs = []
                for tx_input in transactions:
                    try:
                        account = AccountModel.objects.get(uuid=tx_input.account_uuid)
                    except AccountModel.DoesNotExist:
                        return cls(journal_entry=None, success=False, message=f"Account not found: {tx_input.account_uuid}")
                    if tx_input.tx_type not in ("debit", "credit"):
                        return cls(journal_entry=None, success=False, message="tx_type must be 'debit' or 'credit'.")
                    tx_objs.append({
                        "account": account,
                        "amount": tx_input.amount,
                        "tx_type": tx_input.tx_type,
                        "description": tx_input.description or je_description,
                    })

                debits = sum(tx["amount"] for tx in tx_objs if tx["tx_type"] == "debit")
                credits = sum(tx["amount"] for tx in tx_objs if tx["tx_type"] == "credit")
                if abs(debits - credits) > 1e-6:
                    return cls(journal_entry=None, success=False, message="Journal entry is not balanced (debits do not equal credits).")

                je_kwargs = {
                    "ledger": ledger,
                    "description": je_description,
                    "timestamp": timestamp,
                }
                if entity_unit:
                    je_kwargs["entity_unit"] = entity_unit
                journal_entry = JournalEntryModel(**je_kwargs)
                journal_entry.full_clean()
                journal_entry.save()

                for tx in tx_objs:
                    TransactionModel.objects.create(
                        journal_entry=journal_entry,
                        account=tx["account"],
                        amount=tx["amount"],
                        tx_type=tx["tx_type"],
                        description=tx["description"],
                    )

                return cls(
                    journal_entry=journal_entry,
                    success=True,
                    message="Journal entry and transactions created successfully."
                )
            except LedgerModel.DoesNotExist:
                return cls(journal_entry=None, success=False, message="Ledger not found.")
            except ValidationError as e:
                return cls(journal_entry=None, success=False, message=f"Validation error: {str(e)}")
            except Exception as e:
                return cls(journal_entry=None, success=False, message=f"Error: {str(e)}")



class UpdateTransactionInput(graphene.InputObjectType):
    account_uuid = graphene.UUID(required=True)
    amount = graphene.Float(required=True)
    tx_uuid = graphene.UUID(required=False)
    tx_type = graphene.String(required=True, description="Type of transaction: 'debit' or 'credit'")
    description = graphene.String(required=False, description="Description for the transaction")

class UpdateJournalEntry(BaseMutation):
    """
    Update an existing Journal Entry, including splitting into multiple transactions.
    """
    _mutation_module = "ledger"
    _mutation_class = "UpdateJournalEntry"
    async_mutations = False

    journal_entry = graphene.Field(JournalEntryNode, description="The updated journal entry object")
    success = graphene.Boolean()
    message = graphene.String()

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the journal entry to update")
        timestamp = graphene.DateTime(required=False, description="Timestamp for the journal entry")
        entity_unit_uuid = graphene.UUID(required=False, description="UUID of the entity unit (optional)")
        description = graphene.String(required=False, description="Description for the journal entry")
        reconciled = graphene.Boolean(required=False, description="Is the transactions reconciled?")
        transactions = graphene.List(UpdateTransactionInput, required=True, description="List of transactions (debits/credits)")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def update(cls, **kwargs):
        with transaction.atomic():
            try:
                user = kwargs['user']
                je_uuid = kwargs["uuid"]
                je_description = kwargs["description"]
                reconciled = kwargs.get('reconciled', False)

                # Validate JournalEntryModel
                try:
                    je = JournalEntryModel.objects.get(uuid=je_uuid)
                except JournalEntryModel.DoesNotExist:
                    return cls(journal_entry=None, success=False, message=f"Journal Entry not found: {je_uuid}")
                entity = je.ledger.entity

                # Permission check
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        journal_entry=None,
                        success=False,
                        message="Permission denied: you do not have rights to update this journal entry."
                    )

                # Check if locked
                if je.locked:
                    return cls(
                        journal_entry=None,
                        success=False,
                        message="Cannot update a locked Journal Entry."
                    )

                # Update fields
                if "timestamp" in kwargs and kwargs["timestamp"] is not None:
                    je.timestamp = kwargs["timestamp"]
                if "entity_unit_uuid" in kwargs and kwargs["entity_unit_uuid"] is not None:
                    je.entity_unit_id = kwargs["entity_unit_uuid"]
                if "description" in kwargs and kwargs["description"] is not None:
                    je.description = kwargs["description"]

                # Handle transactions
                transactions = kwargs.get("transactions", [])
                if not transactions:
                    return cls(journal_entry=None, success=False, message="At least one transaction (debit/credit) is required.")

                # Build a map of existing transactions by uuid (as string for comparison)
                existing_txs = {str(tx.uuid): tx for tx in je.transactionmodel_set.all()}
                input_uuids = set(str(tx_input.tx_uuid) for tx_input in transactions if tx_input.tx_uuid)

                # Update or create transactions
                for tx_input in transactions:
                    if tx_input.tx_type.lower() not in ("debit", "credit"):
                        return cls(journal_entry=None, success=False, message="tx_type must be 'debit' or 'credit'.")
                    if tx_input.tx_uuid:
                        db_tx = existing_txs.get(str(tx_input.tx_uuid))
                        if db_tx:
                            changed = any([
                                str(db_tx.account_id) != str(tx_input.account_uuid),
                                db_tx.amount != tx_input.amount,
                                db_tx.tx_type != tx_input.tx_type,
                                (db_tx.description or "") != (tx_input.description or "")
                            ])
                            if changed:
                                db_tx.account_id = tx_input.account_uuid
                                db_tx.amount = tx_input.amount
                                db_tx.tx_type = tx_input.tx_type
                                db_tx.description = tx_input.description or db_tx.description
                                db_tx.save()
                        else:
                            return cls(journal_entry=None, success=False, message=f"Transaction with uuid {tx_input.tx_uuid} not found.")
                    else:
                        # Create new transaction
                        TransactionModel.objects.create(
                            journal_entry=je,
                            account_id=tx_input.account_uuid,
                            amount=tx_input.amount,
                            tx_type=tx_input.tx_type,
                            description=tx_input.description or je_description,
                            reconciled=reconciled
                        )

                # Delete transactions that were removed in the input
                for tx_uuid, db_tx in existing_txs.items():
                    if tx_uuid not in input_uuids:
                        db_tx.delete()

                # After all changes, check balance if reconciled
                if reconciled:
                    all_txs = je.transactionmodel_set.all()
                    debits = sum(tx.amount for tx in all_txs if tx.tx_type == "debit")
                    credits = sum(tx.amount for tx in all_txs if tx.tx_type == "credit")
                    if abs(debits - credits) > 1e-6:
                        raise Exception("Journal entry is not balanced (debits do not equal credits).")

                je.full_clean()
                je.save()

                return cls(
                    journal_entry=je,
                    success=True,
                    message="Journal entry and transactions updated successfully."
                )
            except JournalEntryModel.DoesNotExist:
                return cls(
                    journal_entry=None,
                    success=False,
                    message="Journal entry not found."
                )
            except ValidationError as e:
                return cls(
                    journal_entry=None,
                    success=False,
                    message=f"Validation error: {str(e)}"
                )
            except Exception as e:
                return cls(
                    journal_entry=None,
                    success=False,
                    message=f"Error updating journal entry: {str(e)}"
                )


    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(journal_entry=None, success=False, message="Authentication required.")
        
        data['user'] = user
        return cls.update(**data)



class DeleteJournalEntry(BaseMutation):
    """
    Delete a Journal Entry.
    """
    _mutation_module = "ledger"
    _mutation_class = "DeleteJournalEntry"
    async_mutations = False

    success = graphene.Boolean()
    message = graphene.String()

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the journal entry to delete")


    @classmethod
    def async_mutate(cls, user, **kwargs):
        with transaction.atomic():
            try:
                uuid = kwargs["uuid"]
                je = JournalEntryModel.objects.get(uuid=uuid)
                entity = je.ledger.entity

                # Permission check
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        success=False,
                        message="Permission denied: you do not have rights to delete this journal entry."
                    )

                je.delete()
                return []
            except JournalEntryModel.DoesNotExist:
                return cls(
                    success=False,
                    message="Journal entry not found."
                )
            except Exception as e:
                return cls(
                    success=False,
                    message=f"Error deleting journal entry: {str(e)}"
                )

  

class TransactionInput(graphene.InputObjectType):
    uuid = graphene.UUID(required=False)
    account_uuid = graphene.UUID(required=True)
    amount = graphene.Float(required=True)
    description = graphene.String(required=False)
    # Add other fields as needed

class BulkUpdateJournalEntryTransactions(BaseMutation):
    """
    Safely update transactions for a journal entry (no mass deletion).
    """
    _mutation_module = "ledger"
    _mutation_class = "BulkUpdateJournalEntryTransactions"
    async_mutations = False

    class Input(BaseMutation.Input):
        journal_entry_uuid = graphene.UUID(required=True)
        transactions = graphene.List(TransactionInput, required=True)


    @classmethod
    def async_mutate(cls, user, **kwargs):
        with transaction.atomic():
            try:
                je = JournalEntryModel.objects.get(uuid=kwargs["journal_entry_uuid"])
                entity = je.ledger.entity
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(success=False, message="Permission denied.")

                if je.locked or je.posted:
                    return cls(success=False, message="Cannot update transactions for a locked or posted journal entry.")

                # Build a map of existing transactions
                existing_txs = {str(tx.uuid): tx for tx in je.transactionmodel_set.all()}
                updated_uuids = set()

                # Update or create transactions
                for tx_input in kwargs["transactions"]:
                    if tx_input.uuid and str(tx_input.uuid) in existing_txs:
                        tx = existing_txs[str(tx_input.uuid)]
                        tx.account_id = tx_input.account_uuid
                        tx.amount = tx_input.amount
                        tx.description = tx_input.description or ""
                        tx.save()
                        updated_uuids.add(str(tx_input.uuid))
                    else:
                        TransactionModel.objects.create(
                            journal_entry=je,
                            account_id=tx_input.account_uuid,
                            amount=tx_input.amount,
                            description=tx_input.description or "",
                        )

                # Optionally, do NOT delete any transactions not in the input list.
                # If you want to allow marking as reversed/inactive, do it here.

                # Validate that debits == credits
                total = sum(tx.amount for tx in je.transactionmodel_set.all())
                if abs(total) > 1e-6:
                    raise Exception("Journal entry is not balanced (debits do not equal credits).")

                return cls(success=True, message="Transactions updated successfully.")
            except JournalEntryModel.DoesNotExist:
                return cls(success=False, message="Journal entry not found.")
            except Exception as e:
                return cls(success=False, message=f"Error: {str(e)}")
            

class JournalEntryStateMutation(BaseMutation):
    """
    Perform a state transition (lock, unlock, post, unpost) on a Journal Entry.
    """
    _mutation_module = "ledger"
    _mutation_class = "JournalEntryStateMutation"
    async_mutations = False

    journal_entry = graphene.Field(JournalEntryNode, description="The updated journal entry object")

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the journal entry to operate on")
        action = graphene.String(
            required=True,
            description="Action to perform: LOCK, UNLOCK, POST, UNPOST"
        )

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def mutate_state(cls, user, **kwargs):
        with transaction.atomic():
            try:
                uuid = kwargs["uuid"]
                action = kwargs["action"].strip().lower()
                je = JournalEntryModel.objects.get(uuid=uuid)
                entity = je.ledger.entity

                # Permission check
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        journal_entry=None,
                        success=False,
                        message="Permission denied: you do not have rights to change the state of this journal entry."
                    )

                method_map = {
                    'lock': je.lock,
                    'unlock': je.unlock,
                    'post': je.post,
                    'unpost': je.unpost,
                }
                if action not in method_map:
                    return cls(
                        journal_entry=None,
                        success=False,
                        message=f"Invalid action: '{action}'. Valid actions are: LOCK, UNLOCK, POST, UNPOST."
                    )
                method_map[action](commit=True, raise_exception=True)
                je.refresh_from_db()
                return cls(
                    journal_entry=je,
                    success=True,
                    message=f"Action '{action}' performed successfully."
                )
            except JournalEntryModel.DoesNotExist:
                return cls(
                    journal_entry=None,
                    success=False,
                    message="Journal entry not found."
                )
            except Exception as e:
                return cls(
                    journal_entry=None,
                    success=False,
                    message=f"Error changing journal entry state: {str(e)}"
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(journal_entry=None, success=False, message="Authentication required.")
        return cls.mutate_state(user, **data)
    


import graphene
from ledger.models.chart_of_accounts import ChartOfAccountModel
from ledger.gql.account.types import AccountNode
from core.schema import BaseMutation
from django.db import transaction
from django.core.exceptions import ValidationError

    
   

class CreateAccount(BaseMutation):
    """
    Create a new account in the system.
    This mutation allows the creation of an account with specified attributes.
    """

    _mutation_module = "ledger"
    _mutation_class = "CreateAccount"
    async_mutations = False
    account = graphene.Field(AccountNode, description="The created account object")

    class Input(BaseMutation.Input):
        code = graphene.String(required=False, description="Account code (unique per chart)")
        name = graphene.String(required=True, description="Account name")
        role = graphene.String(required=True, description="Account role (from predefined choices)")
        role_default = graphene.Boolean(required=False, description="Is this the default for its role?")
        balance_type = graphene.String(required=True, description="'debit' or 'credit'")
        locked = graphene.Boolean(required=False, description="Is the account locked?")
        is_role_default = graphene.Boolean(required=False, description="Is this the default for its role?")
        active = graphene.Boolean(required=False, description="Is the account active?")
        coa_model_uuid = graphene.UUID(required=True, description="UUID of the ChartOfAccountModel this account belongs to")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        """
        Create account and store it in mutation_results. Return [] on success, or list of error dicts.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        try:
            mutation_results = kwargs.get("mutation_results", {})
            with transaction.atomic():
                coa_model_uuid = kwargs.get('coa_model_uuid')
                coa_model = ChartOfAccountModel.objects.get(uuid=coa_model_uuid)
                name = kwargs.get('name')
                role = kwargs.get('role')
                balance_type = kwargs.get('balance_type')
                code = kwargs.get('code')
                active = kwargs.get('active', True)
                role_default = kwargs.get('role_default', False)
                locked = kwargs.get('locked', False)

                account_model = coa_model.create_account(
                    code=code,
                    role=role,
                    name=name,
                    balance_type=balance_type,
                    active=active
                )
                if role_default is not None:
                    account_model.role_default = role_default
                if locked is not None:
                    account_model.locked = locked
                account_model.save(update_fields=['role_default', 'locked'])

                mutation_results["account"] = account_model
            return []
        except ChartOfAccountModel.DoesNotExist:
            return [{
                "message": "ChartOfAccountModel with this UUID does not exist.",
                "user_message": "Invalid Chart of Account UUID."
            }]
        except Exception as e:
            return [{
                "message": str(e),
                "user_message": "Failed to create account."
            }]

    @classmethod
    def create(cls, **kwargs):
        with transaction.atomic():
            try:
                coa_model_uuid = kwargs.pop('coa_model_uuid')
                coa_model = ChartOfAccountModel.objects.get(uuid=coa_model_uuid)
                # Prepare arguments for create_account
                name = kwargs.pop('name')
                role = kwargs.pop('role')
                balance_type = kwargs.pop('balance_type')
                code = kwargs.pop('code', None)
                active = kwargs.pop('active', True)
                # role_default and locked are optional
                role_default = kwargs.pop('role_default', False)
                locked = kwargs.pop('locked', False)

                # Use create_account method
                account_model = coa_model.create_account(
                    code=code,
                    role=role,
                    name=name,
                    balance_type=balance_type,
                    active=active
                )
                # Set additional fields if needed
                if role_default is not None:
                    account_model.role_default = role_default
                if locked is not None:
                    account_model.locked = locked
                account_model.save(update_fields=['role_default', 'locked'])

                return account_model
            except ChartOfAccountModel.DoesNotExist:
                return cls(
                    account=None,
                    message="ChartOfAccountModel with this UUID does not exist.",
                    user_message="Invalid Chart of Account UUID.",
                    success=False
                )
            except Exception as e:
                return cls(
                    account=None,
                    message=str(e),
                    user_message="Failed to create account.",
                    success=False
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        """
        Executes BaseMutation lifecycle (including async_mutate), then returns account field.
        """
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        mutation_instance.message = "Account created successfully."
        return mutation_instance
        

class UpdateAccount(BaseMutation):
    """
    Update an existing account in the system.
    This mutation allows updating any mutable field of an account, and (optionally) moving it in the tree.
    """

    _mutation_module = "ledger"
    _mutation_class = "UpdateAccount"
    async_mutations = False
    account = graphene.Field(AccountNode, description="The updated account object")

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the account to update")
        code = graphene.String(required=False, description="Account code (unique per chart)")
        name = graphene.String(required=False, description="Account name")
        balance_type = graphene.String(required=False, description="'debit' or 'credit'")
        role_default = graphene.Boolean(required=False, description="Is this the default for its role?")
        active = graphene.Boolean(required=False, description="Is the account active?")
        locked = graphene.Boolean(required=False, description="Is the account locked?")
        # Tree movement support (optional, matches MoveNodeForm)
        position = graphene.String(required=False, description="Treebeard position (e.g., 'first-child', 'left', etc.)")
        ref_node_id = graphene.UUID(required=False, description="Reference node UUID for tree movement")

    @classmethod
    def async_mutate(cls, user, **data):
        """
        Update account and store it in mutation_results.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        from ledger.models.accounts import AccountModel
        try:
            mutation_results = data.get("mutation_results", {})
            with transaction.atomic():
                uuid = data.get('uuid')
                try:
                    account = AccountModel.objects.get(uuid=uuid)
                except AccountModel.DoesNotExist:
                    return [{"message": "Account with this UUID does not exist.", "user_message": "Invalid Account UUID."}]

                allowed_fields = ['code', 'name', 'balance_type', 'role_default', 'active', 'locked']
                for field in allowed_fields:
                    if field in data and data[field] is not None:
                        setattr(account, field, data[field])
                account.full_clean()
                account.save()

                position = data.get('position')
                ref_node_id = data.get('ref_node_id')
                if position and ref_node_id:
                    ref_node = AccountModel.objects.get(uuid=ref_node_id)
                    account.move(ref_node, pos=position)

                mutation_results["account"] = account
            return []
        except ValidationError as e:
            return [{"message": "Validation error: " + str(e), "user_message": "Invalid data provided."}]
        except Exception as e:
            return [{"message": str(e), "user_message": "Failed to update account."}]

    @classmethod
    def update(cls, **kwargs):
        from ledger.models.accounts import AccountModel
        from django.core.exceptions import ValidationError

        with transaction.atomic():
            uuid = kwargs.pop('uuid')
            try:
                account = AccountModel.objects.get(uuid=uuid)
                # Only update allowed fields (do NOT allow role or coa_model changes)
                allowed_fields = ['code', 'name', 'balance_type', 'role_default', 'active', 'locked']
                for field in allowed_fields:
                    if field in kwargs and kwargs[field] is not None:
                        setattr(account, field, kwargs[field])
                account.full_clean()
                account.save()
                # Handle tree movement if requested
                position = kwargs.get('position')
                ref_node_id = kwargs.get('ref_node_id')
                if position and ref_node_id:
                    ref_node = AccountModel.objects.get(uuid=ref_node_id)
                    account.move(ref_node, pos=position)
                return account
            except AccountModel.DoesNotExist:
                return cls(
                    account=None,
                    message="Account with this UUID does not exist.",
                    user_message="Invalid Account UUID.",
                    success=False
                )
            except ValidationError as e:
                return cls(
                    account=None,
                    message="Validation error: " + str(e),
                    user_message="Invalid data provided.",
                    success=False,
                )
            except Exception as e:
                return cls(
                    account=None,
                    message=str(e),
                    user_message="Failed to update account.",
                    success=False
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return 
            
        mutation_instance.message = "Account updated successfully."
        return mutation_instance

class AccountStateMutation(BaseMutation):
    """
    Dynamically perform a state transition (activate, deactivate, lock, unlock) on an account.
    """
    _mutation_module = "ledger"
    _mutation_class = "AccountStateMutation"
    async_mutations = False

    account = graphene.Field(AccountNode, description="The updated account object")
    message = graphene.String()
    success = graphene.Boolean()

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the account to operate on")
        action = graphene.String(
            required=True,
            description="Action to perform: ACTIVATE, DEACTIVATE, LOCK, UNLOCK"
        )

    @classmethod
    def async_mutate(cls, user, **kwargs):
        """
        Perform action on account and store it in mutation_results.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        from ledger.models.accounts import AccountModel
        try:
            mutation_results = kwargs.get("mutation_results", {})
            with transaction.atomic():
                uuid = kwargs.get("uuid")
                action = kwargs.get("action", "").lower()
                try:
                    account = AccountModel.objects.get(uuid=uuid)
                except AccountModel.DoesNotExist:
                    return [{"message": "Account with this UUID does not exist.", "user_message": "Invalid Account UUID."}]

                method_map = {
                    'activate': account.activate,
                    'deactivate': account.deactivate,
                    'lock': account.lock,
                    'unlock': account.unlock,
                }
                if action not in method_map:
                    return [{"message": f"Invalid action: '{action}', valid actions are: ACTIVATE, DEACTIVATE, LOCK, UNLOCK.", "user_message": "Invalid action."}]
                method_map[action](commit=True, raise_exception=True)
                account.refresh_from_db()
                mutation_results["account"] = account
            return []
        except Exception as e:
            return [{"message": str(e), "user_message": "Failed to change account state."}]

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        action = (data.get("action") or "").lower()
        mutation_instance.message = f"Action '{action}' performed successfully." if action else "Action performed successfully."
        return mutation_instance
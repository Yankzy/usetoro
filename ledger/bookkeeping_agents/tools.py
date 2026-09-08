from typing import Optional, List, Any, Tuple, Union
from agents import function_tool
from asgiref.sync import sync_to_async
from ledger.models import (
    ChartOfAccountModel,
    JournalEntryModel,
    LedgerModel,
    AccountModel,
    TransactionModel,
    EntityUnitModel,
    EntityModel,
    BankAccountModel,
)
from assistant.context import UnifiedContext
from ledger.io.io_core import get_localtime
from agents import RunContextWrapper, function_tool
from typing import Optional, List, Any, Dict
import logging
from .types import *
from ledger.io.roles import ACCOUNT_ROLE_CHOICES_FOR_FORMS
from itertools import chain
from typing import List, Dict, Any
from asgiref.sync import async_to_sync
from channels.layers import get_channel_layer
from datetime import datetime, timezone
import uuid
from core.registry import get_service, ServiceRegistryError
from .embeddings import embeddings_manager
import asyncio



logger = logging.getLogger(__name__)


async def tool_notification(user_uuid: Optional[str], tool_name: str, error_message: str, meta: Optional[Dict[str, Any]] = None) -> None:
    try:
        if not user_uuid:
            return
        channel_layer = get_channel_layer()
        if not channel_layer:
            return
        # Create a chat session with an assistant message explaining the error
        session_id = str(uuid.uuid4())
        assistant_text = (
            f"I noticed an error while running '{tool_name}'. "
            f"Details: {error_message}. "
            f"Let's resolve this—could you confirm the inputs or context for that step?"
        )
        try:
            save_ai_chat = get_service('assistant.save_ai_chat')
            save_ai_chat.delay(
                module_id="ledger",
                session_id=session_id,
                role="assistant",
                content=assistant_text,
                user_uuid=uuid.UUID(user_uuid),
                entity_for_session=None
            )
        except ServiceRegistryError as e:
            logger.warning(f"save_ai_chat not available: {e}")
        except Exception:
            # Continue even if persisting chat fails; still push notification
            session_id = None
        async_to_sync(channel_layer.group_send)(
            f"tool_notifications{user_uuid}",
            {
                "type": "tool_notifications",
                "title": "Account suggestion tool failed",
                "message": error_message,
                "tool_name": tool_name,
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "session_id": session_id,
                "meta": meta or {},
            },
        )
    except Exception:
        pass



def _canonicalize_condition(condition: Dict[str, Any]) -> Tuple:
    """
    Normalizes a rule condition into a consistent, hashable format for comparison.
    """
    field = condition.get("field", "").lower()
    operator = condition.get("operator", "").lower()
    value = condition.get("value", "")

    if operator == 'in' and isinstance(value, str):
        items = sorted([item.strip().lower() for item in value.split(',')])
        processed_value = ",".join(items)
    elif isinstance(value, str):
        processed_value = value.lower()
    else:
        processed_value = value

    return (field, operator, processed_value)

class CreateBusinessEntityInput(BaseModel):
    name: str = Field(..., description="The name of the business entity or project.")
    is_ephemeral: bool = Field(..., description="Whether this is an ephemeral (temporary) project that can be deleted easily, or a permanent company.")
    

def _create_entity_sync(user_uuid, name, is_ephemeral, fy_start_month, accrual_method):
    from ledger.gql.entity.mutations import CreateEntityModel
    from core.models import User
    
    user = User.objects.filter(user_uuid=user_uuid).first()
    if not user:
        return {"success": False, "error": "User not found."}
        
    kwargs = {
        "name": name,
        "is_ephemeral": is_ephemeral,
        "fy_start_month": 1,
        "accrual_method": accrual_method,
        "user": user,
        "activate_all_accounts": True,
    }
    
    result = CreateEntityModel.create(**kwargs)
    
    if hasattr(result, 'success') and not result.success:
        return {"success": False, "error": getattr(result, 'message', 'Failed to create entity.')}
        
    entity = getattr(result, 'entity', None)
    if not entity:
        return {"success": False, "error": "Creation succeeded but entity object was not returned."}
        
    return {
        "success": True, 
        "message": f"Successfully created entity '{entity.name}' (Ephemeral: {entity.is_ephemeral}).",
        "entity_id": str(entity.uuid)
    }


@function_tool(strict_mode=False)
async def create_business_entity(
    wrapper: RunContextWrapper[UnifiedContext],
    args: CreateBusinessEntityInput
) -> dict:
    """
    Creates a new business entity or temporary/ephemeral project for the user.
    
    CRITICAL: Before calling this tool, YOU MUST ASK the user for:
    1. The name of the entity/project.
    2. Whether this is intended to be a temporary project (ephemeral) or a permanent company.
    
    If the user does not specify the accounting method or fiscal year start month, you may proceed with the default values.
    """
    try:
        return await sync_to_async(_create_entity_sync)(
            wrapper.context.user_uuid,
            args.name,
            args.is_ephemeral,
            1,
            False,
        )
    except Exception as e:
        logger.exception(f"Failed to create business entity: {e}")
        return {"success": False, "error": str(e)}





@function_tool(strict_mode=False)
async def list_chart_of_accounts_local(
    wrapper: RunContextWrapper[LocalContext],
    search_params: CoASearchType
) -> dict:
    """
    Retrieves a list of accounts from the entity's Chart of Accounts. 
    It can be filtered by account type and keywords to find the most relevant accounts.

    Behavior:
    - If 'search_keywords' are provided and no accounts are found, the tool will automatically
      fall back to a broader search using only the 'account_types' filter (if provided).
      It will notify the agent of this action in a message.

    Args:
        account_types: Optional. A list of balance types to filter by. Use ['debit'] for expenses/assets and ['credit'] for income/liabilities.
        search_keywords: Optional. A list of keywords to search for within the account name or role to find the most specific accounts.
    """
    # Get the pre-fetched list from the context
    all_accounts = wrapper.context.accounts_snapshot
    if not all_accounts:
        return {"success": False, "error": "Accounts snapshot not found in context."}

    # Perform filtering in-memory
    filtered_accounts = all_accounts

    account_types = search_params.account_types
    search_keywords = search_params.search_keywords
    if account_types:
        filtered_accounts = [
            acc for acc in filtered_accounts if acc['balance_type'] in account_types
        ]

    if search_keywords:
        # This search can be made more sophisticated if needed
        results = []
        for acc in filtered_accounts:
            for keyword in [k.lower() for k in search_keywords]:
                if keyword in acc['name'].lower() or (acc['role'] and keyword in acc['role'].lower()):
                    results.append(acc)
                    break # Avoid adding the same account multiple times
        filtered_accounts = results
    
    # Filter out uuids 
    accounts_for_llm = [
        {
            "index": acc["index"],
            "name": acc["name"],
            "role": acc["role"],
            "balance_type": acc["balance_type"],
        }
        for acc in filtered_accounts
    ]

    return {"success": True, "accounts": accounts_for_llm}


@function_tool(strict_mode=False)
async def find_similar_accounts(
    wrapper: RunContextWrapper[LocalContext],
    args: AccountProposalType
) -> List[Dict[str, Any]]:
    """
    Finds existing accounts with similar names using vector embeddings, returning only
    accounts that have the exact same role as the proposed role.

    Args:
        wrapper: The RunContextWrapper containing the current context.
        args: The arguments for creating an account.

    Returns:
        A list of dictionaries with 'name', 'role', and 'similarity_score'.
    """
    # Prepare context and inputs
    target_role = getattr(args, "role", None)
    if not target_role or not isinstance(target_role, str):
        logger.warning(f"Invalid or missing role in args: {args}")
        return []

    # Resolve entity name for the embeddings namespace
    entity_name = wrapper.context.entity_name
    if not entity_name:
        try:
            entity = await wrapper.context.get_entity()
            entity_name = getattr(entity, "name", None)
        except Exception:
            entity_name = None

    if not entity_name:
        logger.warning("Entity name not available; cannot query embeddings index.")
        return []

    # Query embedding index (run sync code off the event loop)
    try:
        results = await sync_to_async(embeddings_manager.get_coa_context_by_vector)(
            entity_name,
            args.name,
        )
    except Exception as e:
        logger.error(f"Embeddings query failed: {e}")
        return []

    if not results:
        return []

    # Threshold for cosine similarity (0..1)
    similarity_threshold = 0.70

    similar_accounts: List[Dict[str, Any]] = []
    seen: set = set()
    for hit in results:
        score = hit.get("similarity_score")
        if score is None or score < similarity_threshold:
            continue

        # Always parse from chunk_text to avoid relying on transient indices
        chunk = (hit.get("chunk_text") or "")
        name_val, role_val = None, None
        try:
            parts = [p.strip() for p in chunk.split('|')]
            if parts:
                name_val = parts[0]
            for p in parts[1:]:
                if p.lower().startswith("role:"):
                    role_val = p.split(':', 1)[1].strip()
                    break
        except Exception:
            pass

        if not name_val or not role_val:
            continue

        # Enforce exact role match with the proposed role
        if role_val != target_role:
            continue

        dedup_key = (name_val.lower(), role_val)
        if dedup_key in seen:
            continue
        seen.add(dedup_key)

        similar_accounts.append({
            "name": name_val,
            "role": role_val,
            "similarity_score": round(float(score), 2),
        })

    similar_accounts.sort(key=lambda x: x["similarity_score"], reverse=True)
    return similar_accounts


@function_tool(strict_mode=False)
async def validate_account_proposal(
    wrapper: RunContextWrapper[LocalContext],
    arg: AccountProposalType,
) -> ValidationResultType:
    """
    Validates a new account proposal against double-entry accounting rules and allowed roles.
    This function ONLY performs validation checks.
    """
    ROLE_PREFIX_TO_BALANCE: Dict[str, str] = {
        "asset": "DEBIT",
        "ex":    "DEBIT",
        "cogs":  "DEBIT",
        "lia":   "CREDIT",
        "eq":    "CREDIT",
        "in":    "CREDIT",
    }

    # 1. Validate role exists
    valid_roles = {role[0] for category in ACCOUNT_ROLE_CHOICES_FOR_FORMS for role in category[1]}
    if arg.role not in valid_roles:
        return ValidationResultType(
            is_valid=False,
            reason=f"FAIL: Proposed role '{arg.role}' is not a valid account role."
        )

    # 2. Validate balance_type based on the role
    role_prefix = arg.role.split('_')[0]
    expected_balance_type = ROLE_PREFIX_TO_BALANCE.get(role_prefix)
    
    if not expected_balance_type:
        return ValidationResultType(
            is_valid=False,
            reason=f"FAIL: The role prefix '{role_prefix}' is not a recognized account category."
        )

    if expected_balance_type == arg.balance_type:
        # If everything is valid, return a success message.
        return ValidationResultType(
            is_valid=True,
            reason="OK: Proposal has a valid role and a correct balance_type for that role."
        )
    else:
        # Generate a specific failure reason.
        reason = (
            f"FAIL: The role '{arg.role}' must have a {expected_balance_type} balance, "
            f"but '{arg.balance_type}' was proposed."
        )
        return ValidationResultType(
            is_valid=False,
            reason=reason
        )


@function_tool(strict_mode=False)
def validate_roles(arg: RoleType) -> str:
    """
    Use this tool to validate your proposed role before finalizing your account suggestion.
    
    Parameters
    ----------
    role: str
        The role or list of roles to validate.

    Returns
    -------
    str
        A message indicating if role is valid
    """
    # A set is used for highly efficient membership checking (O(1) average time complexity).
    valid_roles_from_forms = {
        role[0] for role in chain.from_iterable(
            category[1] for category in ACCOUNT_ROLE_CHOICES_FOR_FORMS
        )
    }

    role_to_validate = arg.role
    
    # Perform a direct membership check on the single string.
    if role_to_validate in valid_roles_from_forms:
        return "Your proposed role is valid."
    else:
        return f"This role is invalid: {role_to_validate}."



@function_tool(strict_mode=False)
def get_roles_by_category(arg: RoleCategoryType) -> List[Dict[str, str]]:
    """
    Intelligently traverses and retrieves account roles for a specific high-level category.

    For example, providing the category 'Expense' will return all roles related to both 
    Cost of Goods Sold (COGS) and other general expenses.
    Categories you can search by are: Asset, Expense, Capital, Income and Liability.

    Parameters
    ----------
    category : str
        The high-level category to retrieve roles for Asset, Expense, Capital, Income and Liability. 
        This is case-insensitive.

    Returns
    -------
    List[Dict[str, str]]]
        A  list of {'role': str, 'label': str} dictionaries. 
        Returns an empty list if the category is not found.
    """
    category = arg.category
    normalized_category = category.strip().lower()

    for cat_name, roles_tuple in ACCOUNT_ROLE_CHOICES_FOR_FORMS:
        if cat_name.lower() == normalized_category:
            return [{'role': role[0], 'label': role[1]} for role in roles_tuple]
    
    return []



@function_tool(strict_mode=False)
def validate_rule_structure(rule: RuleGroupSuggestionType) -> Dict[str, Any]:
    """
    Performs a structural and logical validation of a proposed rule object.
    Checks for required keys, valid enum values, and the mandatory amount direction condition.

    Args:
        rule: The proposed rule object as a dictionary.

    Returns:
        A dictionary containing a boolean 'is_valid' and a list of error strings.
    """
    errors = []
    required_keys = {"name", "priority", "logic", "target_account_index", "conditions"}
    if not required_keys.issubset(rule.keys()):
        errors.append(f"Rule is missing one or more required keys: {required_keys - set(rule.keys())}")

    if rule.get("logic").upper() not in ["AND", "OR"]:
        errors.append(f"Invalid logic field Must be 'AND' or 'OR'.")

    conditions = rule.get("conditions", [])
    if not isinstance(conditions, list) or not conditions:
        errors.append("The 'conditions' field must be a non-empty list.")
    
    has_amount_direction = False
    for i, cond in enumerate(conditions):
        if not {"field", "operator", "value"}.issubset(cond.keys()):
            errors.append(f"Condition at index {i} is missing required keys.")
        if cond.get("field") == "amount" and cond.get("operator") in ["GT", "LT"]:
            has_amount_direction = True
            
    if not has_amount_direction:
        errors.append("Critical Error: Rule is missing a mandatory amount direction condition (e.g., amount > 0 or amount < 0).")
        
    return {"is_valid": not errors, "errors": errors}


@function_tool(strict_mode=False)
async def verify_target_account_exists(wrapper: RunContextWrapper, target_account_index: int) -> Dict[str, Any]:
    """
    Checks if an account with the specified index exists in the entity's Chart of Accounts.

    Args:
        target_account_index: The numerical index of the account to verify.

    Returns:
        A dictionary indicating if the account exists and including its details if found.
    """
    # by searching the in-memory snapshot of the Chart of Accounts.
    accounts_snapshot = wrapper.context.accounts_snapshot
    account = next((acc for acc in accounts_snapshot if acc.get("index") == target_account_index), None)

    if account:
        # We confirm and reinforce the details of the account in the context (by Repeating)
        return {
            "exists": True,
            "account_details": {
                "index": account["index"],
                "name": account["name"],
                "role": account["role"],
                "balance_type": account["balance_type"]
            }
        }
    else:
        return {
            "exists": False,
            "message": f"No account with index '{target_account_index}' was found in the Chart of Accounts."
        }


@function_tool(strict_mode=False)
async def find_similar_rules(
    wrapper: RunContextWrapper[LocalContext], 
    proposed_rule: Dict[str, Any],
) -> Dict[str, Any]:
    """
    Finds existing rules that are substantively similar to a proposed rule using a 
    Weighted Jaccard Similarity score to provide a more accurate, context-aware comparison.
    """
    target_account_index = proposed_rule.get("target_account_index")
    if target_account_index is None:
        return {"error": "Proposed rule is missing 'target_account_index'."}

    # Step 1: Find the stable UUID using the temporary index from the proposed rule.
    accounts_snapshot = wrapper.context.accounts_snapshot
    target_account_details = next(
        (acc for acc in accounts_snapshot if acc.get("index") == target_account_index), 
        None
    )
    
    if not target_account_details:
        return {"similar_rules_found": []}
        
    target_account_uuid = target_account_details.get("uuid")

    # Step 2: Use the stable UUID to filter the rules_snapshot.
    all_rules = wrapper.context.rules_snapshot
    relevant_rules = [
        rule for rule in all_rules 
        if rule.get("target_account_uuid") == target_account_uuid
    ]

    if not relevant_rules:
        return {"similar_rules_found": []}

    # Step 3: # Use Weighted Jaccard Similarity Algorithm. https://en.wikipedia.org/wiki/Jaccard_index
    CONDITION_FIELD_WEIGHTS = {
        "description": 5.0,
        "vendor": 4.0,
        "mcc": 2.5,
        "category": 2.0,
        "amount": 1.0,
    }

    proposed_conditions_weighted = {
        _canonicalize_condition(cond): CONDITION_FIELD_WEIGHTS.get(cond.get("field", "").lower(), 1.0)
        for cond in proposed_rule.get("conditions", [])
    }
    
    results = []
    for existing_rule in relevant_rules:
        existing_conditions_weighted = {
            _canonicalize_condition(cond): CONDITION_FIELD_WEIGHTS.get(cond.get("field", "").lower(), 1.0)
            for cond in existing_rule.get("conditions", [])
        }
        
        # Get the union of all unique conditions from both rules
        all_unique_conditions = set(proposed_conditions_weighted.keys()).union(existing_conditions_weighted.keys())
        
        weighted_intersection_sum = 0.0
        weighted_union_sum = 0.0
        
        for condition_key in all_unique_conditions:
            # Get the weight of the condition in each rule, defaulting to 0 if not present
            w_proposed = proposed_conditions_weighted.get(condition_key, 0.0)
            w_existing = existing_conditions_weighted.get(condition_key, 0.0)
            
            # Sum the minimums (intersection) and maximums (union)
            weighted_intersection_sum += min(w_proposed, w_existing)
            weighted_union_sum += max(w_proposed, w_existing)

        if weighted_union_sum == 0:
            similarity = 1.0 if not all_unique_conditions else 0.0
        else:
            similarity = weighted_intersection_sum / weighted_union_sum
            
        if similarity > 0.5:
            results.append({
                "rule_id": existing_rule.get("id"),
                "rule_name": existing_rule.get("name"),
                "similarity_score": round(similarity, 4),
                "reason": "Targets the same account and has a high weighted condition similarity."
            })
    
    results.sort(key=lambda x: x["similarity_score"], reverse=True)
    
    return {"similar_rules_found": results}



@function_tool(strict_mode=False)
async def get_account_by_index(
    wrapper: RunContextWrapper[LocalContext],
    index: int
) -> dict:
    """
    Verifies if an account with a specific index exists in the context and returns its details.
    Use this to confirm your choice before finalizing the output.
    
    Args:
        index: The exact index number of the account to verify from a previous call.
    """
    accounts_snapshot = wrapper.context.accounts_snapshot
    if not accounts_snapshot:
        return {"success": False, "error": "Accounts snapshot not found in context."}

    # Find the account with the matching index in the in-memory list
    # Note: This is an O(n) scan. For very large lists, a pre-computed dict/map would be faster.
    account = next((acc for acc in accounts_snapshot if acc.get("index") == index), None)

    if account:
        return {
            "success": True, 
            "account_found": True, 
            # Return details but OMIT the UUID to maintain the abstraction
            "account": {"index": account["index"], "name": account["name"], "role": account["role"]}
        }
    else:
        return {
            "success": True, 
            "account_found": False,
            "message": f"No account with the index '{index}' exists in the current context."
        }



@function_tool(strict_mode=False)
async def create_proposed_account(
    wrapper: RunContextWrapper[LocalContext],
    arg: AccountProposalType,
) -> ValidationResultType:
    """
    Schedules the account creation as a background task and returns immediately.
    """
    try:
        # Get the coroutine for creating an account
        account_creation_coro = wrapper.context.create_account(
            arg.name,
            arg.role,
            arg.balance_type,
        )

        # Schedule the coroutine to run on the event loop
        # This returns a Task object instantly; it does not wait.
        asyncio.create_task(account_creation_coro)
        
        # Your function now continues and returns immediately
        logger.info(f"Account creation for '{arg.name}' scheduled in background.")
        return CreateProposedAccountResultType(
            is_created=True,
            fail_reason=""
        )
        
    except Exception as e:
        # NOTE: This will ONLY catch errors from *scheduling* the task
        # (which is very rare), NOT errors from inside create_account.
        logger.error(f"Failed to schedule account creation task: {e}")
        return CreateProposedAccountResultType(
            is_created=False,
            fail_reason=f"Error: {e}"
        )



@function_tool(strict_mode=False)
def tool_failed_notification(
    wrapper: RunContextWrapper[LocalContext],
    arg: ToolFailedNotification,
):
    """
    Formats and sends the admin notification email using EmailAgent.
    """
    try:
        from core.tasks import notify_admin_by_email
        from django.utils import timezone
        event_summary = (
            f"The tool '{arg.tool_name}' failed to execute. "
            f"Error: {arg.error_message}. "
        )

        # Prepare the keyword arguments for the EmailAgent
        email_kwargs = {
            "event_type": "Tool Call Failed",
            "timestamp": timezone.now(),
            "user_identifier": "Agent",
            "ip_address": "N/A",
            "event_summary": event_summary,
        }

        # Use the EmailAgent to send the notification
        notify_admin_by_email.delay(**email_kwargs)
        
        return "Successfully sent tool failure notification to admin."
    except Exception as e:
        logger.error(f"An error occurred while sending tool failure notification: {e}", exc_info=True)
        return "Failed to send tool failure notification to admin."

class EntityInput(BaseModel):
    entity_name: Optional[str] = None

@function_tool(strict_mode=False)
async def set_active_business_entity(wrapper: RunContextWrapper[UnifiedContext], args: EntityInput) -> dict:
    """
    Sets the active business entity for the current session context.
    
    This tool MUST be called when the user indicates which business they want to manage, 
    and no entity is currently active in the session.
    
    Validation:
    - The entity must exist.
    - The entity MUST belong to the user (security check).
    """
    try:
        user = await wrapper.context.get_user()
        
        # We need to find the entity based on name or uuid
        entity = None
        
        if args.entity_name:
            # Lookup by Name and User (Ownership check)
            # We use filter in case there are duplicates (though unlikely for same user) or to handle case-insensitive search if needed? 
            # ideally exact match first.
            entities = await sync_to_async(list)(EntityModel.objects.filter(name__iexact=args.entity_name, admin=user))
            if not entities:
                 return {"success": False, "error": f"Business entity '{args.entity_name}' not found in your account."}
            if len(entities) > 1:
                return {"success": False, "error": f"Multiple entities found with name '{args.entity_name}'. Please specify by UUID or be more specific."}
            entity = entities[0]
            
        else:
            return {"success": False, "error": "You must provide either entity_name or entity_uuid."}

        # Update the context in the wrapper so subsequent tools in this run use it
        if wrapper and wrapper.context:
            wrapper.context.entity_uuid = str(entity.uuid)
            wrapper.context.entity_name = entity.name
            logger.info(f"Updated UnifiedContext with entity: {entity.name} ({entity.uuid})")
        
        return {
            "success": True, 
            "message": f"Active business entity set to: {entity.name}",
            "entity": {
                "uuid": str(entity.uuid),
                "name": entity.name
            }
        }

    except Exception as e:
        return {"success": False, "error": str(e)}

async def get_entity_for_uuid(uuid: str):
    return await sync_to_async(EntityModel.objects.get)(uuid=uuid)


# =========================
# LEDGER ACCOUNTS
# =========================


@function_tool(strict_mode=False)
async def get_banks_connected_by_entity(wrapper: RunContextWrapper[UnifiedContext]) -> dict:
    """
    Retrieve all bank connections for a Business.
    This tool is used to get the bank's the Business connected to their accuont for Bookkeeping.

    How to use:
        - The response will include the bank name and last 4 digits of the account number for each connection.
        - Never ask the user for their connected bank account, this tool will return the list of connected banks.

    Returns: dict
    """
    try:
        user = await wrapper.context.get_user()
        entity = await wrapper.context.get_entity()

        # Get all bank connections for the user
        bank_conns = await sync_to_async(list)(
            BankAccountModel.objects.for_entity(user=user).order_by("-created")
        )

        connections = []
        for bank in bank_conns:
            last4 = bank.account_number[-4:] if bank.account_number else ""
            connections.append({
                # "bank_id": str(conn.uuid),
                "last_4": f"**{last4}",
                "bank_name": bank.bank_name,
            })

        return {"success": True, "bank_connected": connections}
    except BankAccountModel.DoesNotExist:
        return {"success": False, "bank_connected": [], "error": "No bank connections, ask user to connect their bank account in app settings."}

    except Exception as e:
        return {"success": False, "bank_connected": [], "error": str(e)}
    

class AddLedgerAccountInput(BaseModel):
    # user_uuid removed
    coa_model_uuid: str
    name: str
    role: str
    balance_type: str

@function_tool(strict_mode=False)
async def add_ledger_account(wrapper: RunContextWrapper[UnifiedContext], args: AddLedgerAccountInput) -> dict:
    """
    Add a new account (e.g. asset, liability, income, expense) to a chart of accounts.
    """
    try:
        user = await wrapper.context.get_user()
        coa = await sync_to_async(ChartOfAccountModel.objects.get)(uuid=args.coa_model_uuid)
        account = await sync_to_async(AccountModel.objects.create)(
            name=args.name,
            role=args.role,
            balance_type=args.balance_type,
            coa_model=coa
        )
        return {"success": True, "account_id": str(account.uuid), "name": account.name}
    except Exception as e:
        return {"success": False, "error": str(e)}

class UpdateLedgerAccountInput(BaseModel):
    # user_uuid removed
    uuid: str
    code: Optional[str] = None
    name: Optional[str] = None
    balance_type: Optional[str] = None
    role_default: Optional[bool] = None
    active: Optional[bool] = None
    locked: Optional[bool] = None

@function_tool(strict_mode=False)
async def update_ledger_account(wrapper: RunContextWrapper[UnifiedContext], args: UpdateLedgerAccountInput) -> dict:
    """
    Update the details of a ledger account.
    """
    try:
        user = await wrapper.context.get_user()
        account = await sync_to_async(AccountModel.objects.get)(uuid=args.uuid)
        for field in ['code', 'name', 'balance_type', 'role_default', 'active', 'locked']:
            value = getattr(args, field)
            if value is not None:
                setattr(account, field, value)
        await sync_to_async(account.save)()
        return {"success": True, "account_id": str(account.uuid), "name": account.name}
    except Exception as e:
        return {"success": False, "error": str(e)}

class ChangeLedgerAccountStateInput(BaseModel):
    # user_uuid removed
    uuid: str
    action: str

@function_tool(strict_mode=False)
async def change_ledger_account_state(wrapper: RunContextWrapper[UnifiedContext], args: ChangeLedgerAccountStateInput) -> dict:
    """
    Change the state of a ledger account (activate, deactivate, lock, unlock).
    """
    try:
        user = await wrapper.context.get_user()
        account = await sync_to_async(AccountModel.objects.get)(uuid=args.uuid)
        action = args.action.lower()
        method_map = {
            'activate': account.activate,
            'deactivate': account.deactivate,
            'lock': account.lock,
            'unlock': account.unlock,
        }
        if action not in method_map:
            return {"success": False, "error": f"Invalid action: {args.action}"}
        await sync_to_async(method_map[action])(commit=True, raise_exception=True)
        await sync_to_async(account.refresh_from_db)()
        return {"success": True, "message": f"Action '{action}' performed successfully."}
    except Exception as e:
        return {"success": False, "error": str(e)}

@function_tool(strict_mode=False)
async def list_chart_of_accounts(wrapper: RunContextWrapper[UnifiedContext]) -> dict:
    """
    List all chart of accounts for an entity.
    """
    try:
        user = await wrapper.context.get_user()
        entity = await wrapper.context.get_entity()
        accounts = await sync_to_async(list)(
            AccountModel.objects.for_entity(
                user,
                entity
            )
        )
        return {
            "success": True,
            "accounts": [
                {
                    "uuid": str(a.uuid),
                    "name": a.name,
                    "role": a.role,
                    "balance_type": a.balance_type
                }
                for a in accounts
            ]
        }
    except Exception as e:
        logger.info(f"Error in list_chart_of_accounts: {str(e)}")
        return {"success": False, "error": str(e)}

class GetLedgerAccountInput(BaseModel):
    def __init__(self, **data):
        super().__init__(**data)

    # user_uuid removed
    uuid: str

@function_tool(strict_mode=False)
async def get_one_chart_of_account(wrapper: RunContextWrapper[UnifiedContext], args: GetLedgerAccountInput) -> dict:
    """
    Get details for a single ledger account by UUID.
    """
    try:
        user = await wrapper.context.get_user()
        account = await sync_to_async(AccountModel.objects.get)(uuid=args.uuid)
        return {
            "success": True,
            "account": {
                "uuid": str(account.uuid),
                "name": account.name,
                "role": account.role,
                "balance_type": account.balance_type,
                "active": account.active,
                "locked": account.locked,
            }
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

# =========================
# JOURNAL ENTRIES
# =========================

@function_tool(strict_mode=False)
async def list_ledger_accounts_for_journal_entry(wrapper: RunContextWrapper[UnifiedContext]) -> dict:
    """
    List all available ledger accounts for a given ledger, including, name, role, active, locked and balance_type.
    Use this before constructing a journal entry to ensure correct account references.
    """
    try:
        logger.info('LEDGER LIST CALLED')
        user = await wrapper.context.get_user()
        ledger = await sync_to_async(LedgerModel.objects.get)(name='general_ledger')
        entity = ledger.entity
        accounts = await sync_to_async(list)(
            AccountModel.objects.for_entity(user_model=user, entity_model=entity).available()
        )
        return {
            "success": True,
            "accounts": [
                {
                    "name": a.name,
                    "role": a.role,
                    "balance_type": a.balance_type,
                    "active": a.active,
                    "locked": a.locked,
                }
                for a in accounts
            ]
        }
    except Exception as e:
        return {"success": False, "error": str(e)}
    

class CreateJournalEntryInput(BaseModel):
    # user_uuid removed
    description: str
    account_to_debit_name: Optional[str] = None
    account_to_debit_role: Optional[str] = None
    account_to_credit_name: Optional[str] = None
    account_to_credit_role: Optional[str] = None
    transaction_amount: float

    

@function_tool(strict_mode=False)
async def create_journal_entry(wrapper: RunContextWrapper[UnifiedContext], args: CreateJournalEntryInput) -> dict:
    """
    Core Directive: Prioritize understanding the user's primary intent from their input.

    IF User Intent is "Create a Transaction Item" (or "Add Expense," "Record Income," "Log Payment," "New Transaction"):

    1. ACTION: Check Bank Account Connection Status.
        - FUNCTION: Call get_connected_bank_accounts.
        - EXPECTED OUTPUT: list of connected account names with last 4 digit.

    2. CONDITIONAL LOGIC based on Bank Connection Status:
        - SCENARIO A: User HAS Connected Bank Account(s).
            - ACTION 1: Confirm Payment Method.
                - AI's QUESTION: "I see you have connected following bank accounts. 
                    1. Bank A (**1234)
                    1. Bank B (**3456)
                    Was this transaction paid for using one of your connected bank accounts or a card linked to them?"

            - ACTION 2: Process User Response to Payment Method Question.
                - IF User Confirms "Yes" (Paid by Connected Bank/Card):
                    AI's ADVICE: "Great! To avoid duplicate entries and streamline your bookkeeping, DO NOT manually register this transaction. I will automatically detect and import it from your bank statements once available. This ensures accuracy and saves time. You will receive a notification when the transaction is successfully imported and ready for categorization. Would you like me to wait for the bank statement, or is there another detail you need to add about this transaction now?"

                - IF User Confirms "No" (Not Paid by Connected Bank/Card) OR Provides Different Payment Method (e.g., Cash, Other Account):
                    - AI's ACTION: Proceed with standard "Create a Transaction Item flow".
                    - AI's RESPONSE: "Understood. Since this transaction wasn't paid via a connected bank account/card, please provide the details for this transaction item (amount, date, description, category, payment method)."
                    - FUNCTION: Initiate start_manual_transaction_creation.

        SCENARIO B: User HAS NOT Connected Any Bank Account(s).
            - AI's ACTION: Proceed directly with standard "Create a Transaction Item flow".
            - AI's RESPONSE: "Okay, let's create a new transaction item. Please provide the details (e.g., amount, date, description, category, payment method)."
            - AI's PROACTIVE SUGGESTION (Optional): "By the way, connecting your bank accounts can significantly automate your bookkeeping by importing transactions directly. Would you be interested in learning how to connect your bank accounts?"
            - FUNCTION: Initiate start_manual_transaction_creation.

    # System-level instruction
    system: |
      You are “LedgerLyra,” a formal bookkeeping assistant powered by the OpenAI Agents SDK. 
      Your mission is to guide users through creating transaction items with precision and to leverage connected bank data when available.

      When a user expresses an intent to record a new transaction (phrases like “Create a Transaction Item,” “Add Expense,” “Record Income,” “Log Payment,” “New Transaction”), follow this flow:

      1. DETECT intent and IMMEDIATELY call the function get_connected_bank_accounts (no user-facing text yet).
      2. IF get_connected_bank_accounts returns a non‑empty list:
           a. Present the user with their connected accounts:
              “I see you have connected the following bank accounts:
               • {{account_name}} (**{{last4}})
               • …  
               Was this transaction paid using one of these connected accounts or a linked card?”
           b. WAIT for user confirmation:
              – IF “Yes”:  
                 Respond formally:  
                 “Excellent. To prevent duplicates, I will import this transaction automatically from your bank statement once it’s available. You’ll receive a notification when it’s ready for categorization. Would you like me to wait for that, or do you have any additional details to add now?”
              – IF “No” or any other payment method:
                 Invoke start_manual_transaction_creation and prompt:  
                 “Understood. Since this wasn’t paid via a connected account, please provide the transaction details: amount, date, description, category, and payment method.”
      3. IF get_connected_bank_accounts returns an empty list:
           Invoke start_manual_transaction_creation and prompt:
           “Okay, let’s create a new transaction item. Please share the details: amount, date, description, category, and payment method.”
           Optionally add: “By the way, connecting your bank accounts can automate bookkeeping by importing transactions directly. Would you like instructions to connect your accounts?”
      
      Always maintain a formal, precise bookkeeping tone.

    # Function definitions
    functions:
      - name: get_connected_bank_accounts
        description: Retrieve the user’s connected bank accounts.
        parameters: {}
      - name: start_manual_transaction_creation
        description: Begin the manual transaction entry flow.
        parameters: {}

    # User message placeholder
    user: "{{USER_INPUT}}"



        - Create a Transaction Item flow: Create a new journal entry in a ledger with debit and credit entries, using account name and role for disambiguation.
        - If transaction is an expense, ask the user how they paid for it so you can determine the credit account.
        - If transaction is income, ask the user how they received it or from which customer so you can determine the debit account.
        - If the user does not specify an amount, ask for it.
        - Ask the user what the transaction is about so you can craft a description.
    """
    try:
        user = await wrapper.context.get_user()

        ledger = await sync_to_async(LedgerModel.objects.get)(user=user, name='general_ledger')
        entity = ledger.entity

        if ledger.is_locked():
            return {"success": False, "error": "Cannot create new Journal Entries on a locked Ledger."}

        timestamp = get_localtime()

        # Use general_unit if available, else None
        try:
            entity_unit = await sync_to_async(EntityUnitModel.objects.get)(name='general_unit')
            if entity_unit.entity_id != entity.uuid:
                return {"success": False, "error": "Entity unit does not belong to the same entity as the ledger."}
        except EntityUnitModel.DoesNotExist:
            entity_unit = None

        # Find debit account
        debit_qs = AccountModel.objects.filter(
            name=args.account_to_debit_name,
            role=args.account_to_debit_role,
            coa_model__entity=entity,
            locked=False,
            active=True,
            coa_model__active=True,
        )
        debit_account = await sync_to_async(debit_qs.first)()
        if not debit_account:
            return {"success": False, "error": f"Debit account '{args.account_to_debit_name}' with role '{args.account_to_debit_role}' not found or not available."}

        # Find credit account
        credit_qs = AccountModel.objects.filter(
            name=args.account_to_credit_name,
            role=args.account_to_credit_role,
            coa_model__entity=entity,
            locked=False,
            active=True,
            coa_model__active=True,
        )
        credit_account = await sync_to_async(credit_qs.first)()
        if not credit_account:
            return {"success": False, "error": f"Credit account '{args.account_to_credit_name}' with role '{args.account_to_credit_role}' not found or not available."}

        if debit_account.uuid == credit_account.uuid:
            return {"success": False, "error": "Debit and credit accounts must be different."}

        
        amount = float(args.transaction_amount)

        from django.db import transaction as db_transaction
        async with sync_to_async(db_transaction.atomic)():
            je_kwargs = {
                "ledger": ledger,
                "description": args.description or "",
                "timestamp": timestamp,
            }
            if entity_unit:
                je_kwargs["entity_unit"] = entity_unit
            journal_entry = await sync_to_async(JournalEntryModel.objects.create)(**je_kwargs)

            await sync_to_async(TransactionModel.objects.create)(
                journal_entry=journal_entry,
                account=debit_account,
                amount=amount,
                tx_type="debit",
                description=args.description or "",
            )
            await sync_to_async(TransactionModel.objects.create)(
                journal_entry=journal_entry,
                account=credit_account,
                amount=amount,
                tx_type="credit",
                description=args.description or "",
            )

        return {
            "journal_entry_created": True,
            "transactions_created": True,
            "message for user": "Your transaction is recorded"
        }
    except Exception as e:
        return {"success": False, "error": str(e)}
    


class UpdateJournalEntryInput(BaseModel):
    # user_uuid removed
    uuid: str
    description: Optional[str] = None
    timestamp: Optional[Any] = None

@function_tool(strict_mode=False)
async def update_journal_entry(wrapper: RunContextWrapper[UnifiedContext], args: UpdateJournalEntryInput) -> dict:
    """
    Update the details of a journal entry.
    """
    try:
        user = await wrapper.context.get_user()
        je = await sync_to_async(JournalEntryModel.objects.get)(uuid=args.uuid)
        if args.description is not None:
            je.description = args.description
        if args.timestamp is not None:
            je.timestamp = args.timestamp
        await sync_to_async(je.save)()
        return {
            "success": True,
            "journal_entry_id": str(je.uuid),
            "description": je.description
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

class DeleteJournalEntryInput(BaseModel):
    # user_uuid removed
    uuid: str

@function_tool(strict_mode=False)
async def delete_journal_entry(wrapper: RunContextWrapper[UnifiedContext], args: DeleteJournalEntryInput) -> dict:
    """
    Delete a journal entry from the ledger.
    """
    try:
        user = await wrapper.context.get_user()
        
        je = await sync_to_async(JournalEntryModel.objects.get)(uuid=args.uuid)
        await sync_to_async(je.delete)()
        return {"success": True, "message": "Journal entry deleted."}
    except Exception as e:
        return {"success": False, "error": str(e)}

class ChangeJournalEntryStateInput(BaseModel):
    # user_uuid removed
    uuid: str
    action: str

@function_tool(strict_mode=False)
async def change_journal_entry_state(wrapper: RunContextWrapper[UnifiedContext], args: ChangeJournalEntryStateInput) -> dict:
    """
    Change the state of a journal entry (lock, unlock, post, unpost).
    """
    try:
        user = await wrapper.context.get_user()
        
        je = await sync_to_async(JournalEntryModel.objects.get)(uuid=args.uuid)
        action = args.action.lower()
        method_map = {
            'lock': je.lock,
            'unlock': je.unlock,
            'post': je.post,
            'unpost': je.unpost,
        }
        if action not in method_map:
            return {"success": False, "error": f"Invalid action: {args.action}"}
        await sync_to_async(method_map[action])(commit=True, raise_exception=True)
        await sync_to_async(je.refresh_from_db)()
        return {"success": True, "message": f"Action '{action}' performed successfully."}
    except Exception as e:
        return {"success": False, "error": str(e)}

class ListJournalEntriesInput(BaseModel):
    # user_uuid removed
    ledger_uuid: str

@function_tool(strict_mode=False)
async def list_journal_entries(wrapper: RunContextWrapper[UnifiedContext], args: ListJournalEntriesInput) -> dict:
    """
    List all journal entries for a ledger.
    """
    try:
        user = await wrapper.context.get_user()
        
        entries = await sync_to_async(list)(
            JournalEntryModel.objects.filter(ledger__uuid=args.ledger_uuid).order_by('-timestamp')
        )
        return {
            "success": True,
            "journal_entries": [
                {"uuid": str(e.uuid), "description": e.description, "timestamp": e.timestamp}
                for e in entries
            ]
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

class GetJournalEntryInput(BaseModel):
    # user_uuid removed
    uuid: str

@function_tool(strict_mode=False)
async def get_journal_entry(wrapper: RunContextWrapper[UnifiedContext], args: GetJournalEntryInput) -> dict:
    """
    Get details for a single journal entry by UUID.
    """
    try:
        user = await wrapper.context.get_user()
        
        je = await sync_to_async(JournalEntryModel.objects.get)(uuid=args.uuid)
        return {
            "success": True,
            "journal_entry": {
                "uuid": str(je.uuid),
                "description": je.description,
                "timestamp": je.timestamp,
            }
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

class ListJournalEntriesByYearInput(BaseModel):
    # user_uuid removed
    ledger_uuid: str
    year: int

@function_tool(strict_mode=False)
async def list_journal_entries_by_year(wrapper: RunContextWrapper[UnifiedContext], args: ListJournalEntriesByYearInput) -> dict:
    """
    List all journal entries for a ledger in a specific year.
    """
    try:
        user = await wrapper.context.get_user()
        
        entries = await sync_to_async(list)(
            JournalEntryModel.objects.filter(
                ledger__uuid=args.ledger_uuid,
                timestamp__year=args.year
            ).order_by('-timestamp')
        )
        return {"success": True, "journal_entries": [
            {"uuid": str(e.uuid), "description": e.description, "timestamp": e.timestamp} for e in entries
        ]}
    except Exception as e:
        return {"success": False, "error": str(e)}

class ListJournalEntriesByMonthInput(BaseModel):
    # user_uuid removed
    ledger_uuid: str
    year: int
    month: int

@function_tool(strict_mode=False)
async def list_journal_entries_by_month(wrapper: RunContextWrapper[UnifiedContext], args: ListJournalEntriesByMonthInput) -> dict:
    """
    List all journal entries for a ledger in a specific month.
    """
    try:
        user = await wrapper.context.get_user()
        
        entries = await sync_to_async(list)(
            JournalEntryModel.objects.filter(
                ledger__uuid=args.ledger_uuid,
                timestamp__year=args.year,
                timestamp__month=args.month
            ).order_by('-timestamp')
        )
        return {"success": True, "journal_entries": [
            {"uuid": str(e.uuid), "description": e.description, "timestamp": e.timestamp} for e in entries
        ]}
    except Exception as e:
        return {"success": False, "error": str(e)}

class TransactionInput(BaseModel):
    uuid: Optional[str]
    account_uuid: str
    amount: float
    description: Optional[str] = None

class BulkUpdateJournalEntryTransactionsInput(BaseModel):
    # user_uuid removed
    journal_entry_uuid: str
    transactions: List[TransactionInput]

@function_tool(strict_mode=False)
async def update_journal_entry_transactions(wrapper: RunContextWrapper[UnifiedContext], args: BulkUpdateJournalEntryTransactionsInput) -> dict:
    """
    Update the transactions for a journal entry (debits and credits).
    """
    try:
        user = await wrapper.context.get_user()
        
        je = await sync_to_async(JournalEntryModel.objects.get)(uuid=args.journal_entry_uuid)
        existing_txs = {str(tx.uuid): tx for tx in await sync_to_async(list)(je.transactionmodel_set.all())}
        updated_uuids = set()
        for tx_input in args.transactions:
            if tx_input.uuid and str(tx_input.uuid) in existing_txs:
                tx = existing_txs[str(tx_input.uuid)]
                tx.account_id = tx_input.account_uuid
                tx.amount = tx_input.amount
                tx.description = tx_input.description or ""
                await sync_to_async(tx.save)()
                updated_uuids.add(str(tx_input.uuid))
            else:
                await sync_to_async(TransactionModel.objects.create)(
                    journal_entry=je,
                    account_id=tx_input.account_uuid,
                    amount=tx_input.amount,
                    description=tx_input.description or "",
                )
        total = sum(
            tx.amount for tx in await sync_to_async(list)(je.transactionmodel_set.all())
        )
        if abs(total) > 1e-6:
            return {"success": False, "error": "Journal entry is not balanced (debits do not equal credits)."}
        return {"success": True, "message": "Transactions updated successfully."}
    except Exception as e:
        return {"success": False, "error": str(e)}

# =========================
# BUSINESS ENTITY
# =========================

class RegisterBusinessEntityInput(BaseModel):
    # user_uuid removed
    name: str
    fy_start_month: int
    accrual_method: bool

@function_tool(strict_mode=False)
async def register_business_entity(wrapper: RunContextWrapper[UnifiedContext], args: RegisterBusinessEntityInput) -> dict:
    """
    Register a new business entity for bookkeeping.
    """
    try:
        user = await wrapper.context.get_user()
        
        entity = await sync_to_async(EntityModel.objects.create)(
            name=args.name,
            fy_start_month=args.fy_start_month,
            accrual_method=args.accrual_method,
            admin=user
        )
        return {"success": True, "entity_id": str(entity.uuid), "name": entity.name}
    except Exception as e:
        return {"success": False, "error": str(e)}

class UpdateBusinessEntityInput(BaseModel):
    # user_uuid removed
    uuid: str 
    name: Optional[str] = None
    fy_start_month: Optional[int] = None
    accrual_method: Optional[bool] = None

@function_tool(strict_mode=False)
async def update_business_entity(wrapper: RunContextWrapper[UnifiedContext], args: UpdateBusinessEntityInput) -> dict:
    """
    Update the details of a business entity.
    """
    try:
        user = await wrapper.context.get_user()
        
        entity = await get_entity_for_uuid(args.uuid)
        for field in ['name', 'fy_start_month', 'accrual_method']:
            value = getattr(args, field)
            if value is not None:
                setattr(entity, field, value)
        await sync_to_async(entity.save)()
        return {"success": True, "entity_id": str(entity.uuid), "name": entity.name}
    except Exception as e:
        return {"success": False, "error": str(e)}

class DeleteBusinessEntityInput(BaseModel):
    # user_uuid removed
    uuid: str

@function_tool(strict_mode=False)
async def delete_business_entity(wrapper: RunContextWrapper[UnifiedContext], args: DeleteBusinessEntityInput) -> dict:
    """
    Delete a business entity from the system.
    """
    try:
        user = await wrapper.context.get_user()
        
        # Determine if we delete active entity or specific uuid.
        # If input has uuid, use it.
        entity = await get_entity_for_uuid(args.uuid)
        await sync_to_async(entity.delete)()
        return {"success": True, "message": "Entity deleted."}
    except Exception as e:
        return {"success": False, "error": str(e)}

class ChangeBusinessEntityStateInput(BaseModel):
    # user_uuid removed
    uuid: str
    action: str

@function_tool(strict_mode=False)
async def change_business_entity_state(wrapper: RunContextWrapper[UnifiedContext], args: ChangeBusinessEntityStateInput) -> dict:
    """
    Change the state of a business entity (activate, deactivate).
    """
    try:
        user = await wrapper.context.get_user()
        
        entity = await get_entity_for_uuid(args.uuid)
        action = args.action.lower()
        if action == "activate":
            entity.hidden = False
        elif action == "deactivate":
            entity.hidden = True
        else:
            return {"success": False, "error": f"Invalid action: {args.action}"}
        await sync_to_async(entity.save)()
        return {"success": True, "message": f"Entity state changed to {action}."}
    except Exception as e:
        return {"success": False, "error": str(e)}

@function_tool(strict_mode=False)
async def list_business_entities(wrapper: RunContextWrapper[UnifiedContext]) -> dict:
    """
    List all business entities in the system.
    """
    try:
        user = await wrapper.context.get_user()
        
        entities = await sync_to_async(list)(EntityModel.objects.for_user(user))
        return {
            "success": True,
            "entities": [
                {"uuid": str(e.uuid), "name": e.name}
                for e in entities
            ]
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

class GetBusinessEntityInput(BaseModel):
    # user_uuid removed
    uuid: str

@function_tool(strict_mode=False)
async def get_business_entity(wrapper: RunContextWrapper[UnifiedContext], args: GetBusinessEntityInput) -> dict:
    """
    Get details for a single business entity by UUID.
    """
    try:
        user = await wrapper.context.get_user()
        
        entity = await get_entity_for_uuid(args.uuid)
        return {
            "success": True,
            "entity": {
                "uuid": str(entity.uuid),
                "name": entity.name,
                "fy_start_month": entity.fy_start_month,
                "accrual_method": entity.accrual_method,
            }
        }
    except Exception as e:
        return {"success": False, "error": str(e)}

class GetBusinessEntityDashboardInput(BaseModel):
    # user_uuid removed
    uuid: str
    date: Optional[str] = None
    from_date: Optional[str] = None
    to_date: Optional[str] = None

@function_tool(strict_mode=False)
async def get_business_entity_dashboard(wrapper: RunContextWrapper[UnifiedContext], args: GetBusinessEntityDashboardInput) -> dict:
    """
    Get dashboard summary data for a business entity (optionally for a date or date range).
    """
    try:
        user = await wrapper.context.get_user()
        
        entity = await get_entity_for_uuid(args.uuid)
        dashboard = {
            "uuid": str(entity.uuid),
            "name": entity.name,
            "dashboard_date": args.date,
            "dashboard_from_date": args.from_date,
            "dashboard_to_date": args.to_date,
        }
        return {"success": True, "dashboard": dashboard}
    except Exception as e:
        return {"success": False, "error": str(e)}

@function_tool(strict_mode=False)
async def verify_entity_context(wrapper: RunContextWrapper[UnifiedContext]) -> dict:
    """
    Verifies if the business entity context is currently set for the session.
    
    Use this tool before handing off to a specialist agent to confirm that `set_active_business_entity` 
    was successful and the context is ready.
    """
    try:
        # Check if entity_uuid is set in the context
        if wrapper.context and wrapper.context.entity_uuid:
            return {
                "success": True,
                "is_set": True,
                "entity_name": wrapper.context.entity_name,
                "entity_uuid": wrapper.context.entity_uuid,
                "message": f"Context is set to entity: {wrapper.context.entity_name}"
            }
        else:
            return {
                "success": True, 
                "is_set": False, 
                "message": "No business entity is currently active in the context."
            }
    except Exception as e:
        return {"success": False, "error": str(e)}



        
import json
import logging
from pathlib import Path
from typing import Any, List, Optional
from openai import AsyncOpenAI

from domain.bank import BankItem
from domain.books import BookItem
from domain.patch import ProposedState
from agent.prompt import SYSTEM_PROMPT
from agent.optimizer_tool import OPTIMIZER_TOOL_SCHEMA, execute_optimizer_tool
from agent.candidate_generation import generate_plausible_candidates

logger = logging.getLogger(__name__)

_openai_client: Optional[AsyncOpenAI] = None


def _read_openai_api_key() -> str:
    """Read OPENAI_API_KEY from the worker's container/.env file."""
    # The repository-level container/.env is four levels above this module:
    # reconciliation_eval/agent -> app -> python-worker -> repository root.
    env_path = Path(__file__).resolve().parents[4] / "container" / ".env"
    if not env_path.is_file():
        raise RuntimeError(f"OpenAI environment file not found: {env_path}")

    for line in env_path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        if name.strip() == "OPENAI_API_KEY":
            return value.strip().strip("'\"")

    raise RuntimeError(f"OPENAI_API_KEY is missing from {env_path}")


def get_openai_client() -> AsyncOpenAI:
    global _openai_client
    if _openai_client is None:
        api_key = _read_openai_api_key()
        _openai_client = AsyncOpenAI(api_key=api_key)
    return _openai_client

async def run_reconciliation_agent(
    problem_id: str,
    currency: str,
    bank_items: List[BankItem],
    book_items: List[BookItem],
    scenario_evidence: str,
    model_name: str = "gpt-5.4-mini",
    max_optimizer_calls: int = 5
) -> ProposedState:
    """
    Executes the LLM reasoning loop (Section 40).
    """
    client = get_openai_client()
    
    # Generate the deterministic candidates
    plausible_candidates = generate_plausible_candidates(bank_items, book_items)

    # Construct the initial scenario context
    context = (
        f"SCENARIO: {problem_id}\n"
        f"CURRENCY: {currency}\n\n"
        f"BANK ITEMS:\n{[b.model_dump_json() for b in bank_items]}\n\n"
        f"BOOK ITEMS:\n{[j.model_dump_json() for j in book_items]}\n\n"
        f"EVIDENCE:\n{scenario_evidence}\n\n"
        f"--- PRE-CALCULATED PLAUSIBLE CANDIDATES ---\n"
        f"The following mathematically valid combinations were found. "
        f"Use the evidence to SCORE them (assigning utility) and generate hypotheses for them. "
        f"If a bank item appears in multiple overlapping candidates, generate a hypothesis for EACH one.\n"
        f"{plausible_candidates}"
    )

    input_items: List[dict[str, Any]] = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": context}
    ]

    # Responses API function tools are flat, unlike Chat Completions tools.
    tool_definition = OPTIMIZER_TOOL_SCHEMA["function"]
    tools: List[dict[str, Any]] = (
        [{
            "type": "function",
            "name": tool_definition["name"],
            "description": tool_definition["description"],
            "parameters": tool_definition["parameters"],
            "strict": False,
        }]
        if max_optimizer_calls > 0
        else []
    )
    calls_made = 0

    async def request(input_value: List[dict[str, Any]], structured: bool = False) -> Any:
        request_args: dict[str, Any] = {
            "model": model_name,
            "input": input_value,
        }
        if tools:
            request_args["tools"] = tools
        if structured:
            request_args["text"] = {
                "format": {
                    "type": "json_schema",
                    "name": "proposed_state",
                    "schema": ProposedState.model_json_schema(),
                    # ProposedState contains optional/defaulted fields, so its
                    # Pydantic schema is not valid under strict Structured
                    # Outputs requirements (which require every property).
                    # We still validate the returned JSON with Pydantic below.
                    "strict": False,
                }
            }
        return await client.responses.create(**request_args)

    while calls_made <= max_optimizer_calls:
        logger.info(f"Agent reasoning loop iteration {calls_made}")
        response = await request(input_items)
        output_items = [item.model_dump(exclude_unset=True) for item in response.output]
        input_items.extend(output_items)

        tool_calls = [item for item in response.output if getattr(item, "type", None) == "function_call"]
        if tool_calls:
            calls_made += 1
            for tool_call in tool_calls:
                try:
                    tool_args = json.loads(tool_call.arguments)
                    logger.info(f"Executing Optimizer Tool... (Call {calls_made})")
                    tool_result_json = execute_optimizer_tool(
                        tool_args=tool_args,
                        problem_id=problem_id,
                        currency=currency,
                        original_bank_items=bank_items,
                        original_book_items=book_items,
                    )
                except Exception as e:
                    logger.error(f"Error parsing tool args: {e}")
                    tool_result_json = json.dumps({
                        "status": "ERROR",
                        "diagnostics": ["Invalid JSON format in tool arguments."],
                    })
                input_items.append({
                    "type": "function_call_output",
                    "call_id": tool_call.call_id,
                    "output": tool_result_json,
                })
            continue

        logger.info("LLM finished reasoning. Requesting final ProposedState.")
        input_items.append({
            "role": "user",
            "content": "You have finished reasoning. Please output the exact final ProposedState JSON.",
        })
        final_response = await request(input_items, structured=True)
        return ProposedState.model_validate_json(final_response.output_text)
        
    # If we hit the max tool calls limit (Section 40)
    logger.warning("Max optimizer calls exceeded. Forcing termination.")
    input_items.append({
        "role": "user", 
        "content": "You have exceeded the maximum number of optimizer calls. Leave remaining ambiguous items unresolved and output the ProposedState JSON now."
    })

    final_response = await request(input_items, structured=True)
    return ProposedState.model_validate_json(final_response.output_text)

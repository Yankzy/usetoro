"""
Natural-language Bookkeeping Operator Agent.

Operates the BookkeepingWorkbench via typed tools.
Maintains conversational memory, bounds execution loops (max 8 rounds),
and interacts through OpenAI Responses API.
"""

from __future__ import annotations

import json
import logging
from typing import Any, Callable

from openai import AsyncOpenAI

from bookkeeping_state.llm.client import (
    get_openai_client,
    run_coro_sync,
)
from .prompts import OPERATOR_SYSTEM_PROMPT
from .tools import (
    OPERATOR_TOOLS,
    execute_operator_tool,
)
from .workbench import BookkeepingWorkbench

logger = logging.getLogger(__name__)

MAX_TOOL_ROUNDS = 8


def get_operator_model_name(default: str = "gpt-5.6-luna") -> str:
    """
    Resolve model name for the Bookkeeping Operator.
    Follows precedence: BOOKKEEPING_OPERATOR_MODEL -> BOOKKEEPING_EVAL_MODEL -> OPENAI_MODEL -> default.
    """
    import os

    return (
        os.environ.get("BOOKKEEPING_OPERATOR_MODEL")
        or os.environ.get("BOOKKEEPING_EVAL_MODEL")
        or os.environ.get("OPENAI_MODEL")
        or default
    )


class BookkeepingOperator:
    """
    Autonomous conversational agent operating a detached BookkeepingWorkbench.
    """

    def __init__(
        self,
        workbench: BookkeepingWorkbench,
        model_name: str | None = None,
        client: AsyncOpenAI | None = None,
        instructions: str | None = None,
        max_rounds: int = MAX_TOOL_ROUNDS,
        trace_callback: Callable[[str, dict[str, Any]], None] | None = None,
    ) -> None:
        self.workbench = workbench
        self.model_name = model_name or get_operator_model_name()
        self._client = client
        self.instructions = instructions or OPERATOR_SYSTEM_PROMPT
        self.max_rounds = max_rounds
        self.trace_callback = trace_callback
        self.trace_enabled = False
        self.history: list[dict[str, Any]] = []

    @property
    def client(self) -> AsyncOpenAI:
        if self._client is None:
            self._client = get_openai_client()
        return self._client

    def clear_chat(self) -> None:
        """Reset conversational history."""
        self.history.clear()

    def _emit_trace(self, event_type: str, payload: dict[str, Any]) -> None:
        if self.trace_callback:
            self.trace_callback(event_type, payload)
        elif self.trace_enabled:
            print(f"[operator-trace:{event_type}] {json.dumps(payload, default=str)}")

    async def handle_message_async(self, user_text: str) -> str:
        """
        Process a user natural-language message asynchronously.
        Executes a bounded tool loop against the workbench.
        """
        user_text = user_text.strip()
        if not user_text:
            return "Please provide a request or question."

        # Maintain conversational memory across turns
        self.history.append({"role": "user", "content": user_text})
        conversation_items: list[dict[str, Any]] = list(self.history)

        round_idx = 0
        final_text = ""

        while round_idx < self.max_rounds:
            round_idx += 1
            self._emit_trace("reasoning_start", {"round": round_idx, "model": self.model_name})

            request_args: dict[str, Any] = {
                "model": self.model_name,
                "instructions": self.instructions,
                "tools": OPERATOR_TOOLS,
                "input": conversation_items,
            }

            try:
                response = await self.client.responses.create(**request_args)
            except Exception as exc:
                error_msg = f"Operator LLM API error: {exc}"
                logger.exception(error_msg)
                self._emit_trace("api_error", {"error": str(exc), "round": round_idx})
                return f"Error communicating with operator model: {exc}"

            # Append model's outputs to conversation_items
            for item in response.output:
                if hasattr(item, "model_dump"):
                    conversation_items.append(item.model_dump(exclude_unset=True))
                elif isinstance(item, dict):
                    conversation_items.append(item)

            tool_calls = [
                item
                for item in response.output
                if getattr(item, "type", None) == "function_call"
                or (isinstance(item, dict) and item.get("type") == "function_call")
            ]

            if not tool_calls:
                # No more tools called; final response reached
                text = getattr(response, "output_text", None)
                if not text:
                    # Fallback inspection of output items
                    text_parts = []
                    for item in response.output:
                        if getattr(item, "type", None) == "message":
                            content = getattr(item, "content", None)
                            if isinstance(content, list):
                                for part in content:
                                    if getattr(part, "type", None) == "text":
                                        text_parts.append(getattr(part, "text", ""))
                            elif isinstance(content, str):
                                text_parts.append(content)
                    text = "\n".join(text_parts).strip()

                final_text = text or "I have processed your request."
                break

            # Execute tool calls
            for tool_call in tool_calls:
                call_name = getattr(tool_call, "name", None) or (
                    tool_call.get("name") if isinstance(tool_call, dict) else None
                )
                call_args = getattr(tool_call, "arguments", None) or (
                    tool_call.get("arguments") if isinstance(tool_call, dict) else None
                )
                call_id = (
                    getattr(tool_call, "call_id", None)
                    or getattr(tool_call, "id", None)
                    or (tool_call.get("call_id") if isinstance(tool_call, dict) else None)
                    or (tool_call.get("id") if isinstance(tool_call, dict) else None)
                    or f"call_{round_idx}"
                )

                self._emit_trace("tool_call", {"tool": call_name, "arguments": call_args, "call_id": call_id})
                result = execute_operator_tool(self.workbench, call_name or "", call_args or {})
                self._emit_trace("tool_result", {"tool": call_name, "status": result.get("status"), "call_id": call_id})

                result_json = json.dumps(result, default=str)
                conversation_items.append({
                    "type": "function_call_output",
                    "call_id": call_id,
                    "output": result_json,
                })

        if not final_text and round_idx >= self.max_rounds:
            final_text = (
                f"Operator paused: reached maximum tool reasoning limit ({self.max_rounds} rounds). "
                "Current operations may be partially complete; check status with 'state' or 'unresolved'."
            )

        # Store assistant final reply in persistent conversational history
        self.history.append({"role": "assistant", "content": final_text})
        return final_text

    def handle_message(self, user_text: str) -> str:
        """
        Synchronous entrypoint for handling a user message.
        """
        return run_coro_sync(self.handle_message_async(user_text))

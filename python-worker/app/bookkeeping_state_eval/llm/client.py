from __future__ import annotations

import asyncio
import concurrent.futures
import os
from collections.abc import Coroutine
from pathlib import Path
from typing import Any, TypeVar

from openai import AsyncOpenAI

T = TypeVar("T")

_openai_client: AsyncOpenAI | None = None


def _read_openai_api_key() -> str:
    """
    Read OPENAI_API_KEY from environment or the repository container/.env file.
    Follows reconciliation_eval donor implementation.
    """
    env_key = os.environ.get("OPENAI_API_KEY")
    if env_key and env_key.strip():
        return env_key.strip().strip("'\"")

    # Upward search for container/.env from current file
    current = Path(__file__).resolve()
    for parent in [current] + list(current.parents):
        env_path = parent / "container" / ".env"
        if env_path.is_file():
            for line in env_path.read_text().splitlines():
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                name, value = line.split("=", 1)
                if name.strip() == "OPENAI_API_KEY":
                    key = value.strip().strip("'\"")
                    if key:
                        return key

    raise RuntimeError("OPENAI_API_KEY not found in environment or container/.env")


def get_openai_client(api_key: str | None = None) -> AsyncOpenAI:
    """
    Return a shared or configured AsyncOpenAI client.
    """
    global _openai_client
    if api_key:
        return AsyncOpenAI(api_key=api_key)

    if _openai_client is None:
        resolved_key = _read_openai_api_key()
        _openai_client = AsyncOpenAI(api_key=resolved_key)
    return _openai_client


def get_model_name(default: str = "gpt-5.6-sol") -> str:
    """
    Resolve model name from environment or fallback to default.
    Consistent with reconciliation_eval conventions.
    """
    return (
        os.environ.get("BOOKKEEPING_EVAL_MODEL")
        or os.environ.get("OPENAI_MODEL")
        or default
    )


def get_semantic_provider_mode(default: str = "deterministic") -> str:
    """
    Resolve semantic provider selection from environment.
    Canonical environment variable: BOOKKEEPING_SEMANTIC_PROVIDER.
    """
    mode = os.environ.get("BOOKKEEPING_SEMANTIC_PROVIDER", default).lower().strip()
    if mode in ("llm", "real", "openai"):
        return "llm"
    return "deterministic"


def run_coro_sync(coro: Coroutine[Any, Any, T]) -> T:
    """
    Safely execute an async coroutine from synchronous code.
    Correctly handles environments where an event loop is already running (e.g. pytest-asyncio)
    by running in a dedicated worker thread, avoiding nested asyncio.run() crashes.
    """
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        loop = None

    if loop is not None and loop.is_running():
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
            future = executor.submit(lambda: asyncio.run(coro))
            return future.result()
    else:
        return asyncio.run(coro)

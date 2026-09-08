from __future__ import annotations

from openai import AsyncOpenAI

from bookkeeping_state_eval.llm.client import (
    get_model_name,
    get_openai_client,
    get_semantic_provider_mode,
)
from bookkeeping_state_eval.reconciliation.candidate_generation import (
    DefaultReconciliationScorer,
    ReconciliationSemanticScoreProvider,
)
from bookkeeping_state_eval.routing.scorer import (
    RoutingSemanticScoreProvider,
    ZeroRoutingSemanticScoreProvider,
)


def create_routing_semantic_provider(
    mode: str | None = None,
    *,
    model_name: str | None = None,
    client: AsyncOpenAI | None = None,
) -> RoutingSemanticScoreProvider:
    """
    Factory creating routing semantic score provider.
    Defaults to deterministic ZeroRoutingSemanticScoreProvider unless mode is "llm".
    """
    resolved_mode = mode if mode is not None else get_semantic_provider_mode("deterministic")
    if resolved_mode == "llm":
        from bookkeeping_state_eval.routing.llm_scorer import (
            LlmRoutingSemanticScoreProvider,
        )
        return LlmRoutingSemanticScoreProvider(
            llm_client=client,
            model_name=model_name or get_model_name("gpt-5.6-luna"),
        )
    return ZeroRoutingSemanticScoreProvider()


def create_reconciliation_semantic_provider(
    mode: str | None = None,
    *,
    model_name: str | None = None,
    client: AsyncOpenAI | None = None,
) -> ReconciliationSemanticScoreProvider:
    """
    Factory creating reconciliation semantic score provider.
    Defaults to deterministic DefaultReconciliationScorer unless mode is "llm".
    """
    resolved_mode = mode if mode is not None else get_semantic_provider_mode("deterministic")
    if resolved_mode == "llm":
        from bookkeeping_state_eval.reconciliation.llm_scorer import (
            LlmReconciliationSemanticScoreProvider,
        )
        return LlmReconciliationSemanticScoreProvider(
            llm_client=client,
            model_name=model_name or get_model_name("gpt-5.6-luna"),
        )
    return DefaultReconciliationScorer()

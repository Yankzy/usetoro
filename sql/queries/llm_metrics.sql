-- name: GetLLMPricingModel :one
SELECT * FROM toro_core.llm_pricing_models
WHERE model = $1 LIMIT 1;

-- name: InsertLLMTurnMetric :one
INSERT INTO toro_core.llm_turn_metrics (
    tenant_id, conversation_id, dag_id, node_id, agent_id,
    model, provider, input_tokens, output_tokens, total_tokens, cost_usd,
    entropy, confidence, state_transition, hold_reason,
    campaign_id, prospect_id, step_number, metadata,
    payer_tenant_id
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18, $19,
    $20
) RETURNING *;

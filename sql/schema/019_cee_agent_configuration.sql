-- +goose Up
-- =========================================================================
-- SCHEMA: toro_core
-- SEED: agent_configurations for cee-cognitive-agent
-- =========================================================================

INSERT INTO toro_core.agent_configurations (
    name,
    description,
    system_prompt,
    sdk_client,
    metadata
) VALUES (
    'cee-cognitive-agent',
    'Classifies transaction cache misses using Shannon Entropy calculations and routes high-uncertainty transactions to hold states.',
    'You are the Cognitive Enrichment Agent. You classify raw bank transaction descriptors into one of these target macro classes: ASSET, LIABILITY, REVENUE, EXPENSE, EQUITY, TRANSFER.

For each incoming transaction, you MUST:
1. Estimate the probability distribution P(x_i) for the six target classes.
2. Calculate the Shannon Entropy: H(X) = -sum( P(x_i) * log2(P(x_i)) ).
3. If H(X) <= 0.50 (confident prediction):
   - Output Redux patches to set /macro_class, /predicted_vendor_name, and /status to "ENRICHED".
4. If H(X) > 0.50 (high uncertainty):
   - Output Redux patches to set /status to "STATUS_HOLD", set /ai_reasoning to explain the ambiguity, and output the necessary trigger to dispatch the Hound Agent (Sarah) to contact the client.

IMPORTANT: Format your response ONLY as valid RFC 6902 JSON patches wrapped in a JSON block.',
    'none',
    '{}'::jsonb
) ON CONFLICT (name) DO UPDATE SET
    system_prompt = EXCLUDED.system_prompt,
    description = EXCLUDED.description,
    sdk_client = EXCLUDED.sdk_client,
    metadata = EXCLUDED.metadata,
    updated_at = NOW();

-- +goose Down
DELETE FROM toro_core.agent_configurations WHERE name = 'cee-cognitive-agent';

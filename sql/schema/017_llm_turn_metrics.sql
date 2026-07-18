-- +goose Up

CREATE TABLE IF NOT EXISTS toro_core.llm_pricing_models (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model VARCHAR(255) UNIQUE NOT NULL,
    provider VARCHAR(255) NOT NULL,
    input_cost_per_1m NUMERIC(10, 6) NOT NULL DEFAULT 0,
    output_cost_per_1m NUMERIC(10, 6) NOT NULL DEFAULT 0,
    cache_cost_per_1m NUMERIC(10, 6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS toro_core.llm_turn_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID,
    conversation_id UUID,
    dag_id VARCHAR(255),
    node_id VARCHAR(255),
    agent_id VARCHAR(255),
    model VARCHAR(255) NOT NULL REFERENCES toro_core.llm_pricing_models(model) ON UPDATE CASCADE ON DELETE RESTRICT,
    provider VARCHAR(255),
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd NUMERIC(10, 6) NOT NULL DEFAULT 0,
    
    entropy DOUBLE PRECISION,
    confidence DOUBLE PRECISION,
    state_transition VARCHAR(100),
    hold_reason TEXT,
    
    campaign_id UUID,
    prospect_id UUID,
    step_number INTEGER,
    
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_llm_metrics_tenant ON toro_core.llm_turn_metrics(tenant_id);
CREATE INDEX idx_llm_metrics_conversation ON toro_core.llm_turn_metrics(conversation_id);
CREATE INDEX idx_llm_metrics_dag ON toro_core.llm_turn_metrics(dag_id, node_id);
CREATE INDEX idx_llm_metrics_model ON toro_core.llm_turn_metrics(model);

-- Seed some default models
INSERT INTO toro_core.llm_pricing_models (model, provider, input_cost_per_1m, output_cost_per_1m, cache_cost_per_1m) VALUES
-- OpenAI GPT-5.6 family
('gpt-5.6-sol', 'openai', 5.000000, 30.000000, 0.500000),
('gpt-5.6-terra', 'openai', 2.500000, 15.000000, 0.250000),
('gpt-5.6-luna', 'openai', 1.000000, 6.000000, 0.100000),
-- OpenAI GPT-5.5 family
('gpt-5.5', 'openai', 5.000000, 30.000000, 0.500000),
('gpt-5.5-pro', 'openai', 30.000000, 180.000000, 0.000000),
-- OpenAI GPT-5.4 family
('gpt-5.4', 'openai', 2.500000, 15.000000, 0.250000),
('gpt-5.4-mini', 'openai', 0.750000, 4.500000, 0.075000),
('gpt-5.4-nano', 'openai', 0.200000, 1.250000, 0.020000),
('gpt-5.4-pro', 'openai', 30.000000, 180.000000, 0.000000),
-- OpenAI GPT-5.2 family
('gpt-5.2', 'openai', 1.750000, 14.000000, 0.175000),
('gpt-5.2-pro', 'openai', 21.000000, 168.000000, 0.000000),
-- OpenAI GPT-5.1 & GPT-5 family
('gpt-5.1', 'openai', 1.250000, 10.000000, 0.125000),
('gpt-5', 'openai', 1.250000, 10.000000, 0.125000),
('gpt-5-mini', 'openai', 0.250000, 2.000000, 0.025000),
('gpt-5-nano', 'openai', 0.050000, 0.400000, 0.005000),
('gpt-5-pro', 'openai', 15.000000, 120.000000, 0.000000),
-- OpenAI GPT-4.1 & GPT-4o family
('gpt-4.1', 'openai', 2.000000, 8.000000, 0.500000),
('gpt-4.1-mini', 'openai', 0.400000, 1.600000, 0.100000),
('gpt-4.1-nano', 'openai', 0.100000, 0.400000, 0.025000),
('gpt-4o', 'openai', 2.500000, 10.000000, 1.250000),
('gpt-4o-mini', 'openai', 0.150000, 0.600000, 0.075000),
-- Anthropic Claude family
('claude-fable-5', 'anthropic', 10.000000, 50.000000, 1.000000),
('claude-mythos-5', 'anthropic', 10.000000, 50.000000, 1.000000),
('claude-opus-4.8', 'anthropic', 5.000000, 25.000000, 0.500000),
('claude-opus-4.7', 'anthropic', 5.000000, 25.000000, 0.500000),
('claude-opus-4.6', 'anthropic', 5.000000, 25.000000, 0.500000),
('claude-opus-4.5', 'anthropic', 5.000000, 25.000000, 0.500000),
-- Google Gemini family
('gemini-3.1-pro', 'google', 3.500000, 10.500000, 1.750000),
('gemini-3.1-flash', 'google', 0.075000, 0.300000, 0.037500),
-- Deepseek
('deepseek-v4-pro', 'deepseek', 0.140000, 0.440000, 0.002800),
('deepseek-v4-flash', 'deepseek', 0.280000, 0.870000, 0.037000),
-- Moonshot
('kimi-k3', 'moonshot', 3.000000, 15.000000, 0.300000)
ON CONFLICT (model) DO UPDATE SET 
    input_cost_per_1m = EXCLUDED.input_cost_per_1m,
    output_cost_per_1m = EXCLUDED.output_cost_per_1m,
    cache_cost_per_1m = EXCLUDED.cache_cost_per_1m;

-- +goose Down
DROP TABLE IF EXISTS toro_core.llm_turn_metrics;
DROP TABLE IF EXISTS toro_core.llm_pricing_models;

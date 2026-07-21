-- +goose Up
INSERT INTO toro_core.llm_pricing_models (model, provider, input_cost_per_1m, output_cost_per_1m, cache_cost_per_1m) VALUES
('gemini-3.5-pro', 'google', 3.500000, 10.500000, 1.750000),
('gemini-3.5-flash', 'google', 0.075000, 0.300000, 0.037500)
ON CONFLICT (model) DO UPDATE SET 
    input_cost_per_1m = EXCLUDED.input_cost_per_1m,
    output_cost_per_1m = EXCLUDED.output_cost_per_1m,
    cache_cost_per_1m = EXCLUDED.cache_cost_per_1m;

-- +goose Down
DELETE FROM toro_core.llm_pricing_models WHERE model IN ('gemini-3.5-pro', 'gemini-3.5-flash');

package redux

// RBACPolicy explicitly maps immutable Actor identifiers (e.g. "AI_AGENT", "CPA_ADMIN") 
// to their statically authorized JSON topological subsets.
type RBACPolicy struct {
	AllowedPrefixes map[string][]string
}

// EngineConfig holds operational tuning properties governing throughput and structural integrity 
// bounds during deterministic Redux compilations. It allows custom metrics bindings and limits on JSON Ops.
type EngineConfig struct {
	SchemaString    string          // Raw W3C JSON Schema payload to precompile against operations
	MaxOperations   int             // Restricts patch arrays limiting sequence-size exhaustion attacks
	MaxPayloadBytes int             // Byte Limit capping OOM memory crashes from enormous LLM patches 
	Metrics         MetricsRecorder // Binds native Prometheus or OpenTelemetry collection objects natively
	RBAC            RBACPolicy      // Injected Actor-authorization boundaries guaranteeing operational paths
}

// DefaultConfig provides optimal safe bounds scaling up natively out-of-the-box
// restricting arrays to 10 logical operations and 64KB per pulse minimizing load on matrix handlers.
func DefaultConfig() EngineConfig {
	return EngineConfig{
		MaxOperations:   10,
		MaxPayloadBytes: 64 * 1024, // 64 Kilobytes default safe bounding
		Metrics:         noopMetrics{},
	}
}

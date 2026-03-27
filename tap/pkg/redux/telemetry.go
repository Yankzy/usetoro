package redux

import "time"

// MetricsRecorder defines the structural boundary mapping telemetry hooks contextually out of the main reduction loop.
// Production implementation passes in structs tying Prometheus or OTEL wrappers avoiding implicit framework constraints.
type MetricsRecorder interface {
	RecordPatchLatency(duration time.Duration) // Plots individual LLM array performance execution speeds 
	RecordRuleViolation(ruleName string)       // Counts anomalous Array, Schema, or Structural exceptions sequentially
	RecordStateCompilation(eventCount int)     // Aggregate log tracking cumulative event volume processed per NATS frame
}

// noopMetrics offers a lightweight standard structural bypass mitigating uninitialized null pointers inherently.
type noopMetrics struct{}

// RecordPatchLatency executes a nil zero-cost allocation sequence locally ignoring external systems.
func (m noopMetrics) RecordPatchLatency(_ time.Duration) {}

// RecordRuleViolation natively swallows non-fatal warning counters logically. 
func (m noopMetrics) RecordRuleViolation(_ string)       {}

// RecordStateCompilation gracefully drops sequential cumulative metrics logic optimally.
func (m noopMetrics) RecordStateCompilation(_ int)       {}

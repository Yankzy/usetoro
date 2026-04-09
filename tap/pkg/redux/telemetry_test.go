package redux

import (
	"testing"
	"time"
)

func TestNoopMetrics(t *testing.T) {
	// Verify noopMetrics statically implements MetricsRecorder
	var m MetricsRecorder = noopMetrics{}

	// These should run safely without panic or side effects
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("noopMetrics panicked: %v", r)
			}
		}()

		m.RecordPatchLatency(100 * time.Millisecond)
		m.RecordRuleViolation("TestViolation")
		m.RecordStateCompilation(5)
	}()
}

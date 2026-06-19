package workers

import (
	"testing"
)

func TestSubjectIsCovered(t *testing.T) {
	tests := []struct {
		name     string
		req      string
		existing string
		expected bool
	}{
		{
			name:     "exact match",
			req:      "worker.inbox.ase_bridge",
			existing: "worker.inbox.ase_bridge",
			expected: true,
		},
		{
			name:     "wildcard level 1 match",
			req:      "worker.inbox.ase_bridge",
			existing: "worker.inbox.*",
			expected: true,
		},
		{
			name:     "wildcard level 2 match",
			req:      "worker.inbox.ase_bridge",
			existing: "worker.inbox.>",
			expected: true,
		},
		{
			name:     "wildcard level 2 match nested",
			req:      "worker.inbox.ase_bridge.child",
			existing: "worker.inbox.>",
			expected: true,
		},
		{
			name:     "mismatch",
			req:      "worker.inbox.ase_bridge",
			existing: "worker.other.>",
			expected: false,
		},
		{
			name:     "too short",
			req:      "worker.inbox",
			existing: "worker.inbox.*",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := subjectIsCovered(tc.req, tc.existing)
			if got != tc.expected {
				t.Errorf("subjectIsCovered(%q, %q) = %v, want %v", tc.req, tc.existing, got, tc.expected)
			}
		})
	}
}

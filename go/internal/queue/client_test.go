package queue

import (
	"testing"
)

func TestContainsAllSubjects(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		required []string
		expected bool
	}{
		{
			name:     "all required present identically",
			existing: []string{"a", "b", "c"},
			required: []string{"a", "b", "c"},
			expected: true,
		},
		{
			name:     "all required present but existing has more",
			existing: []string{"a", "b", "c", "d"},
			required: []string{"a", "c"},
			expected: true,
		},
		{
			name:     "some required missing",
			existing: []string{"a", "b"},
			required: []string{"a", "c"},
			expected: false,
		},
		{
			name:     "empty required",
			existing: []string{"a", "b"},
			required: []string{},
			expected: true,
		},
		{
			name:     "empty existing, required missing",
			existing: []string{},
			required: []string{"a"},
			expected: false,
		},
		{
			name:     "both empty",
			existing: []string{},
			required: []string{},
			expected: true,
		},
		{
			name:     "existing covers required with tail wildcard",
			existing: []string{"qbo.>"},
			required: []string{"qbo.events.*", "qbo.test"},
			expected: true,
		},
		{
			name:     "existing covers required with single wildcard",
			existing: []string{"qbo.*.created"},
			required: []string{"qbo.events.created"},
			expected: true,
		},
		{
			name:     "existing uses wildcard but does not cover tail",
			existing: []string{"qbo.*"},
			required: []string{"qbo.events.created"},
			expected: false, // Wait, qbo.* only matches ONE token. So qbo.events.created expands to two tokens, so false.
		},
		{
			name:     "existing narrower than required",
			existing: []string{"qbo.events.*"},
			required: []string{"qbo.>"},
			expected: false,
		},
		{
			name:     "required has wildcard, existing has literal (should be false since existing doesn't cover required)",
			existing: []string{"qbo.events.created"},
			required: []string{"qbo.events.*"},
			expected: false,
		},
		{
			name:     "required exactly matches wildcard form of existing",
			existing: []string{"qbo.events.*"},
			required: []string{"qbo.events.*"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsAllSubjects(tt.existing, tt.required)
			if result != tt.expected {
				t.Errorf("expected %v, got %v for existing=%v, required=%v", tt.expected, result, tt.existing, tt.required)
			}
		})
	}
}

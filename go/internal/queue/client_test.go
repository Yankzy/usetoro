package queue

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestSubjectsExactMatch(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		required []string
		expected bool
	}{
		{
			name:     "exact match identical order",
			existing: []string{"a", "b", "c"},
			required: []string{"a", "b", "c"},
			expected: true,
		},
		{
			name:     "exact match different order",
			existing: []string{"c", "a", "b"},
			required: []string{"a", "b", "c"},
			expected: true,
		},
		{
			name:     "existing has more",
			existing: []string{"a", "b", "c", "d"},
			required: []string{"a", "c"},
			expected: false,
		},
		{
			name:     "required has more",
			existing: []string{"a", "b"},
			required: []string{"a", "b", "c"},
			expected: false,
		},
		{
			name:     "some required missing",
			existing: []string{"a", "b"},
			required: []string{"a", "c"},
			expected: false,
		},
		{
			name:     "empty required non-empty existing",
			existing: []string{"a", "b"},
			required: []string{},
			expected: false,
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
			name:     "wildcards mismatch",
			existing: []string{"qbo.>"},
			required: []string{"qbo.events.*", "qbo.test"},
			expected: false,
		},
		{
			name:     "wildcards exact match",
			existing: []string{"qbo.events.*"},
			required: []string{"qbo.events.*"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subjectsExactMatch(tt.existing, tt.required)
			if result != tt.expected {
				t.Errorf("expected %v, got %v for existing=%v, required=%v", tt.expected, result, tt.existing, tt.required)
			}
		})
	}
}

func TestNeedsStreamUpdate(t *testing.T) {
	defaultExisting := &nats.StreamConfig{
		Subjects:    []string{"a", "b"},
		MaxAge:      30 * 24 * time.Hour,
		AllowMsgTTL: false,
		AllowRollup: false,
		DenyDelete:  true,
		DenyPurge:   true,
		AllowDirect: true,
	}

	tests := []struct {
		name     string
		existing *nats.StreamConfig
		required *nats.StreamConfig
		expected bool
	}{
		{
			name:     "identical config",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: false,
		},
		{
			name:     "subjects differ",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b", "c"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "MaxAge differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      1 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "AllowMsgTTL differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: true,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "AllowRollup differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: true,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "DenyDelete differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  false,
				DenyPurge:   true,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "DenyPurge differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   false,
				AllowDirect: true,
			},
			expected: true,
		},
		{
			name:     "AllowDirect differs",
			existing: defaultExisting,
			required: &nats.StreamConfig{
				Subjects:    []string{"a", "b"},
				MaxAge:      30 * 24 * time.Hour,
				AllowMsgTTL: false,
				AllowRollup: false,
				DenyDelete:  true,
				DenyPurge:   true,
				AllowDirect: false,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := needsStreamUpdate(tt.existing, tt.required)
			if result != tt.expected {
				t.Errorf("expected %v, got %v for %s", tt.expected, result, tt.name)
			}
		})
	}
}


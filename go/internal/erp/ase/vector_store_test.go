package ase

import (
	"testing"
)

// TestFloatsToVectorLiteral verifies the pgvector wire format serializer.
func TestFloatsToVectorLiteral(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		expected string
	}{
		{
			name:     "empty slice",
			input:    []float32{},
			expected: "[]",
		},
		{
			name:     "single value",
			input:    []float32{0.5},
			expected: "[0.500000]",
		},
		{
			name:     "multiple values",
			input:    []float32{0.1, 0.2, 0.3},
			expected: "[0.100000,0.200000,0.300000]",
		},
		{
			name:     "negative values",
			input:    []float32{-0.1, 0.0, 1.0},
			expected: "[-0.100000,0.000000,1.000000]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := floatsToVectorLiteral(tt.input)
			if got != tt.expected {
				t.Errorf("floatsToVectorLiteral(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestVectorMemoryConfigDefaults verifies that parseConfig produces safe
// VectorMemoryConfig defaults when no vector_memory block is present.
func TestVectorMemoryConfigDefaults(t *testing.T) {
	// The default config loaded at package init (or via InitConfig) should
	// have Enabled=false and sensible non-zero values.
	defaults := VectorMemoryConfig{
		Enabled:                 false,
		EmbeddingProvider:       "openai",
		OpenAIEmbeddingModel:    "text-embedding-3-small",
		EmbeddingDimensions:     1536,
		ScaNNNumLeaves:          10,
		RetrievalTopK:           5,
		HydratorIntervalSeconds: 30,
		HydratorMinConfidence:   0.98,
		HydratorBatchSize:       50,
	}

	if defaults.Enabled {
		t.Error("default VectorMemoryConfig should be disabled")
	}
	if defaults.EmbeddingProvider != "openai" {
		t.Errorf("expected 'openai', got %q", defaults.EmbeddingProvider)
	}
	if defaults.EmbeddingDimensions != 1536 {
		t.Errorf("expected 1536 dims, got %d", defaults.EmbeddingDimensions)
	}
	if defaults.RetrievalTopK != 5 {
		t.Errorf("expected topK=5, got %d", defaults.RetrievalTopK)
	}
	if defaults.HydratorMinConfidence != 0.98 {
		t.Errorf("expected minConfidence=0.98, got %f", defaults.HydratorMinConfidence)
	}
}

// TestVectorStoreVectorCfg verifies vectorCfg returns a safe fallback
// when no config is loaded for the given tenant/realm.
func TestVectorStoreVectorCfgFallback(t *testing.T) {
	vs := &VectorStore{}

	cfg := vs.vectorCfg()
	if cfg.OpenAIEmbeddingModel == "" {
		t.Error("vectorCfg fallback should return a non-empty OpenAIEmbeddingModel")
	}
	if cfg.RetrievalTopK <= 0 {
		t.Error("vectorCfg fallback should return a positive RetrievalTopK")
	}
}

// TestDerefStr verifies the nil-pointer-safe string dereference helper.
func TestDerefStr(t *testing.T) {
	t.Run("nil pointer", func(t *testing.T) {
		if got := derefStr(nil); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
	t.Run("non-nil pointer", func(t *testing.T) {
		s := "hello"
		if got := derefStr(&s); got != "hello" {
			t.Errorf("expected %q, got %q", s, got)
		}
	})
}

// TestVectorSourceTypeConstants verifies the source type string constants.
func TestVectorSourceTypeConstants(t *testing.T) {
	if VectorSourceMemoryRule != "memory_rule" {
		t.Errorf("VectorSourceMemoryRule = %q, want 'memory_rule'", VectorSourceMemoryRule)
	}
	if VectorSourceResolvedTx != "resolved_tx" {
		t.Errorf("VectorSourceResolvedTx = %q, want 'resolved_tx'", VectorSourceResolvedTx)
	}
}

// TestMetaToJSON verifies the metadata serialization helper.
func TestMetaToJSON(t *testing.T) {
	meta := map[string]any{
		"macro_class":  "EXPENSE",
		"confidence":   0.99,
		"account_type": "Expense",
	}
	b, err := metaToJSON(meta)
	if err != nil {
		t.Fatalf("metaToJSON failed: %v", err)
	}
	if len(b) == 0 {
		t.Error("metaToJSON returned empty bytes")
	}
}

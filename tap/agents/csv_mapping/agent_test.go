package csvmapping

import (
	"strings"
	"testing"
)

func TestExtractJSONPatches(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantErr       bool
		expectedCount int
	}{
		{
			name:          "Pure JSON Array",
			input:         `[{"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"}]`,
			wantErr:       false,
			expectedCount: 1,
		},
		{
			name:          "JSON with markdown blocks",
			input:         "```json\n" + `[{"op": "add", "path": "/columns_mapped", "value": {"date_col_idx": 1}}, {"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"}]` + "\n```",
			wantErr:       false,
			expectedCount: 2,
		},
		{
			name:          "JSON with conversational text",
			input:         "Here is the mapping you requested:\n```json\n" + `[{"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"}]` + "\n```\nLet me know if you need anything else.",
			wantErr:       false,
			expectedCount: 1,
		},
		{
			name:          "Invalid JSON format",
			input:         `[{"op": "add", "path": "/status"`, // Missing closing bracket and value
			wantErr:       true,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches, err := ExtractJSONPatches(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(patches) != tt.expectedCount {
				t.Errorf("expected %d patches, got %d", tt.expectedCount, len(patches))
			}
		})
	}
}

func TestBuildUserPrompt(t *testing.T) {
	rows := [][]string{
		{"", "   ", ""},                          // Empty row (skips because cols < 2)
		{"Date", "Description", "Amount"},        // Valid row
		{"col1"},                                 // Single col row (skips)
		{"2026-01-01", "AMZN Mktp US", "-14.99"}, // Valid row
	}

	prompt := BuildUserPrompt(rows)

	if strings.Contains(prompt, "Row 0:") {
		t.Errorf("expected row 0 to be skipped")
	}
	if strings.Contains(prompt, "Row 2:") {
		t.Errorf("expected row 2 to be skipped")
	}
	if !strings.Contains(prompt, "Row 1: Date | Description | Amount") {
		t.Errorf("expected row 1 to be included")
	}
	if !strings.Contains(prompt, "Row 3: 2026-01-01 | AMZN Mktp US | -14.99") {
		t.Errorf("expected row 3 to be included")
	}

	if !strings.Contains(prompt, "POLARITY SIGN") {
		t.Errorf("expected POLARITY SIGN instruction in prompt")
	}
	if !strings.Contains(prompt, "polarity_sign") {
		t.Errorf("expected polarity_sign field in prompt")
	}
}



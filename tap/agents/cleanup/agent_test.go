package cleanup

import (
	"strings"
	"testing"
)

func TestExtractJSONToMapping(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		expectedCol int
	}{
		{
			name:        "Pure JSON",
			input:       `{"date_col_idx": 1, "description_col_idx": 2, "amount_col_idx": 3, "is_split_amount": false, "is_expense_positive": true, "confidence_score": 95.0, "reasoning": "Clear headers present"}`,
			wantErr:     false,
			expectedCol: 2,
		},
		{
			name:        "JSON with markdown blocks",
			input:       "```json\n" + `{"date_col_idx": 1, "description_col_idx": 2, "amount_col_idx": 3, "is_split_amount": false, "is_expense_positive": true, "confidence_score": 95.0, "reasoning": "Clear headers present"}` + "\n```",
			wantErr:     false,
			expectedCol: 2,
		},
		{
			name:        "JSON with conversational text",
			input:       "Here is the mapping you requested:\n```json\n" + `{"date_col_idx": 0, "description_col_idx": 1, "amount_col_idx": 2, "is_split_amount": false, "is_expense_positive": false, "confidence_score": 88.5, "reasoning": "Standard structure"}` + "\n```\nLet me know if you need anything else.",
			wantErr:     false,
			expectedCol: 1,
		},
		{
			name:        "Invalid JSON format",
			input:       `{"date_col_idx": 1, "description_col_idx": 2`,
			wantErr:     true,
			expectedCol: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping, err := extractJSONToMapping(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mapping.DescriptionColIdx != tt.expectedCol {
				t.Errorf("expected DescriptionColIdx %d, got %d", tt.expectedCol, mapping.DescriptionColIdx)
			}
		})
	}
}

func TestBuildUserPrompt(t *testing.T) {
	rows := [][]string{
		{"", "   ", ""},                           // Empty row (skips because cols < 2)
		{"Date", "Description", "Amount"},         // Valid row
		{"col1"},                                  // Single col row (skips)
		{"2026-01-01", "AMZN Mktp US", "-14.99"},  // Valid row
	}

	prompt := buildUserPrompt(rows)

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
	if !strings.Contains(prompt, "confidence_score") {
		t.Errorf("expected confidence_score instruction in prompt")
	}
}

package ses

import (
	"testing"
)

func TestCleanMessageID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard Message-ID with brackets",
			input:    "<12345.67890@mail.example.com>",
			expected: "12345.67890@mail.example.com",
		},
		{
			name:     "Message-ID without brackets",
			input:    "12345.67890@mail.example.com",
			expected: "12345.67890@mail.example.com",
		},
		{
			name:     "Empty String",
			input:    "",
			expected: "",
		},
		{
			name:     "Whitespace padded",
			input:    "  <12345.67890@mail.example.com>  ",
			expected: "12345.67890@mail.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanMessageID(tt.input)
			if result != tt.expected {
				t.Errorf("cleanMessageID(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

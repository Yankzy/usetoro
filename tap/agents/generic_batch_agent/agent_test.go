package genericbatch

import (
	"encoding/json"
	"testing"
)

func TestExtractJSONPatches(t *testing.T) {
	_ = &GenericBatchAgent{} // keep import for package coverage

	tests := []struct {
		name      string
		respText  string
		wantLen   int
		expectErr bool
	}{
		{
			name: "Standard JSON array",
			respText: `Here is the response:
[
  {"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"}
]
Have a nice day.`,
			wantLen:   1,
			expectErr: false,
		},
		{
			name: "Single JSON object fallback",
			respText: `Sure! Here is the patch:
{
  "op": "add",
  "path": "/rows",
  "value": {
    "0": { "id": "tx_123", "macro_class": "EXPENSE" }
  }
}`,
			wantLen:   1,
			expectErr: false,
		},
		{
			name:      "No valid JSON",
			respText:  `Hello, this is just plain text.`,
			wantLen:   0,
			expectErr: true,
		},
		{
			name: "Malformed array",
			respText: `[ {"op": "add" `,
			wantLen:   0,
			expectErr: true,
		},
		{
			name: "Malformed object",
			respText: `{ "op": "add" `,
			wantLen:   0,
			expectErr: true,
		},
		{
			name: "JSON inside ```json fence",
			respText: "```json\n{\n  \"op\": \"add\",\n  \"path\": \"/rows\",\n  \"value\": {\n    \"0\": { \"id\": \"tx_123\", \"macro_class\": \"EXPENSE\" }\n  }\n}\n```",
			wantLen:   1,
			expectErr: false,
		},
		{
			name: "JSON inside bare ``` fence",
			respText: "```\n{\n  \"op\": \"add\",\n  \"path\": \"/rows\",\n  \"value\": {\n    \"0\": { \"id\": \"tx_123\", \"macro_class\": \"EXPENSE\" }\n  }\n}\n```",
			wantLen:   1,
			expectErr: false,
		},
		{
			name: "Malformed JSON — extra trailing brace",
			respText: "```json\n{\"op\": \"add\", \"path\": \"/rows\", \"value\": {\"0\": {\"id\": \"tx_123\", \"macro_class\": \"EXPENSE\"}}}}\n```",
			wantLen:   0,
			expectErr: true,
		},
		{
			name: "Comma-separated objects wrapped in array fallback",
			respText: "```json\n{\"op\": \"add\", \"path\": \"/rows\", \"value\": {\"0\": {\"id\": \"tx_1\", \"macro_class\": \"EXPENSE\"}}},\n{\"op\": \"add\", \"path\": \"/rows\", \"value\": {\"0\": {\"id\": \"tx_2\", \"macro_class\": \"REVENUE\"}}}\n```",
			wantLen:   2,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches, err := extractJSONPatches(tt.respText)
			if (err != nil) != tt.expectErr {
				t.Fatalf("extractJSONPatches() error = %v, expectErr = %v", err, tt.expectErr)
			}
			if !tt.expectErr {
				if len(patches) != tt.wantLen {
					t.Errorf("extractJSONPatches() got len = %d, want = %d", len(patches), tt.wantLen)
				}
				// Verify it unmarshals successfully
				for _, p := range patches {
					var m map[string]interface{}
					if err := json.Unmarshal(p, &m); err != nil {
						t.Errorf("failed to unmarshal extracted patch: %v", err)
					}
				}
			}
		})
	}
}

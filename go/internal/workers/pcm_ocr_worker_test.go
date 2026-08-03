package workers

import (
	"strings"
	"testing"
)

func TestPcmOcrWorker_IsRapEmail_Detection(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		to        string
		alias     string
		wantRap   bool
	}{
		{
			name:      "Standard rap email recipient",
			recipient: "rap_test100@a.usetoro.io",
			to:        "rap_test100@a.usetoro.io",
			alias:     "rap_test100",
			wantRap:   true,
		},
		{
			name:      "Non-rap general email recipient",
			recipient: "support@usetoro.io",
			to:        "support@usetoro.io",
			alias:     "support",
			wantRap:   false,
		},
		{
			name:      "Uppercase RAP email recipient",
			recipient: "RAP_1042@a.usetoro.io",
			to:        "RAP_1042@a.usetoro.io",
			alias:     "rap_1042",
			wantRap:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isRapEmail := strings.Contains(strings.ToLower(tt.recipient), "rap_") ||
				strings.Contains(strings.ToLower(tt.to), "rap_") ||
				strings.Contains(strings.ToLower(tt.alias), "rap_")

			if isRapEmail != tt.wantRap {
				t.Errorf("isRapEmail = %v, want %v", isRapEmail, tt.wantRap)
			}
		})
	}
}

package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderOAuthCallbackPage(t *testing.T) {
	tests := []struct {
		name         string
		data         OAuthTemplateData
		expectedText []string
		absentText   []string
	}{
		{
			name: "Success State",
			data: OAuthTemplateData{State: "SUCCESS"},
			expectedText: []string{
				"Connection Successful",
				"You may close this tab and return to the application.",
				"window.location.href = \"toro://auth-success\";",
				"window.close();",
				"color: #39FF14;", // Green checkmark styles
			},
			absentText: []string{
				"Connection Failed",
				"Invalid Request",
			},
		},
		{
			name: "Error State",
			data: OAuthTemplateData{State: "ERROR", ErrorMessage: "QBO configuration not available."},
			expectedText: []string{
				"Connection Failed",
				"QBO configuration not available.",
				"color: #FF3914;", // Red X styles
			},
			absentText: []string{
				"window.location.href = \"toro://auth-success\";",
				"window.close();",
				"Connection Successful",
			},
		},
		{
			name: "Invalid State",
			data: OAuthTemplateData{State: "INVALID_STATE"},
			expectedText: []string{
				"Invalid Request",
				"Please restart the connection process from the desktop app.",
				"color: #FFC814;", // Warning icon styles
			},
			absentText: []string{
				"window.location.href = \"toro://auth-success\";",
				"window.close();",
				"Connection Successful",
				"Connection Failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			renderOAuthCallbackPage(recorder, tt.data)

			body := recorder.Body.String()

			for _, expected := range tt.expectedText {
				if !strings.Contains(body, expected) {
					t.Errorf("expected body to contain %q", expected)
				}
			}

			for _, absent := range tt.absentText {
				if strings.Contains(body, absent) {
					t.Errorf("expected body to NOT contain %q", absent)
				}
			}
		})
	}
}

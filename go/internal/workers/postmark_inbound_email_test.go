package workers

import (
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestParseAgentEmail(t *testing.T) {
	config.SetGlobal(&config.Config{
		VirtualEmployees: map[string]config.AgentAlias{
			"alex":   {},
			"robert": {},
		},
	})

	tests := []struct {
		name          string
		email         string
		wantAlias     string
		wantSubdomain string
	}{
		{
			name:          "Basic",
			email:         "mark@a.usetoro.io",
			wantAlias:     "mark",
			wantSubdomain: "a",
		},
		{
			name:          "With Name",
			email:         "Mark Smith <mark@a.usetoro.io>",
			wantAlias:     "mark",
			wantSubdomain: "a",
		},
		{
			name:          "No Subdomain",
			email:         "sarah@usetoro.io",
			wantAlias:     "sarah",
			wantSubdomain: "usetoro",
		},
		{
			name:          "Name Only",
			email:         "Alex",
			wantAlias:     "alex",
			wantSubdomain: "",
		},
		{
			name:          "Full Name Only",
			email:         "Robert Johnson",
			wantAlias:     "robert",
			wantSubdomain: "",
		},
		{
			name:          "Unknown",
			email:         "unknown",
			wantAlias:     "",
			wantSubdomain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, subdomain := parseAgentEmail(tt.email)
			assert.Equal(t, tt.wantAlias, alias)
			assert.Equal(t, tt.wantSubdomain, subdomain)
		})
	}
}

func TestCleanMessageID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "Standard",
			id:   "<12345.67890@example.com>",
			want: "12345.67890",
		},
		{
			name: "No brackets",
			id:   "12345.67890@example.com",
			want: "12345.67890",
		},
		{
			name: "No domain",
			id:   "<12345.67890>",
			want: "12345.67890",
		},
		{
			name: "Just ID",
			id:   "12345.67890",
			want: "12345.67890",
		},
		{
			name: "With spaces",
			id:   "  <12345.67890@example.com>  ",
			want: "12345.67890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanMessageID(tt.id)
			assert.Equal(t, tt.want, got)
		})
	}
}

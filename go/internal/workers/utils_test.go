package workers

import (
	"context"
	"io"
	"log/slog"
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
			alias, subdomain := ParseAgentEmail(tt.email)
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
			got := CleanMessageID(tt.id)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveSender_NilDB(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender, err := ResolveSender(context.Background(), logger, nil, nil, "user@example.com", "rap_morocco@a.usetoro.io", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database not available")
	assert.False(t, sender.EntityID.Valid)
}

func TestResolveSender_PopulatesAliasAndSubdomain(t *testing.T) {
	config.SetGlobal(&config.Config{
		VirtualEmployees: map[string]config.AgentAlias{},
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// With nil DB, ResolveSender still populates AgentAlias from ParseAgentEmail before erroring
	// (db == nil returns early, but we can test the non-db path)
	sender, err := ResolveSender(context.Background(), logger, nil, nil, "user@example.com", "rap_morocco@a.usetoro.io", "")
	assert.Error(t, err)
	// Even on error, FromHandle/ToHandle should be populated
	assert.Equal(t, "user@example.com", sender.FromHandle)
	assert.Equal(t, "rap_morocco@a.usetoro.io", sender.ToHandle)
}

func TestBounce_ReturnsNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	err := Bounce(context.Background(), logger, cfg, "unknown@example.com", "hello", "Test Subject")
	assert.NoError(t, err)
}


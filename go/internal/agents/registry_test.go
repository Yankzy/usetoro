package agents

import (
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
)

func TestLookup_GlobalConfig(t *testing.T) {
	// Setup test mock config
	cfg := &config.Config{
		VirtualEmployees: map[string]config.AgentAlias{
			"test-agent": {
				Name:         "Test Agent",
				Description:  "Test Desc",
				Email:        "test@example.com",
				SystemPrompt: "Test Prompt",
			},
		},
	}

	// Save existing global config
	oldCfg := config.GetGlobal()
	defer config.SetGlobal(oldCfg)

	config.SetGlobal(cfg)

	// Trigger lookup to check it loads from the global config
	a := Lookup("test-agent")
	if a == nil {
		t.Fatalf("Lookup returned nil")
	}
	if a.Name != "Test Agent" {
		t.Errorf("Name = %q, want %q", a.Name, "Test Agent")
	}
	if a.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", a.Email, "test@example.com")
	}
}

package tools_test

import (
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

func TestAgentRegistry_BuiltIns(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.RegisterBuiltIns()

	tests := []struct {
		agentType string
		wantFound bool
	}{
		{"general-purpose", true},
		{"explore", true},
		{"plan", true},
		{"nonexistent", false},
	}

	for _, tt := range tests {
		def, ok := reg.Get(tt.agentType)
		if ok != tt.wantFound {
			t.Errorf("Get(%q) found=%v, want %v", tt.agentType, ok, tt.wantFound)
		}
		if tt.wantFound && def.Type != tt.agentType {
			t.Errorf("Get(%q).Type = %q, want %q", tt.agentType, def.Type, tt.agentType)
		}
	}
}

func TestAgentRegistry_ExploreDefinition(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.RegisterBuiltIns()

	def, ok := reg.Get("explore")
	if !ok {
		t.Fatal("explore agent not found")
	}

	// Explore should not have Agent, FileWrite, FileEdit
	disallowed := map[string]bool{}
	for _, name := range def.DisallowedTools {
		disallowed[name] = true
	}
	if !disallowed["Agent"] {
		t.Error("explore should disallow Agent")
	}
	if !disallowed["FileWrite"] {
		t.Error("explore should disallow FileWrite")
	}
	if !disallowed["FileEdit"] {
		t.Error("explore should disallow FileEdit")
	}

	// Tools should be ["*"]
	if len(def.Tools) != 1 || def.Tools[0] != "*" {
		t.Errorf("explore Tools = %v, want [\"*\"]", def.Tools)
	}
}

func TestAgentRegistry_GeneralPurposeDefinition(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.RegisterBuiltIns()

	def, ok := reg.Get("general-purpose")
	if !ok {
		t.Fatal("general-purpose agent not found")
	}

	if len(def.Tools) != 1 || def.Tools[0] != "*" {
		t.Errorf("general-purpose Tools = %v, want [\"*\"]", def.Tools)
	}
	if len(def.DisallowedTools) != 0 {
		t.Errorf("general-purpose should have no disallowed tools, got %v", def.DisallowedTools)
	}
}

func TestAgentRegistry_List(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.RegisterBuiltIns()

	list := reg.List()
	if len(list) != 3 {
		t.Errorf("List() returned %d definitions, want 3", len(list))
	}
}

func TestAgentRegistry_Override(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.Register(tools.AgentDefinition{Type: "test", Tools: []string{"ToolA"}})
	def, ok := reg.Get("test")
	if !ok || def.Tools[0] != "ToolA" {
		t.Fatal("first register failed")
	}

	// Override
	reg.Register(tools.AgentDefinition{Type: "test", Tools: []string{"ToolB"}})
	def, ok = reg.Get("test")
	if !ok || def.Tools[0] != "ToolB" {
		t.Fatal("override failed")
	}
}

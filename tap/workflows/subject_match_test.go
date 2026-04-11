package workflows

import "testing"

func TestMatchNATSSubject(t *testing.T) {
	tests := []struct {
		pattern string
		subject string
		want    bool
	}{
		{"events.accounting.*.cleanup", "events.accounting.1.cleanup", true},
		{"events.accounting.*.cleanup", "events.accounting.abc.cleanup", true},
		{"events.accounting.*.cleanup", "events.accounting.1.cleanup.extra", false},
		{"events.accounting.*.cleanup", "events.accounting.cleanup", false},
		{"events.>", "events.accounting.1.cleanup", true},
		{"events.accounting.>", "events.accounting.1.cleanup", true},
		{"events.accounting.>", "events.sales.1.cleanup", false},
		{"events.accounting.1.cleanup", "events.accounting.1.cleanup", true},
		{"events.accounting.1.cleanup", "events.accounting.2.cleanup", false},
	}

	for _, tt := range tests {
		if got := matchNATSSubject(tt.pattern, tt.subject); got != tt.want {
			t.Fatalf("matchNATSSubject(%q, %q) = %v, want %v", tt.pattern, tt.subject, got, tt.want)
		}
	}
}

func TestResolveBlueprintForSubject_PrefersMoreSpecific(t *testing.T) {
	o := &Orchestrator{
		blueprints: []WorkflowDef{
			{Name: "wild", TriggerTopic: "events.accounting.*.cleanup"},
			{Name: "exact", TriggerTopic: "events.accounting.1.cleanup"},
		},
	}

	def, err := o.resolveBlueprintForSubject("events.accounting.1.cleanup")
	if err != nil {
		t.Fatalf("resolveBlueprintForSubject error: %v", err)
	}
	if def.Name != "exact" {
		t.Fatalf("expected 'exact' blueprint, got %q", def.Name)
	}
}

package workflows

import (
	"reflect"
	"testing"
)

func TestBuildConversationID(t *testing.T) {

	tests := []struct {
		name string
		path []string
		step string
		want string
	}{
		{"top-level", []string{"root"}, "start", "root/start"},
		{"nested", []string{"root", "child"}, "map_rows", "root/child/map_rows"},
		{"deep", []string{"a", "b", "c"}, "final", "a/b/c/final"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildConversationID(tt.path, tt.step); got != tt.want {
				t.Fatalf("buildConversationID(%v, %q) = %q, want %q", tt.path, tt.step, got, tt.want)
			}
		})
	}
}

func TestParseConversationID(t *testing.T) {
	cid := "root/child/final_step"
	path, step, err := parseConversationID(cid)
	if err != nil {
		t.Fatalf("parseConversationID(%q) returned %v", cid, err)
	}
	if step != "final_step" {
		t.Fatalf("expected step final_step, got %q", step)
	}
	if !reflect.DeepEqual(path, []string{"root", "child"}) {
		t.Fatalf("expected path [root child], got %v", path)
	}
}


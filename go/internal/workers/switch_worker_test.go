package workers

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestSwitch_SenderMode_Expression(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "expression",
		Output:         5,
		FallbackOutput: -1,
	}

	payload := `[{"id": 1}, {"id": 2}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	if !res.IsArray() {
		t.Fatal("result is not an array")
	}

	if len(res.Array()) != 2 {
		t.Errorf("expected 2 items, got %d", len(res.Array()))
	}

	res.ForEach(func(_, item gjson.Result) bool {
		if item.Get("route").Int() != 5 {
			t.Errorf("expected route 5, got %d", item.Get("route").Int())
		}
		if !item.Get("id").Exists() {
			t.Error("missing original data in wrapped output")
		}
		return true
	})
}

func TestSwitch_SenderMode_Rules_Boolean(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "rules",
		DataType:       "boolean",
		Value1Path:     "active",
		FallbackOutput: -1,
		Rules: []Rule{
			{Operation: "equal", Value2: true, Output: 1},
			{Operation: "notEqual", Value2: true, Output: 2},
		},
	}

	payload := `[{"active": true, "name": "A"}, {"active": false, "name": "B"}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	items := res.Array()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Get("route").Int() != 1 || items[0].Get("name").String() != "A" {
		t.Errorf("item 0 mismatch: %v", items[0].Raw)
	}
	if items[1].Get("route").Int() != 2 || items[1].Get("name").String() != "B" {
		t.Errorf("item 1 mismatch: %v", items[1].Raw)
	}
}

func TestSwitch_SenderMode_Rules_Number(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "rules",
		DataType:       "number",
		Value1Path:     "amount",
		FallbackOutput: -1,
		Rules: []Rule{
			{Operation: "smaller", Value2: 100.0, Output: 1},
			{Operation: "largerEqual", Value2: 100.0, Output: 2},
		},
	}

	payload := `[{"amount": 50}, {"amount": 100}, {"amount": 150}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	items := res.Array()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	if items[0].Get("route").Int() != 1 {
		t.Errorf("item 0 (50) should be route 1, got %d", items[0].Get("route").Int())
	}
	if items[1].Get("route").Int() != 2 {
		t.Errorf("item 1 (100) should be route 2, got %d", items[1].Get("route").Int())
	}
	if items[2].Get("route").Int() != 2 {
		t.Errorf("item 2 (150) should be route 2, got %d", items[2].Get("route").Int())
	}
}

func TestSwitch_SenderMode_Rules_String(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		value2    interface{}
		input     string
		matches   bool
	}{
		{"contains", "contains", "toro", `{"name": "usetoro"}`, true},
		{"notContains", "notContains", "toro", `{"name": "google"}`, true},
		{"startsWith", "startsWith", "abc", `{"name": "abcdef"}`, true},
		{"endsWith", "endsWith", "xyz", `{"name": "abcxyz"}`, true},
		{"equal", "equal", "hello", `{"name": "hello"}`, true},
		{"regex", "regex", "^[0-9]+$", `{"name": "12345"}`, true},
		{"regex_fail", "regex", "^[0-9]+$", `{"name": "123a45"}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := SwitchConfig{
				NodeMode:       "sender",
				Mode:           "rules",
				DataType:       "string",
				Value1Path:     "name",
				FallbackOutput: -1,
				Rules: []Rule{
					{Operation: tc.operation, Value2: tc.value2, Output: 1},
				},
			}
			payload := "[" + tc.input + "]"
			result, err := Switch([]byte(payload), config)
			if err != nil {
				t.Fatalf("Switch failed: %v", err)
			}
			res := gjson.ParseBytes(result)
			items := res.Array()
			if tc.matches {
				if len(items) != 1 {
					t.Errorf("expected match for %s, got 0", tc.name)
				} else if items[0].Get("route").Int() != 1 {
					t.Errorf("expected route 1 for %s, got %d", tc.name, items[0].Get("route").Int())
				}
			} else {
				if len(items) != 0 {
					t.Errorf("expected no match for %s, got %d", tc.name, len(items))
				}
			}
		})
	}
}

func TestSwitch_SenderMode_Rules_DateTime(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "rules",
		DataType:       "dateTime",
		Value1Path:     "created_at",
		FallbackOutput: -1,
		Rules: []Rule{
			{Operation: "after", Value2: "2024-01-01T00:00:00Z", Output: 1},
		},
	}

	payload := `[{"created_at": "2024-02-01T12:00:00Z"}, {"created_at": "2023-12-31T23:59:59Z"}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	items := res.Array()
	if len(items) != 1 {
		t.Fatalf("expected 1 match, got %d", len(items))
	}
	if items[0].Get("created_at").String() != "2024-02-01T12:00:00Z" {
		t.Errorf("wrong item matched: %s", items[0].Get("created_at").String())
	}
}

func TestSwitch_SenderMode_Fallback(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "rules",
		DataType:       "number",
		Value1Path:     "val",
		Rules:          []Rule{{Operation: "equal", Value2: 1, Output: 1}},
		FallbackOutput: 99,
	}

	payload := `[{"val": 1}, {"val": 2}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	items := res.Array()
	if items[0].Get("route").Int() != 1 {
		t.Errorf("item 0 should be 1, got %d", items[0].Get("route").Int())
	}
	if items[1].Get("route").Int() != 99 {
		t.Errorf("item 1 should be 99, got %d", items[1].Get("route").Int())
	}
}

func TestSwitch_ReceiverMode(t *testing.T) {
	config := SwitchConfig{
		NodeMode:   "receiver",
		RouteIndex: 1,
	}

	payload := `[{"route": 1, "id": 101}, {"route": 2, "id": 102}, {"route": 1, "id": 103}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	items := res.Array()
	if len(items) != 2 {
		t.Fatalf("expected 2 unwrapped items, got %d", len(items))
	}

	if items[0].Get("id").Int() != 101 {
		t.Errorf("item 0 mismatch: %v", items[0].Raw)
	}
	if items[1].Get("id").Int() != 103 {
		t.Errorf("item 1 mismatch: %v", items[1].Raw)
	}
}

func TestSwitch_NestedPaths(t *testing.T) {
	config := SwitchConfig{
		NodeMode:       "sender",
		Mode:           "rules",
		DataType:       "string",
		Value1Path:     "user.profile.role",
		FallbackOutput: -1,
		Rules:          []Rule{{Operation: "equal", Value2: "admin", Output: 1}},
	}

	payload := `[{"user": {"profile": {"role": "admin"}}}, {"user": {"profile": {"role": "user"}}}]`
	result, err := Switch([]byte(payload), config)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	res := gjson.ParseBytes(result)
	if len(res.Array()) != 1 {
		t.Fatalf("expected 1 match, got %d", len(res.Array()))
	}
}

func TestSwitch_Errors(t *testing.T) {
	t.Run("not_array", func(t *testing.T) {
		_, err := Switch([]byte(`{"not": "array"}`), SwitchConfig{})
		if err == nil {
			t.Error("expected error for non-array payload")
		}
	})

	t.Run("invalid_node_mode", func(t *testing.T) {
		_, err := Switch([]byte(`[]`), SwitchConfig{NodeMode: "invalid"})
		if err == nil {
			t.Error("expected error for invalid nodeMode")
		}
	})
}

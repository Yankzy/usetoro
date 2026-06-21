package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/services/cleanup"
)

func TestRunEnrichmentRedux_SystemWrapper(t *testing.T) {
	ctx := context.Background()

	// 1. Construct a base state representing a real InstanceState wrapper from the DB
	// which contains system arrays like instance_path.
	baseStateMap := map[string]interface{}{
		"workflow_def":  "CSV Cleaner Pipeline",
		"instance_path": []interface{}{"b41eeeaa-5ef0-4edb-9b55-7fde32529f17", "fdfeb073-5cd8-4db8-ad1a-0e63b25ac146"},
		"variables": map[string]interface{}{
			"TRIGGER": map[string]interface{}{
				"rows": []interface{}{"row1", "row2"}, // Contains arrays inside variables map
			},
		},
		"last_proof": nil,
	}
	baseStateBytes, _ := json.Marshal(baseStateMap)

	// 2. Prepare test enriched rows
	allRows := []*cleanup.EnrichedRow{
		{
			ID:                 "row_1",
			SessionID:          "session_123",
			RealmID:            "9341456276406470",
			RawDescription:     "Amazon purchase",
			RawAmount:          -125.50,
			RawDate:            time.Now(),
			PredictedAccountID: "acct_123",
			ConfidenceScore:    1.0,
			AIReasoning:        "Amazon mapping succeeded",
		},
	}

	t.Run("Success - System Wrapper Bypasses no-array", func(t *testing.T) {
		nextState, nextSeq, faults, err := RunEnrichmentRedux(ctx, baseStateBytes, 0, allRows)
		if err != nil {
			t.Fatalf("unexpected fatal error: %v", err)
		}
		if len(faults) > 0 {
			t.Fatalf("unexpected domain faults: %+v", faults)
		}
		if nextSeq != 1 {
			t.Errorf("expected next sequence to be 1, got %d", nextSeq)
		}

		// Verify state keys
		var nextStateMap map[string]interface{}
		if err := json.Unmarshal(nextState, &nextStateMap); err != nil {
			t.Fatalf("failed to parse nextState: %v", err)
		}

		if nextStateMap["status"] != "ENRICHED" {
			t.Errorf("expected status 'ENRICHED', got %v", nextStateMap["status"])
		}

		enrichments, ok := nextStateMap["enrichments"].(map[string]interface{})
		if !ok {
			t.Fatal("expected enrichments to be present as a map")
		}

		row1, ok := enrichments["row_1"].(map[string]interface{})
		if !ok {
			t.Fatal("expected row_1 enrichment to be present")
		}

		if row1["predicted_account_id"] != "acct_123" {
			t.Errorf("expected predicted_account_id 'acct_123', got %v", row1["predicted_account_id"])
		}
	})

	t.Run("Failure - Array Injected inside Domain Field is Denied", func(t *testing.T) {
		// Mock rows containing an array in a domain field.
		// SplitSuggestion is not allowed to contain arrays in the Redux trace.
		// In Go it is converted to map[string]cleanup.SplitLine, but let's verify if that fails
		// if a domain-level array is injected.
		// Since SplitSuggestion is mapped to map[string]SplitLine, let's inject a custom array
		// directly in the base state or test a raw reduction.
		// Instead of constructing it via Go struct, we can test by manually adding an array key
		// to the baseState outside the systemKeys list.
		badBaseStateMap := map[string]interface{}{
			"workflow_def":        "CSV Cleaner Pipeline",
			"instance_path":       []interface{}{"u1"},
			"variables":           map[string]interface{}{},
			"custom_domain_array": []interface{}{"violating_array"}, // Not a system key, so checked!
		}
		badBaseStateBytes, _ := json.Marshal(badBaseStateMap)

		_, _, faults, err := RunEnrichmentRedux(ctx, badBaseStateBytes, 0, allRows)
		if err != nil {
			t.Fatalf("unexpected fatal error: %v", err)
		}
		if len(faults) != 1 {
			t.Fatalf("expected exactly 1 domain fault, got %d", len(faults))
		}
		if !strings.Contains(faults[0].Error, "no-array rule violation") {
			t.Errorf("expected no-array violation error, got: %s", faults[0].Error)
		}
	})
}

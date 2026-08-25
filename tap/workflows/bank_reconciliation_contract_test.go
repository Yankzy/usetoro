package workflows

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/spf13/viper"
)

func loadRepositoryWorkflow(t *testing.T, path string) WorkflowDef {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read workflow %s: %v", path, err)
	}
	var definition WorkflowDef
	if err := v.Unmarshal(&definition); err != nil {
		t.Fatalf("decode workflow %s: %v", path, err)
	}
	return definition
}

func TestBankReconciliationBlueprintsAreDirectlyRoutable(t *testing.T) {
	paths, err := filepath.Glob("bank_reconciliation_*.yml")
	if err != nil || len(paths) != 8 {
		t.Fatalf("expected eight lifecycle/review blueprints, got %d (%v)", len(paths), err)
	}
	topics := make(map[string]string)
	for _, path := range paths {
		definition := loadRepositoryWorkflow(t, path)
		if previous := topics[definition.TriggerTopic]; previous != "" {
			t.Fatalf("duplicate trigger topic %q in %s and %s", definition.TriggerTopic, previous, path)
		}
		topics[definition.TriggerTopic] = path
		stepIDs := make(map[string]bool)
		for _, step := range definition.Steps {
			stepIDs[step.ID] = true
		}
		for _, step := range definition.Steps {
			for _, dependency := range step.DependsOn {
				if !stepIDs[dependency] {
					t.Fatalf("%s step %s has missing dependency %s", path, step.ID, dependency)
				}
			}
			if step.SubWorkflow != "" || strings.HasPrefix(step.ActivityType, "hitl.") {
				continue
			}
			if _, err := core.BuildWorkerInboxFromActivity(step.ActivityType); err != nil {
				t.Fatalf("%s step %s cannot route to a persistent worker: %v", path, step.ID, err)
			}
			if step.WorkflowSchema != "" && !json.Valid([]byte(step.WorkflowSchema)) {
				t.Fatalf("%s step %s has invalid JSON schema", path, step.ID)
			}
		}
	}
}

func TestPCMWorkflowPreservesStage2AndApprovalContext(t *testing.T) {
	definition := loadRepositoryWorkflow(t, "pcm_workflow.yml")
	steps := make(map[string]WorkflowStep)
	for _, step := range definition.Steps {
		steps[step.ID] = step
		if strings.Contains(step.ActivityType, "export") || strings.Contains(step.ActivityType, "reconciliation.close") {
			t.Fatalf("inbound PCM must not export or close: %s", step.ActivityType)
		}
	}
	if len(steps["statement_intake"].SuspendRoutes) != 1 || steps["statement_intake"].SuspendRoutes[0] != 1 {
		t.Fatal("unreliable statement intake must suspend the workflow")
	}
	stage2State := InstanceState{
		InstancePath: []string{"root", "atlas-july-workflow"},
		Variables:    map[string]json.RawMessage{},
		LastProof: json.RawMessage(`{
			"session_id":"atlas-session","entity_id":"atlas-entity","realm_id":"rap_atlas_sarl",
			"bank_account_id":"atlas-bank","bank_ledger_account_code":"514100","currency":"MAD",
			"source_document_ids":["atlas-statement"],"canonical_bank_line_ids":["atlas-line"],
			"statement_period_key":"2026-07","statement_opening_balance":"184325.7200",
			"statement_closing_balance":"184075.7200"
		}`),
	}
	stage2Payload := unwrapStepPayload(buildStepPayload(steps["stage2"], stage2State, stage2State.LastProof))
	var stage2 map[string]json.RawMessage
	if err := json.Unmarshal(stage2Payload, &stage2); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workflow_id", "workflow_trace_id", "session_id", "entity_id", "realm_id", "bank_account_id", "bank_ledger_account_code", "currency", "source_document_ids", "canonical_bank_line_ids", "statement_period_key", "statement_opening_balance", "statement_closing_balance"} {
		if len(stage2[key]) == 0 {
			t.Fatalf("Stage-2 lost required workflow field %s: %s", key, stage2Payload)
		}
	}

	state := InstanceState{
		InstancePath: []string{"root", "atlas-july-workflow"},
		Variables: map[string]json.RawMessage{
			"TRIGGER":          json.RawMessage(`{"entity_id":"atlas-entity"}`),
			"stage2":           json.RawMessage(`{"results":[{"stage2_hash":"abc","stage2_output":{"schema_version":"pcm.stage2.v1"}}]}`),
			"treatment_review": json.RawMessage(`{"action":"approved","actor_user_id":"atlas-accountant"}`),
		},
		LastProof: json.RawMessage(`{"action":"approved","actor_user_id":"atlas-accountant"}`),
	}
	journalPayload := unwrapStepPayload(buildStepPayload(steps["journal_post"], state, state.LastProof))
	var journal map[string]json.RawMessage
	if err := json.Unmarshal(journalPayload, &journal); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"entity_id", "actor_user_id", "results"} {
		if len(journal[key]) == 0 {
			t.Fatalf("journal posting lost required workflow field %s: %s", key, journalPayload)
		}
	}
}

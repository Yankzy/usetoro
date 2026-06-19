package vcoo

import (
	"strings"
	"testing"
)

func TestParseAndFormatFounderState(t *testing.T) {
	// 1. Raw Markdown without frontmatter (backwards compatible)
	mdContent := `# MASTER SYSTEM INTENT: FOUNDER MATRIX v1
## Baseline Constraints: Launch Window June 2026

### 1. Mandatory Engineering Milestones
- Task ID: ` + "`" + `ENGINE-SANDBOX-WASM` + "`" + ` | Weight: 0.40 | Status: ACTIVE
- Task ID: ` + "`" + `ENGINE-MAIL-POSTMARK` + "`" + ` | Weight: 0.30 | Status: BACKLOG

### 2. The 10X Weekly Distribution Benchmarks (Targets)
- Target ID: ` + "`" + `OUTBOUND_CALLS` + "`" + `  | Metric: 250 | Unit: Dials
- Target ID: ` + "`" + `VIDEO_PROOFS` + "`" + `    | Metric: 10  | Unit: Drops
- Target ID: ` + "`" + `X_DAILY_POSTS` + "`" + `   | Metric: 35  | Unit: Posts
- Target ID: ` + "`" + `ONBOARDING_RUNS` + "`" + ` | Metric: 5   | Unit: Calls
`

	state, err := ParseFounderState(mdContent)
	if err != nil {
		t.Fatalf("failed to parse state: %v", err)
	}

	if len(state.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(state.Tasks))
	}
	if state.Tasks[0].ID != "ENGINE-SANDBOX-WASM" || state.Tasks[0].Weight != 0.40 || state.Tasks[0].Status != "ACTIVE" {
		t.Errorf("task 0 incorrect: %+v", state.Tasks[0])
	}
	if state.Tasks[1].ID != "ENGINE-MAIL-POSTMARK" || state.Tasks[1].Weight != 0.30 || state.Tasks[1].Status != "BACKLOG" {
		t.Errorf("task 1 incorrect: %+v", state.Tasks[1])
	}

	if len(state.Targets) != 4 {
		t.Fatalf("expected 4 targets, got %d", len(state.Targets))
	}
	if state.Targets[0].ID != "OUTBOUND_CALLS" || state.Targets[0].Metric != 250 || state.Targets[0].Unit != "Dials" {
		t.Errorf("target 0 incorrect: %+v", state.Targets[0])
	}

	// 2. OKF Markdown with YAML frontmatter
	okfContent := `---
type: Playbook
title: Founder Playbook
description: Master system targets
---
# MASTER SYSTEM INTENT: FOUNDER MATRIX v1
## Baseline Constraints: Launch Window June 2026

### 1. Mandatory Engineering Milestones
- Task ID: ` + "`" + `ENGINE-SANDBOX-WASM` + "`" + ` | Weight: 0.40 | Status: ACTIVE
`
	stateOKF, err := ParseFounderState(okfContent)
	if err != nil {
		t.Fatalf("failed to parse OKF state: %v", err)
	}
	if len(stateOKF.Tasks) != 1 || stateOKF.Tasks[0].ID != "ENGINE-SANDBOX-WASM" {
		t.Errorf("failed to parse tasks from OKF content: %+v", stateOKF)
	}

	// 3. Format state (should output YAML frontmatter)
	formatted := FormatFounderState(state)
	if !strings.Contains(formatted, "type: Playbook") {
		t.Errorf("formatted markdown missing OKF frontmatter header: %s", formatted)
	}
	if !strings.Contains(formatted, "ENGINE-SANDBOX-WASM") {
		t.Errorf("formatted markdown missing task ID: %s", formatted)
	}

	// Roundtrip check
	stateRoundtrip, err := ParseFounderState(formatted)
	if err != nil {
		t.Fatalf("failed to parse formatted output: %v", err)
	}
	if len(stateRoundtrip.Tasks) != 2 || stateRoundtrip.Tasks[0].ID != "ENGINE-SANDBOX-WASM" {
		t.Errorf("roundtrip state parsing failed: %+v", stateRoundtrip)
	}
}

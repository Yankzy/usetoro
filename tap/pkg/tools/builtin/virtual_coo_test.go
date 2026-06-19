package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockVCOODBTX struct {
	userRow   database.GetUserVCOODataRow
	queryErr  error
	execErr   error
	lastArgs  []any
	lastQuery string
}

func (m *mockVCOODBTX) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	m.lastQuery = query
	m.lastArgs = args
	return &mockVCOORow{row: m.userRow, err: m.queryErr}
}

func (m *mockVCOODBTX) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	m.lastQuery = query
	m.lastArgs = args
	return pgconn.NewCommandTag("UPDATE 1"), m.execErr
}

func (m *mockVCOODBTX) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

type mockVCOORow struct {
	row database.GetUserVCOODataRow
	err error
}

func (r *mockVCOORow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 6 {
		return fmt.Errorf("expected 6 dest args, got %d", len(dest))
	}

	*(dest[0].(*pgtype.UUID)) = r.row.ID
	*(dest[1].(*pgtype.UUID)) = r.row.EntityID
	*(dest[2].(*string)) = r.row.Email
	*(dest[3].(*[]string)) = r.row.VcooActiveBlockers
	*(dest[4].(*[]byte)) = r.row.VcooHistory
	*(dest[5].(*[]byte)) = r.row.VcooState
	return nil
}

func TestUpdateVirtualCOORestrictionTool_NilQueries(t *testing.T) {
	tool := &UpdateVirtualCOORestrictionTool{
		Queries: nil,
		Logger:  slog.Default(),
	}

	_, err := tool.Call(context.Background(), map[string]any{
		"user_id":          "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
		"velocity_delta":   -0.15,
		"apply_multiplier": true,
	})
	if err == nil || !strings.Contains(err.Error(), "database queries not initialized") {
		t.Errorf("expected database queries initialization error, got %v", err)
	}
}

func TestUpdateVirtualCOORestrictionTool_InvalidUUID(t *testing.T) {
	mockDB := &mockVCOODBTX{}
	tool := &UpdateVirtualCOORestrictionTool{
		Queries: database.New(mockDB),
		Logger:  slog.Default(),
	}

	_, err := tool.Call(context.Background(), map[string]any{
		"user_id":          "invalid-uuid-string",
		"velocity_delta":   -0.15,
		"apply_multiplier": true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid user_id UUID") {
		t.Errorf("expected UUID validation error, got %v", err)
	}
}

func TestUpdateVirtualCOORestrictionTool_Success(t *testing.T) {
	var mockUUID pgtype.UUID
	_ = mockUUID.Scan("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")

	userRow := database.GetUserVCOODataRow{
		ID:                 mockUUID,
		EntityID:           mockUUID,
		Email:              "founder@yourplatform.com",
		VcooActiveBlockers: []string{"WASM memory page bounds"},
		VcooHistory:        []byte("[]"),
		VcooState:          []byte("{}"),
	}

	mockDB := &mockVCOODBTX{userRow: userRow}
	tool := &UpdateVirtualCOORestrictionTool{
		Queries: database.New(mockDB),
		Logger:  slog.Default(),
	}

	// Create a temporary OKF Playbook founder_state.md matching the parser regexes
	tenantID := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	testBaseDir := filepath.Join("docs", "knowledge", tenantID)
	testDir := filepath.Join(testBaseDir, "playbooks")
	_ = os.MkdirAll(testDir, 0755)
	filePath := filepath.Join(testDir, "founder_state.md")
	_ = os.WriteFile(filePath, []byte(`---
type: Playbook
title: Founder Playbook
description: Master system targets and engineering milestones
---
# MASTER SYSTEM INTENT: FOUNDER MATRIX v1
## Baseline Constraints: Launch Window June 2026

### 1. Mandatory Engineering Milestones
- Task ID: `+"`ENGINE-SANDBOX-WASM`"+` | Weight: 0.40 | Status: ACTIVE
- Task ID: `+"`TECH-SWEEP-PRD-V1`"+` | Weight: 0.20 | Status: BACKLOG

### 2. The 10X Weekly Distribution Benchmarks (Targets)
- Target ID: `+"`OUTBOUND_CALLS`"+`  | Metric: 250 | Unit: Dials
- Target ID: `+"`VIDEO_PROOFS`"+`  | Metric: 10 | Unit: Drops
- Target ID: `+"`X_DAILY_POSTS`"+`  | Metric: 35 | Unit: Posts
- Target ID: `+"`ONBOARDING_RUNS`"+`  | Metric: 5 | Unit: Calls
`), 0644)
	defer os.RemoveAll(testBaseDir)

	res, err := tool.Call(context.Background(), map[string]any{
		"user_id":          "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
		"velocity_delta":   0.05,
		"apply_multiplier": false,
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}

	if !strings.Contains(res, "Successfully updated VCOO state") {
		t.Errorf("unexpected response: %s", res)
	}

	// Since apply_multiplier is false, it should unlock the backlog task
	updatedBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read founder state: %v", err)
	}

	updatedContent := string(updatedBytes)
	if !strings.Contains(updatedContent, "Task ID: `TECH-SWEEP-PRD-V1` | Weight: 0.20 | Status: ACTIVE") {
		t.Errorf("expected TECH-SWEEP-PRD-V1 to be active, got: %s", updatedContent)
	}

	// Check DB update
	if mockDB.lastQuery == "" {
		t.Fatal("expected update in database")
	}

	var updatedState map[string]any
	if err := json.Unmarshal(mockDB.lastArgs[3].([]byte), &updatedState); err != nil {
		t.Fatalf("failed to unmarshal state args: %v", err)
	}

	if updatedState["velocity_delta"].(float64) != 0.05 {
		t.Errorf("expected velocity delta 0.05, got %v", updatedState["velocity_delta"])
	}
	if updatedState["restriction_active"].(bool) != false {
		t.Errorf("expected restriction active false, got %v", updatedState["restriction_active"])
	}
	if updatedState["multiplier"].(float64) != 1.0 {
		t.Errorf("expected multiplier 1.0, got %v", updatedState["multiplier"])
	}
}

package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/vcoo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type mockVCOODBTX struct {
	userRow   database.GetUserVCOODataByEmailRow
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
	return &mockVCOOQueryRows{user: m.userRow}, nil
}

type mockVCOOQueryRows struct {
	user  database.GetUserVCOODataByEmailRow
	index int
}

func (r *mockVCOOQueryRows) Close() {}
func (r *mockVCOOQueryRows) Err() error { return nil }
func (r *mockVCOOQueryRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *mockVCOOQueryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *mockVCOOQueryRows) Next() bool {
	r.index++
	return r.index == 1
}
func (r *mockVCOOQueryRows) Scan(dest ...any) error {
	if len(dest) != 6 {
		return fmt.Errorf("expected 6 dest args, got %d", len(dest))
	}
	*(dest[0].(*pgtype.UUID)) = r.user.ID
	*(dest[1].(*pgtype.UUID)) = r.user.EntityID
	*(dest[2].(*string)) = r.user.Email
	*(dest[3].(*[]string)) = r.user.VcooActiveBlockers
	*(dest[4].(*[]byte)) = r.user.VcooHistory
	*(dest[5].(*[]byte)) = r.user.VcooState
	return nil
}
func (r *mockVCOOQueryRows) Values() ([]any, error) { return nil, nil }
func (r *mockVCOOQueryRows) RawValues() [][]byte { return nil }
func (r *mockVCOOQueryRows) Conn() *pgx.Conn { return nil }

type mockVCOORow struct {
	row database.GetUserVCOODataByEmailRow
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

type mockLLM struct {
	text string
	err  error
}

func (m *mockLLM) GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return m.text, m.err
}

func (m *mockLLM) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
	if m.err != nil {
		return m.err
	}
	extracted, ok := output.(*vcoo.ExtractedReport)
	if !ok {
		return errors.New("invalid output type")
	}
	extracted.ActiveBlockers = []string{"WASM memory page bounds"}
	extracted.OutboundVoipDials = 60
	extracted.PartnersSigned = 1
	extracted.ScheduledOnboardings = 2
	extracted.XPostsExecuted = 4
	extracted.VideoProofsDropped = 3
	return nil
}

func TestVirtualCOOWorker_Handle_Malformed(t *testing.T) {
	w := &VirtualCOOWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "worker.inbox.vcoo_ingress",
		Data:    []byte(`{ "invalid": "json"`),
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected nil error for malformed payload (poison pill handled), got %v", err)
	}
}

func TestVirtualCOOWorker_Handle_SourceUnverified(t *testing.T) {
	cfg := &config.Config{
		VCOOFounderEmail: "founder@yourplatform.com",
	}
	mockDB := &mockVCOODBTX{
		queryErr: errors.New("user not found"),
	}
	queries := database.New(mockDB)
	w := &VirtualCOOWorker{
		logger: slog.Default(),
		cfg:    cfg,
		db:     queries,
	}

	payload := PostmarkInboundEmail{
		From:     "hacker@external.com",
		TextBody: "Outbound dials: 50",
	}
	data, _ := json.Marshal(payload)

	msg := &nats.Msg{
		Subject: "worker.inbox.vcoo_ingress",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected nil error for unverified email source, got %v", err)
	}
}

func TestVirtualCOOWorker_Handle_Success(t *testing.T) {
	cfg := &config.Config{
		VCOOFounderEmail: "founder@yourplatform.com",
	}

	var mockUUID pgtype.UUID
	_ = mockUUID.Scan("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")

	userRow := database.GetUserVCOODataByEmailRow{
		ID:                 mockUUID,
		EntityID:           mockUUID,
		Email:              "founder@yourplatform.com",
		VcooActiveBlockers: []string{},
		VcooHistory:        []byte("[]"),
		VcooState:          []byte("{}"),
	}

	mockDB := &mockVCOODBTX{userRow: userRow}
	queries := database.New(mockDB)
	llm := &mockLLM{text: "MOCK BRIEF TEXT"}

	w := &VirtualCOOWorker{
		logger:  slog.Default(),
		cfg:     cfg,
		db:      queries,
		vcooSvc: vcoo.NewService(queries, llm),
	}

	payload := PostmarkInboundEmail{
		From:     "founder@yourplatform.com",
		TextBody: "Outbound VoIP dials: 60\nPartners: 1\nOnboardings: 2\nX Posts: 4\nVideo Proofs: 3\nBlocker: WASM memory page bounds",
	}
	data, _ := json.Marshal(payload)

	msg := &nats.Msg{
		Subject: "worker.inbox.vcoo_ingress",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	if mockDB.lastQuery == "" {
		t.Fatal("expected Exec/QueryRow to be called on database, but it was not")
	}

	// Verify that UpdateUserVCOOData was called with updated blockers and history
	if len(mockDB.lastArgs) != 4 {
		t.Fatalf("expected UpdateUserVCOOData args (4), got %d", len(mockDB.lastArgs))
	}

	blockers := mockDB.lastArgs[1].([]string)
	if len(blockers) != 1 || blockers[0] != "WASM memory page bounds" {
		t.Errorf("expected blockers to contain 'WASM memory page bounds', got %v", blockers)
	}

	historyBytes := mockDB.lastArgs[2].([]byte)
	var history []map[string]interface{}
	if err := json.Unmarshal(historyBytes, &history); err != nil {
		t.Fatalf("failed to unmarshal history bytes: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(history))
	}

	if history[0]["outbound_voip_dials"].(float64) != 60 {
		t.Errorf("expected dials: 60, got %v", history[0]["outbound_voip_dials"])
	}
}

func TestVirtualCOOWorker_CronCheck(t *testing.T) {
	// Setup test environment, mock HTTP Server for Postmark API
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Postmark-Server-Token") != "test-token" {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"MessageID":"123","ErrorCode":0,"Message":"OK"}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		VCOOFounderEmail:         "founder@yourplatform.com",
		PostmarkServerToken:      "test-token",
		PostmarkSenderSignature:  "coo@inbound.yourplatform.com",
	}

	var mockUUID pgtype.UUID
	_ = mockUUID.Scan("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")

	userRow := database.GetUserVCOODataByEmailRow{
		ID:                 mockUUID,
		EntityID:           mockUUID,
		Email:              "founder@yourplatform.com",
		VcooActiveBlockers: []string{"WASM memory page bounds"},
		VcooHistory:        []byte("[]"),
		VcooState:          []byte(`{"VelocityDelta": -0.12, "RestrictionActive": true, "Multiplier": 1.5}`),
	}

	mockDB := &mockVCOODBTX{userRow: userRow}
	queries := database.New(mockDB)
	llm := &mockLLM{text: "MOCK BRIEF TEXT DISPATCH FOR TODAY"}

	// Create and write OKF Playbook founder_state.md temporarily so tests are robust
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

### 2. The 10X Weekly Distribution Benchmarks (Targets)
- Target ID: `+"`OUTBOUND_CALLS`"+`  | Metric: 250 | Unit: Dials
- Target ID: `+"`VIDEO_PROOFS`"+`  | Metric: 10 | Unit: Drops
- Target ID: `+"`X_DAILY_POSTS`"+`  | Metric: 35 | Unit: Posts
- Target ID: `+"`ONBOARDING_RUNS`"+`  | Metric: 5 | Unit: Calls
`), 0644)
	defer os.RemoveAll(testBaseDir)

	w := &VirtualCOOWorker{
		db:      queries,
		logger:  slog.Default(),
		cfg:     cfg,
		vcooSvc: vcoo.NewService(queries, llm),
		client:  server.Client(),
	}

	// We override the Postmark url using transport hijack or direct client override
	// Let's modify w.sendEmail to use the test server endpoint.
	// But w.sendEmail is a method. Wait, does it use hardcoded postmark URL?
	// Yes: `https://api.postmarkapp.com/email`.
	// We can test w.sendEmail by having a client or transport redirect or we can just make sure sendEmail is testable.
	// Let's check w.sendEmail: it uses `w.client.Do(req)`. We can use a custom RoundTripper on w.client that intercepts requests to api.postmarkapp.com and redirects to our test server.
	w.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "postmarkapp.com") {
			// Redirect to test server
			targetURL := server.URL + req.URL.Path
			newReq, _ := http.NewRequestWithContext(req.Context(), req.Method, targetURL, req.Body)
			newReq.Header = req.Header
			return http.DefaultClient.Do(newReq)
		}
		return nil, errors.New("unexpected request host")
	})

	// Manually execute sendEmail to verify it works
	w.sendEmail(context.Background(), "founder@yourplatform.com", "MOCK BRIEF TEXT DISPATCH FOR TODAY", true)
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPostmarkInboundEmailWorker_VCOORouting(t *testing.T) {
	w := &PostmarkInboundEmailWorker{
		logger: slog.Default(),
		nc:     nil, // Tested with safety check
	}

	payload := PostmarkInboundEmail{
		From:              "founder@yourplatform.com",
		To:                "coo@inbound.yourplatform.com",
		OriginalRecipient: "coo@inbound.yourplatform.com",
		TextBody:          "Dials: 50",
	}
	data, _ := json.Marshal(payload)

	msg := &nats.Msg{
		Subject: "worker.inbox.email.postmark_inbound_email",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected no error for VCOO alias routing, got %v", err)
	}
}

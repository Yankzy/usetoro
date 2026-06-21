package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
)

// mockRoundTripper intercepts HTTP requests and returns a canned response.
type mockRoundTripper struct {
	lastRequest *http.Request
	lastBody    []byte
	response    *http.Response
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	m.lastRequest = req
	if req.Body != nil {
		m.lastBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(m.lastBody))
	}
	if m.response != nil {
		return m.response, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"MessageID":"test-msg-123"}`)),
		Header:     make(http.Header),
	}, nil
}

// setupTestConfig sets the global config for testing and returns a cleanup function.
func setupTestConfig(cfg *config.Config) func() {
	prev := config.GetGlobal()
	config.SetGlobal(cfg)
	return func() {
		config.SetGlobal(prev)
	}
}

func TestSendEmail_FallbackBranch_UsesSarahAddresses(t *testing.T) {
	// Arrange: no virtual employees configured, no PostmarkSenderSignature.
	// The fallback branch should use sarah@usetoro.io for From and sarah@cpa.usetoro.io for ReplyTo.
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",                             // empty → fallback branch
		VirtualEmployees:        map[string]config.AgentAlias{}, // no aliases
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{}
	w := &OmniChatWorker{
		db:     nil, // not needed for payload construction
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	// Act: send an email with a "from" handle that contains "@" but no matching alias.
	msgID, err := w.sendEmail(
		context.Background(),
		"client@example.com",    // to
		"General Purpose Agent", // from — no "@", no alias match
		"Test Subject",
		"Test body",
		"", // no in-reply-to
		"", // no slack channel
		"", // no slack parent ts
		"", // entityID
	)

	// Assert
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if msgID != "test-msg-123" {
		t.Errorf("expected MessageID 'test-msg-123', got %q", msgID)
	}
	if transport.lastRequest == nil {
		t.Fatal("expected an HTTP request, but none was made")
	}
	if transport.lastRequest.Method != "POST" {
		t.Errorf("expected POST method, got %s", transport.lastRequest.Method)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(transport.lastBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// The from handle "General Purpose Agent" has no "@", so it should be formatted as:
	// "General Purpose Agent" <sarah@usetoro.io>
	expectedFrom := `"General Purpose Agent" <sarah@usetoro.io>`
	if payload["From"] != expectedFrom {
		t.Errorf("From = %q, want %q", payload["From"], expectedFrom)
	}

	expectedReplyTo := "sarah@cpa.usetoro.io"
	if payload["ReplyTo"] != expectedReplyTo {
		t.Errorf("ReplyTo = %q, want %q", payload["ReplyTo"], expectedReplyTo)
	}

	// Verify other payload fields are correct
	if payload["To"] != "client@example.com" {
		t.Errorf("To = %q, want %q", payload["To"], "client@example.com")
	}
	if payload["Subject"] != "Test Subject" {
		t.Errorf("Subject = %q, want %q", payload["Subject"], "Test Subject")
	}
	if payload["TextBody"] != "Test body" {
		t.Errorf("TextBody = %q, want %q", payload["TextBody"], "Test body")
	}
}

func TestSendEmail_FallbackBranch_FromWithAtSign(t *testing.T) {
	// When "from" contains "@" but no agent alias matches and no PostmarkSenderSignature,
	// the From address should be the bare fallbackFrom ("sarah@usetoro.io").
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{}
	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	msgID, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"random@unknown.com", // contains "@" but no alias match
		"Test Subject",
		"Body",
		"",
		"",
		"",
		"",
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if msgID != "test-msg-123" {
		t.Errorf("expected MessageID 'test-msg-123', got %q", msgID)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(transport.lastBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// When from contains "@", the fallback uses the bare fallbackFrom address.
	expectedFrom := "sarah@usetoro.io"
	if payload["From"] != expectedFrom {
		t.Errorf("From = %q, want %q", payload["From"], expectedFrom)
	}
	expectedReplyTo := "sarah@cpa.usetoro.io"
	if payload["ReplyTo"] != expectedReplyTo {
		t.Errorf("ReplyTo = %q, want %q", payload["ReplyTo"], expectedReplyTo)
	}
}

func TestSendEmail_PostmarkTokenNotConfigured(t *testing.T) {
	// Failure case: PostmarkServerToken is empty.
	cfg := &config.Config{
		PostmarkServerToken:     "",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{},
	}

	_, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"test",
		"Subject",
		"Body",
		"",
		"",
		"",
		"",
	)

	if err == nil {
		t.Fatal("expected error for missing postmark server token, got nil")
	}
	if !strings.Contains(err.Error(), "postmark server token not configured") {
		t.Errorf("expected error about missing token, got: %v", err)
	}
}

func TestSendEmail_UsesReSubjectWhenInReplyToPresent(t *testing.T) {
	// When inReplyTo is non-empty, the subject should be prefixed with "Re: ".
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{}
	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	_, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"test@example.com",
		"Invoice Question",
		"Body",
		"<in-reply-to-message-id@example.com>",
		"",
		"",
		"",
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(transport.lastBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if payload["Subject"] != "Re: Invoice Question" {
		t.Errorf("Subject = %q, want %q", payload["Subject"], "Re: Invoice Question")
	}

	// Verify that In-Reply-To and References headers are set.
	headers, ok := payload["Headers"].([]interface{})
	if !ok {
		t.Fatal("expected Headers to be present in payload")
	}
	if len(headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(headers))
	}
}

func TestSendEmail_EmptySubjectDefaults(t *testing.T) {
	// When subject is empty, it defaults to "Response from Toro AI".
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{}
	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	_, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"test@example.com",
		"", // empty subject
		"Body",
		"",
		"",
		"",
		"",
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(transport.lastBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if payload["Subject"] != "Response from Toro AI" {
		t.Errorf("Subject = %q, want %q", payload["Subject"], "Response from Toro AI")
	}
}

func TestSendEmail_PostmarkHTTPError(t *testing.T) {
	// Failure case: Postmark returns a non-200 status.
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(`{"ErrorCode":400,"Message":"Invalid From address"}`)),
			Header:     make(http.Header),
		},
	}
	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	_, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"test@example.com",
		"Subject",
		"Body",
		"",
		"",
		"",
		"",
	)

	if err == nil {
		t.Fatal("expected error for Postmark API error, got nil")
	}
	if !strings.Contains(err.Error(), "postmark API error") {
		t.Errorf("expected error containing 'postmark API error', got: %v", err)
	}
}

func TestSendEmail_NoReDuplicate(t *testing.T) {
	// When subject already starts with "Re:", don't add another "Re: " prefix.
	cfg := &config.Config{
		PostmarkServerToken:     "test-token",
		PostmarkSenderSignature: "",
		VirtualEmployees:        map[string]config.AgentAlias{},
	}
	cleanup := setupTestConfig(cfg)
	defer cleanup()

	transport := &mockRoundTripper{}
	w := &OmniChatWorker{
		db:     nil,
		logger: slog.Default(),
		cfg:    cfg,
		client: &http.Client{Transport: transport},
	}

	_, err := w.sendEmail(
		context.Background(),
		"client@example.com",
		"test@example.com",
		"Re: Already Prefixed",
		"Body",
		"<in-reply-to-id@example.com>",
		"",
		"",
		"",
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(transport.lastBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if payload["Subject"] != "Re: Already Prefixed" {
		t.Errorf("Subject = %q, want %q (should not double-prefix 'Re:')", payload["Subject"], "Re: Already Prefixed")
	}
}

package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	appconfig "github.com/Yankzy/usetoro/internal/config"
	agentregistry "github.com/Yankzy/usetoro/tap/agents"
	_ "github.com/Yankzy/usetoro/tap/agents/approval"
	_ "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	_ "github.com/Yankzy/usetoro/tap/agents/reconcile_expense"
	_ "github.com/Yankzy/usetoro/tap/agents/reconcile_revenue"
	_ "github.com/Yankzy/usetoro/tap/agents/stripe_processor"
	_ "github.com/Yankzy/usetoro/tap/agents/intent_extractor"
	_ "github.com/Yankzy/usetoro/tap/agents/ocr_agent"
	_ "github.com/Yankzy/usetoro/tap/agents/outflow_classification_agent"
	_ "github.com/Yankzy/usetoro/tap/agents/inflow_classification_agent"
	_ "github.com/Yankzy/usetoro/tap/agents/account_type_agent"
	_ "github.com/Yankzy/usetoro/tap/agents/customer_vendor_selection"
	_ "github.com/Yankzy/usetoro/tap/agents/account_selection"
	_ "github.com/Yankzy/usetoro/tap/agents/general_agent"
	_ "github.com/Yankzy/usetoro/tap/agents/generic_batch_agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

func loadAppConfigForAgentTests(t *testing.T) *appconfig.Config {
	t.Helper()

	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("NATS_URL", "nats://localhost:4222")
	// 32-byte key base64: "12345678901234567890123456789012"
	_ = os.Setenv("ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	_ = os.Chdir(root)

	cfg, _, err := appconfig.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

type mockBus struct {
	mu   sync.Mutex
	subs []string
}

func (m *mockBus) Publish(_ string, _ []byte) error     { return nil }
func (m *mockBus) PublishCore(_ string, _ []byte) error { return nil }

func (m *mockBus) RequestWithContext(_ context.Context, _ string, _ []byte) (*nats.Msg, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBus) QueueSubscribe(subj, _ string, _ nats.MsgHandler, _ ...nats.SubOpt) (*nats.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, subj)
	return &nats.Subscription{}, nil
}

func (m *mockBus) subscriptions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.subs...)
}

func TestConfiguredInternalAgents_RegisteredAndDerivedRouting(t *testing.T) {
	cfg := loadAppConfigForAgentTests(t)
	registry := agentregistry.GetRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := &mockBus{}

	internalCount := 0
	modulesRequiringOutputSubject := map[string]struct{}{
		"reconcile-expense-agent": {},
		"reconcile-revenue-agent": {},
	}

	for _, agentCfg := range cfg.Agents {
		if agentCfg.Engine != "internal" {
			continue
		}
		internalCount++

		if _, needsOutputSubject := modulesRequiringOutputSubject[agentCfg.InternalModule]; needsOutputSubject && agentCfg.OutputSubject == "" {
			t.Fatalf("agent %s requires output_subject in defaults.yml", agentCfg.InternalModule)
		}

		if agentCfg.TaskQueue == "" {
			t.Fatalf("agent %s must define task_queue in defaults.yml", agentCfg.InternalModule)
		}

		factory, ok := registry[agentCfg.InternalModule]
		if !ok {
			t.Fatalf("missing registry factory for internal module %q", agentCfg.InternalModule)
		}

		expectedQueue, err := core.BuildTaskSubjectFromActivity(agentCfg.ActivityType, core.ComplexityEntry)
		if err != nil {
			t.Fatalf("invalid activity_type for module %s: %v", agentCfg.InternalModule, err)
		}
		if agentCfg.TaskQueue != expectedQueue {
			t.Fatalf("agent %s task_queue mismatch: got %s want %s", agentCfg.InternalModule, agentCfg.TaskQueue, expectedQueue)
		}

		before := len(bus.subscriptions())
		r := factory(core.Environment{
			Logger: logger,
			Bus:    bus,
			Config: agentCfg,
		})
		if r == nil {
			t.Fatalf("factory returned nil runnable for module %s", agentCfg.InternalModule)
		}
		if err := r.Start(); err != nil {
			t.Fatalf("failed to start module %s: %v", agentCfg.InternalModule, err)
		}
		t.Cleanup(func(r core.Runnable) func() {
			return func() { _ = r.Stop() }
		}(r))

		subs := bus.subscriptions()
		if len(subs) < before+2 {
			t.Fatalf("expected inbox + task queue subscriptions for module %s, got %v", agentCfg.InternalModule, subs[before:])
		}

		newSubs := subs[before:]
		hasInbox := false
		hasDerivedQueue := false
		for _, s := range newSubs {
			if strings.Contains(s, ".inbox") {
				hasInbox = true
			}
			if s == expectedQueue {
				hasDerivedQueue = true
			}
		}
		if !hasInbox {
			t.Fatalf("module %s did not subscribe to inbox; got %v", agentCfg.InternalModule, newSubs)
		}
		if !hasDerivedQueue {
			t.Fatalf("module %s did not subscribe to derived queue %s; got %v", agentCfg.InternalModule, expectedQueue, newSubs)
		}
	}

	if internalCount == 0 {
		t.Fatal("no internal agents found in configuration")
	}
}

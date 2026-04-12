package workers

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func loadCfg(t *testing.T) *config.Config {
	t.Helper()
	// Satisfy required config validation without needing real services.
	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("NATS_URL", "nats://localhost:4222")
	// 32-byte key base64: "12345678901234567890123456789012"
	_ = os.Setenv("ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	// Ensure working directory is repo root so config paths resolve.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	_ = os.Chdir(root)

	cfg, _, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

func TestCSVMappingSubscriptions_Derived(t *testing.T) {
	cfg := loadCfg(t)
	w := &CSVMappingWorker{cfg: cfg, logger: testLogger()}

	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	expectedSubject, _ := core.BuildWorkerInboxFromActivity(cfg.Workers.CSVMappingActivityType)
	if subs[0].Subject != expectedSubject {
		t.Fatalf("subject mismatch: got %s want %s", subs[0].Subject, expectedSubject)
	}
	expectedGroup := deriveGroup(expectedSubject)
	if subs[0].Group != expectedGroup {
		t.Fatalf("group mismatch: got %s want %s", subs[0].Group, expectedGroup)
	}
}

func TestCSVMappingSubscriptions_ConfigOverride(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.CSVMapping = "worker.inbox.custom"
	cfg.Workers.CSVMappingGroup = "custom-group"
	w := &CSVMappingWorker{cfg: cfg, logger: testLogger()}

	subs := w.Subscriptions()
	if subs[0].Subject != "worker.inbox.custom" {
		t.Fatalf("override subject mismatch: got %s", subs[0].Subject)
	}
	if subs[0].Group != "custom-group" {
		t.Fatalf("override group mismatch: got %s", subs[0].Group)
	}
}

func TestEnrichmentSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.Enrichment = "proof.custom.topic"
	cfg.Workers.EnrichmentGroup = "custom-enrich-group"
	w := &EnrichmentWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "proof.custom.topic" || subs[0].Group != "custom-enrich-group" {
		t.Fatalf("enrichment override mismatch: %+v", subs[0])
	}
}

func TestTransactionSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.Transaction = "ledger.custom.*"
	cfg.Workers.TransactionGroup = "tx-group"
	w := &TransactionWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "ledger.custom.*" || subs[0].Group != "tx-group" {
		t.Fatalf("transaction override mismatch: %+v", subs[0])
	}
}

func TestAttachableSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.Attachable = "ledger.attachables.custom"
	cfg.Workers.AttachableGroup = "attach-group"
	w := &AttachableWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "ledger.attachables.custom" || subs[0].Group != "attach-group" {
		t.Fatalf("attachable override mismatch: %+v", subs[0])
	}
}

func TestFignodeSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.Fignode = "proof.custom.reconcile.>"
	cfg.Workers.FignodeGroup = "fignode-group"
	w := &FignodePublisherWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "proof.custom.reconcile.>" || subs[0].Group != "fignode-group" {
		t.Fatalf("fignode override mismatch: %+v", subs[0])
	}
}

func TestVectorSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	cfg.Workers.Vector = []string{"ledger.one.*"}
	cfg.Workers.VectorGroupPrefix = "vec"
	w := &VectorSyncWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 vector subscription, got %d", len(subs))
	}
	if subs[0].Subject != "ledger.one.*" {
		t.Fatalf("vector subject mismatch: %s", subs[0].Subject)
	}
	expectedGroup := "vec-" + strings.TrimSuffix(groupFromSubject("ledger.one.*"), "-group")
	if subs[0].Group != expectedGroup {
		t.Fatalf("vector group mismatch: got %s want %s", subs[0].Group, expectedGroup)
	}
}

func deriveGroup(subject string) string {
	return groupFromSubject(subject)
}

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
	expectedSubject, _ := core.BuildWorkerInboxFromActivity(cfg.Workers.Get(config.WorkerKeyFromType(w)).ActivityType)
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
	setWorkerConfig(cfg, config.WorkerKeyFromType(&CSVMappingWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "worker.inbox.custom"
		workerCfg.Group = "custom-group"
	})
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
	setWorkerConfig(cfg, config.WorkerKeyFromType(&EnrichmentWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "proof.custom.topic"
		workerCfg.Group = "custom-enrich-group"
	})
	w := &EnrichmentWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "proof.custom.topic" || subs[0].Group != "custom-enrich-group" {
		t.Fatalf("enrichment override mismatch: %+v", subs[0])
	}
}

func TestEnrichmentSubscriptions_Derived(t *testing.T) {
	cfg := loadCfg(t)
	w := &EnrichmentWorker{cfg: cfg, logger: testLogger()}

	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}

	expectedSubject, _ := core.BuildWorkerInboxFromActivity(cfg.Workers.Get(config.WorkerKeyFromType(w)).ActivityType)
	if subs[0].Subject != expectedSubject {
		t.Fatalf("subject mismatch: got %s want %s", subs[0].Subject, expectedSubject)
	}

	expectedGroup := deriveGroup(expectedSubject)
	if subs[0].Group != expectedGroup {
		t.Fatalf("group mismatch: got %s want %s", subs[0].Group, expectedGroup)
	}
}

func TestERPEventSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&ERPEventWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "toro.erp.events.custom"
		workerCfg.Group = "erp-custom-group"
	})
	w := &ERPEventWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "toro.erp.events.custom" || subs[0].Group != "erp-custom-group" {
		t.Fatalf("erp event override mismatch: %+v", subs[0])
	}
}

func TestTransactionSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&TransactionWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "ledger.custom.*"
		workerCfg.Group = "tx-group"
	})
	w := &TransactionWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "ledger.custom.*" || subs[0].Group != "tx-group" {
		t.Fatalf("transaction override mismatch: %+v", subs[0])
	}
}

func TestAttachableSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&AttachableWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "ledger.attachables.custom"
		workerCfg.Group = "attach-group"
	})
	w := &AttachableWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "ledger.attachables.custom" || subs[0].Group != "attach-group" {
		t.Fatalf("attachable override mismatch: %+v", subs[0])
	}
}

func TestFignodeSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&FignodePublisherWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "proof.custom.reconcile.>"
		workerCfg.Group = "fignode-group"
	})
	w := &FignodePublisherWorker{cfg: cfg, logger: testLogger()}
	subs := w.Subscriptions()
	if subs[0].Subject != "proof.custom.reconcile.>" || subs[0].Group != "fignode-group" {
		t.Fatalf("fignode override mismatch: %+v", subs[0])
	}
}

func TestVectorSubscriptions(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&VectorSyncWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subjects = []string{"ledger.one.*"}
		workerCfg.GroupPrefix = "vec"
	})
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

func TestWebhooksSubscriptions_Derived(t *testing.T) {
	cfg := loadCfg(t)
	w := &WebhooksWorker{cfg: cfg, logger: testLogger()}

	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}

	expectedSubject, _ := core.BuildWorkerInboxFromActivity(cfg.Workers.Get(config.WorkerKeyFromType(w)).ActivityType)
	if subs[0].Subject != expectedSubject {
		t.Fatalf("subject mismatch: got %s want %s", subs[0].Subject, expectedSubject)
	}
}

func TestWebhooksSubscriptions_ConfigOverride(t *testing.T) {
	cfg := loadCfg(t)
	setWorkerConfig(cfg, config.WorkerKeyFromType(&WebhooksWorker{}), func(workerCfg *config.WorkerSubjectConfig) {
		workerCfg.Subject = "worker.inbox.webhooks.custom"
		workerCfg.Group = "webhooks-custom-group"
	})
	w := &WebhooksWorker{cfg: cfg, logger: testLogger()}

	subs := w.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Subject != "worker.inbox.webhooks.custom" {
		t.Fatalf("webhooks override subject mismatch: got %s", subs[0].Subject)
	}
	if subs[0].Group != "webhooks-custom-group" {
		t.Fatalf("webhooks override group mismatch: got %s", subs[0].Group)
	}
}

func TestDefaultWorkerConfigs_CoverAllWorkers(t *testing.T) {
	cfg := loadCfg(t)

	cases := []struct {
		name        string
		worker      Worker
		expectedKey string
	}{
		{name: "csv mapping", worker: &CSVMappingWorker{cfg: cfg, logger: testLogger()}, expectedKey: "csv_mapping"},
		{name: "enrichment", worker: &EnrichmentWorker{cfg: cfg, logger: testLogger()}, expectedKey: "enrichment"},
		{name: "erp event", worker: &ERPEventWorker{cfg: cfg, logger: testLogger()}, expectedKey: "erp_event"},
		{name: "transaction", worker: &TransactionWorker{cfg: cfg, logger: testLogger()}, expectedKey: "transaction"},
		{name: "attachable", worker: &AttachableWorker{cfg: cfg, logger: testLogger()}, expectedKey: "attachable"},
		{name: "fignode", worker: &FignodePublisherWorker{cfg: cfg, logger: testLogger()}, expectedKey: "fignode_publisher"},
		{name: "webhooks", worker: &WebhooksWorker{cfg: cfg, logger: testLogger()}, expectedKey: "webhooks"},
		{name: "vector", worker: &VectorSyncWorker{cfg: cfg, logger: testLogger()}, expectedKey: "vector_sync"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := config.WorkerKeyFromType(tc.worker)
			if key != tc.expectedKey {
				t.Fatalf("worker key mismatch: got %s want %s", key, tc.expectedKey)
			}

			workerCfg := cfg.Workers.Get(key)
			if isEmptyWorkerConfig(workerCfg) {
				t.Fatalf("missing config entry for worker key %s", key)
			}

			subs := tc.worker.Subscriptions()
			if len(subs) == 0 {
				t.Fatalf("expected at least one subscription for worker %s", tc.name)
			}
		})
	}
}

func deriveGroup(subject string) string {
	return groupFromSubject(subject)
}

func setWorkerConfig(cfg *config.Config, workerName string, mutate func(*config.WorkerSubjectConfig)) {
	workerCfg := cfg.Workers.Get(workerName)
	mutate(&workerCfg)
	cfg.Workers[workerName] = workerCfg
}

func isEmptyWorkerConfig(cfg config.WorkerSubjectConfig) bool {
	return cfg.ActivityType == "" &&
		cfg.Subject == "" &&
		len(cfg.Subjects) == 0 &&
		cfg.Group == "" &&
		cfg.GroupPrefix == ""
}

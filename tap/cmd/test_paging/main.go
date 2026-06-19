package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

type mockBus struct{}

func (b *mockBus) Publish(subject string, data []byte) error {
	fmt.Printf("[NATS MockBus] Publish to %s\n", subject)
	return nil
}
func (b *mockBus) PublishCore(subject string, data []byte) error { return nil }
func (b *mockBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return nil, nil
}
func (b *mockBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func printMemStats(prefix string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("[%s] Alloc = %v KiB, TotalAlloc = %v KiB, HeapAlloc = %v KiB, NumGC = %v\n",
		prefix, m.Alloc/1024, m.TotalAlloc/1024, m.HeapAlloc/1024, m.NumGC)
}

func main() {
	fmt.Println("[Stage 1] Initializing Logging and Environment...")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("ERROR: OPENAI_API_KEY is not set. The LLM call will fail. Please set it in your environment.")
		os.Exit(1)
	}

	// Dynamic Tenant UUID representing the tenant
	tenantUUID := "550e8400-e29b-41d4-a716-446655440000"
	fmt.Printf("[Stage 2] Setting up OKF Knowledge Directory for Tenant: %s...\n", tenantUUID)

	testBaseDir := filepath.Clean(filepath.Join("docs", "knowledge", tenantUUID))
	err := os.MkdirAll(testBaseDir, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating base dir: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		fmt.Println("[Stage 5] Cleaning up test directory structures on disk...")
		// _ = os.RemoveAll(filepath.Join("docs", "knowledge", tenantUUID))
	}()

	conceptPath := "playbooks/financials.md"
	fullTestPath := filepath.Clean(filepath.Join(testBaseDir, conceptPath))
	err = os.MkdirAll(filepath.Dir(fullTestPath), 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating concept subdirs: %v\n", err)
		os.Exit(1)
	}

	// Large sample knowledge file content to observe memory changes
	content := `---
type: Reference
title: Q1 Revenue Figures
description: Financial records showing Q1 transactions
resource: file:///docs/knowledge/550e8400-e29b-41d4-a716-446655440000/playbooks/financials.md
---
Transaction ID: TXN-001, Amount: $1000.00, Description: Office Equipment
Transaction ID: TXN-002, Amount: $500.00, Description: Software Subscription
Transaction ID: TXN-003, Amount: $120.00, Description: Coffee supplies
`
	err = os.WriteFile(fullTestPath, []byte(content), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing OKF markdown document: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stage 2] Successfully wrote OKF file to: %s\n", fullTestPath)

	cfg := core.AgentConfig{
		DID:          "test-agent-did",
		Name:         "Test Paging Agent",
		Model:        "gpt-4o",
		SystemPrompt: "You are a helpful financial assistant. You MUST page in documents to answer questions.",
	}

	r := agent.NewRuntime(logger, &mockBus{}, cfg)

	ctx := agent.WithTenantID(context.Background(), tenantUUID)

	fmt.Println("[Stage 3] Preparing Paging Context directory maps by scanning OKF catalog...")
	pages, err := agent.ScanOKFCatalog(ctx, tenantUUID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning OKF catalog: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Stage 3] Successfully scanned and loaded %d pages from OKF catalog!\n", len(pages))

	// Clean fetcher without file deletion
	fetcher := func(ctx context.Context, uuid string) (string, error) {
		fmt.Printf("\n[Fetcher Stage] Fetcher callback triggered for UUID/Path: %s\n", uuid)

		docContent, err := agent.OKFDocumentFetcher(ctx, uuid)
		if err != nil {
			return "", fmt.Errorf("OKFDocumentFetcher failed: %w", err)
		}
		fmt.Printf("[Fetcher Stage] OKFDocumentFetcher read %d bytes successfully!\n", len(docContent))
		return docContent, nil
	}

	prompt := "Please read the financial records for Q1 (local_ref 1) using the PAGE_IN tool, sum up the total amount of transactions, and list what they were."

	fmt.Println("\n[Memory Stats] Before ExecWithPaging:")
	printMemStats("Pre-Execution")

	fmt.Println("\n[Stage 4] Launching ExecWithPaging loop...")
	result, err := r.ExecWithPaging(ctx, prompt, "", pages, fetcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing loop: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n[Memory Stats] Immediately After ExecWithPaging (Before manual GC):")
	printMemStats("Post-Execution")

	// Trigger manual Garbage Collection to collect the paged message history and context map
	fmt.Println("\n[Stage 5] Triggering Garbage Collection manually...")
	runtime.GC()

	fmt.Println("[Memory Stats] After Manual GC:")
	printMemStats("Post-GC")

	fmt.Printf("\n--- Final Result from LLM ---\n%s\n", result)
}

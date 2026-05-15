package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

type mockBus struct{}

func (b *mockBus) Publish(subject string, data []byte) error {
	fmt.Printf("MockBus: Publish to %s\n", subject)
	return nil
}
func (b *mockBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return nil, nil
}
func (b *mockBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("WARNING: OPENAI_API_KEY is not set. The LLM call will likely fail.")
	}

	cfg := core.AgentConfig{
		DID:          "test-agent-did",
		Name:         "Test Paging Agent",
		Model:        "gpt-4o",
		SystemPrompt: "You are a helpful financial assistant.",
	}

	runtime := agent.NewRuntime(logger, &mockBus{}, cfg)

	pages := []agent.PageContext{
		{Type: "CSV", Summary: "Financial records for Q1", UUID: "doc-123"},
		{Type: "Note", Summary: "Meeting minutes from Jan", UUID: "doc-456"},
	}

	fetcher := func(ctx context.Context, uuid string) (string, error) {
		fmt.Printf("Fetcher: Fetching document %s\n", uuid)
		if uuid == "doc-123" {
			return "Transaction ID: 001, Amount: $100, Description: Office Supplies\nTransaction ID: 002, Amount: $50, Description: Coffee", nil
		}
		if uuid == "doc-456" {
			return "Attendees: Alice, Bob. Discussed budget for Q2. Decided to increase coffee budget.", nil
		}
		return "", fmt.Errorf("document not found")
	}

	ctx := context.Background()
	prompt := "Please read the financial records for Q1 (local_ref 1) and tell me the total amount of transactions mentioned."

	fmt.Println("--- Executing ExecWithPaging ---")
	result, err := runtime.ExecWithPaging(ctx, prompt, "", pages, fetcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n--- Final Result ---\n%s\n", result)
}

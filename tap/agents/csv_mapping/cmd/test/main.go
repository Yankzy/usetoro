package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func loadEnvFile(filepath string) {
	f, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"' `)
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

func main() {
	// 0. Quietly load environment
	envPaths := []string{"container/.env", "../container/.env", "../../container/.env", "../../../container/.env", "../../../../container/.env", "../../../../../container/.env"}
	for _, p := range envPaths {
		loadEnvFile(p)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := core.AgentConfig{
		DID:          "did:toro:test-mapper",
		Model:        "gpt-4o",
		SystemPrompt: `Respond ONLY with JSON matching the LLMColumnMapping schema. Use precise accounting language: refer to cash flow directions as "Cash Inflow" and "Cash Outflow", never as "Income" or "Expense". Flag ambiguity if inflow vs outflow cannot be distinguished.`,
	}

	rows := [][]string{
		{"Date", "Trans Description", "Amount", "Balance"},
		{"2025-01-01", "ABC TRUCKING", "1000.00", "5000.00"},
		{"2025-01-02", "CLIENT PAYMENT", "2500.00", "7500.00"},
		{"2025-01-03", "FUEL STATION", "150.00", "7350.00"},
	}

	rt := agent.NewRuntime(logger, nil, cfg)
	ctx := context.Background()

	prompt := csvmapping.BuildUserPrompt(rows)
	respText, err := rt.ExecWithPaging(ctx, prompt, "", nil, nil)
	if err != nil {
		os.Exit(1)
	}

	// The ONLY output is the raw response
	fmt.Println(respText)
}

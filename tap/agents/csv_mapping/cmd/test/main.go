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
		ActivityType: "agents.accounting.map_csv",
		Model:        "gpt-4o",
	}

	rows := [][]string{
		{"Date", "Trans Description", "Amount", "Balance"},
		{"2025-01-01", "ABC TRUCKING", "-1000.00", "5000.00"},
		{"2025-01-02", "CLIENT PAYMENT", "2500.00", "7500.00"},
		{"2025-01-03", "FUEL STATION", "-150.00", "7350.00"},
	}

	// Instantiate the actual agent
	a := csvmapping.CSVMappingAgent{}
	a.BaseAgent = agent.NewBaseAgent(logger, nil, cfg, nil)
	a.RT = agent.NewRuntime(logger, nil, cfg)

	ctx := context.Background()

	// Use the real system prompt from the workflow YAML
	sysPrompt := `You are an expert data analyst parsing raw CSV headers. Analyze the columns and map them strictly according to the provided schema below. Do not assume any fields outside of this schema. 
RFC 6902 Compliance: You MUST reply with a JSON array of RFC 6902 patches.

Example format:
[
  {"op": "add", "path": "/columns_mapped", "value": { "date_col_idx": 0, "description_col_idx": 1, ... }},
  {"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"},
  {"op": "add", "path": "/is_ambiguous", "value": false},
  {"op": "add", "path": "/ambiguity_reason", "value": null},
  {"op": "add", "path": "/polarity_sign", "value": "minus"},
  {"op": "add", "path": "/route", "value": 0}
]

The "value" inside the "/columns_mapped" patch must strictly conform to this schema:
{
  "date_col_idx": <int>,
  "description_col_idx": <int>,
  "amount_col_idx": <int or null>,
  "is_split_amount": <bool>,
  "debit_col_idx": <int or null>,
  "credit_col_idx": <int or null>,
  "vendor_col_idx": <int or null>,
  "customer_col_idx": <int or null>,
  "custom_col_idx": <int or null>,
  "confidence_score": <float 0 to 1>,
  "is_ambiguous": <bool>,
  "ambiguity_reason": <string or null>,
}

SPLIT AMOUNTS: If the statement uses separate Debit and Credit columns, set is_split_amount to true, provide debit_col_idx and credit_col_idx, and set amount_col_idx to null.
NULL HANDLING: For any optional field that is NOT present, you MUST set its index to null. Do NOT use 0 as a placeholder.

AMBIGUITY (Polarity): The data is ambiguous if you cannot definitively determine the cash direction (Inflow vs Outflow). This happens when there is only a single 'Amount' column with NO polarity indicators (no signs '-' or parentheses '()'). If this occurs, set is_ambiguous to true and provide an ambiguity_reason using precise language (e.g., 'cannot distinguish Cash Inflow from Outflow').
NOTE: Do not try to infer polarity from the descriptions, like 'Uber is always expense' fallacy. This may be an incorrect assumption. Plus the input data is just a sample to help you map the columns, it may not be representative of the entire dataset.
Use our precise internal language (not accounting terms): refer to cash flow directions as "Cash Inflow" and "Cash Outflow", never as "Income" or "Expense". Flag ambiguity if inflow vs outflow cannot be distinguished.
`

	task := core.TaskDefinition{
		SystemPrompt: sysPrompt,
	}

	fmt.Println("Running actual agent MapColumnsUsingLLM...")
	patches, err := a.MapColumnsUsingLLM(ctx, task, rows)
	if err != nil {
		fmt.Printf("Error running agent: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully extracted RFC 6902 patches!")
	for i, p := range patches {
		fmt.Printf("Patch %d: %s\n", i, string(p))
	}
}

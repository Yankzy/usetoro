# AI Column Mapping Tester (Go Implementation Plan)

We will build a centralized `LLMClient` in Go to handle OpenAI ChatCompletions, ensuring AI logic isn't scattered. We will then build a CLI testing tool to rapidly prototype the prompt.

## 1. Create `internal/services/ai/llm_client.go`
This will house the generic `go-openai` execution logic for JSON-structured outputs.

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

type LLMClient struct {
	client *openai.Client
	model  string
}

func NewLLMClient(apiKey, model string) (*LLMClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	// Fallback to gpt-5.4 if no model specified for fast/cheap testing
	if model == "" {
		model = string(shared.ChatModelGPT5_4)
	}
	return &LLMClient{
		client: openai.NewClient(apiKey),
		model:  model,
	}, nil
}

// GenerateJSON struct requires a system prompt, a user prompt, and an output pointer (struct)
func (c *LLMClient) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
	req := openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
		Temperature: 0.1,
	}

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return fmt.Errorf("llm completion error: %w", err)
	}

	rawJSON := resp.Choices[0].Message.Content
	if err := json.Unmarshal([]byte(rawJSON), output); err != nil {
		return fmt.Errorf("failed to parse LLM JSON response: %w\nRaw: %s", err, rawJSON)
	}

	return nil
}
```

## 2. Create the Tester CLI (`cmd/test_mapper/main.go`)
This standalone script will load a sample CSV, build the prompt, use the new `LLMClient`, and print the mapped JSON result to the terminal.

```go
package main

import (
    "context"
    "fmt"
    // ... imports for csv, json, os, ai package ...
)

// The Strict Schema Expected from the LLM
type ColumnMapping struct {
	DateColIdx        *int    `json:"date_col_idx"`
	DescriptionColIdx *int    `json:"description_col_idx"`
	AmountColIdx      *int    `json:"amount_col_idx"`
	IsSplitAmount     bool    `json:"is_split_amount"`
	DebitColIdx       *int    `json:"debit_col_idx"`
	CreditColIdx      *int    `json:"credit_col_idx"`
	VendorColIdx      *int    `json:"vendor_col_idx"`
	ConfidenceScore   float64 `json:"confidence_score"`
	Reasoning         string  `json:"reasoning"`
}

func main() {
    // 1. Parse Args (CSV file path)
    // 2. Extract first 3 rows of CSV
    // 3. Initialize ai.NewLLMClient(os.Getenv("OPENAI_API_KEY"), "")
    // 4. Construct Prompt with the CSV JSON exactly like we would in Python
    // 5. Call llmClient.GenerateJSON(...)
    // 6. Pretty-print the resulting ColumnMapping struct to stdout!
}
```

Does this architecture look good? By placing `llm_client.go` next to `embedder.go` and `coa_mapper.go` in `internal/services/ai`, all our OpenAI usage remains centralized!

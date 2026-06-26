package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Prompts map[string]string `yaml:"prompts"`
}

type TestCase struct {
	Name        string
	PromptKey   string
	Description string
	Amount      string
	Context     string
	Expected    string
}

func loadPrompts() (map[string]string, error) {
	b, err := os.ReadFile("../../internal/erp/ase/dags/ase_gaap_us.yml")
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	promptsRaw, ok := raw["prompts"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing prompts key")
	}

	prompts := make(map[string]string)
	for k, v := range promptsRaw {
		prompts[k] = fmt.Sprintf("%v", v)
	}
	return prompts, nil
}

func callGemini(apiKey, systemInstruction, userMessage string) (string, error) {
	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=" + apiKey

	payload := map[string]interface{}{
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": systemInstruction},
			},
		},
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": userMessage},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
		},
	}

	b, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gemini error: %s", string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	json.Unmarshal(body, &result)

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("no response text")
}

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY environment variable is not set")
	}

	prompts, err := loadPrompts()
	if err != nil {
		log.Fatalf("Failed to load prompts: %v", err)
	}

	tests := []TestCase{
		{
			Name:        "Expense < $75",
			PromptKey:   "expense_compliance_specialist",
			Description: "Office Supplies",
			Amount:      "-65.00",
			Context:     "Bought pens from Staples.",
			Expected:    "COMPLIANT_EXPENSE",
		},
		{
			Name:        "Expense > $75 Missing Receipt",
			PromptKey:   "expense_compliance_specialist",
			Description: "Software License",
			Amount:      "-85.00",
			Context:     "Paid for monthly software.",
			Expected:    "HOLD_MISSING_RECEIPT",
		},
		{
			Name:        "Meals & Entertainment Missing Attendees",
			PromptKey:   "expense_compliance_specialist",
			Description: "Dinner at Steakhouse",
			Amount:      "-150.00",
			Context:     "Dinner with clients.",
			Expected:    "HOLD_MISSING_MEAL_DOCUMENTATION",
		},
		{
			Name:        "Charity Missing Acknowledgement",
			PromptKey:   "other_expense_compliance_specialist",
			Description: "Donation to Red Cross",
			Amount:      "-300.00",
			Context:     "Annual donation.",
			Expected:    "HOLD_MISSING_CHARITY_ACKNOWLEDGEMENT",
		},
		{
			Name:        "Contractor Payment >= $600",
			PromptKey:   "universal_outflow_compliance_specialist",
			Description: "Consulting Services",
			Amount:      "-650.00",
			Context:     "Paid John Doe for consulting.",
			Expected:    "HOLD_W9_REQUIRED",
		},
	}

	fmt.Println("Starting LLM Prompt Evaluation Tests...")
	fmt.Println("---------------------------------------")

	for _, tc := range tests {
		fmt.Printf("Running Test: %s\\n", tc.Name)
		fmt.Printf("Running Test: %s\n", tc.Name)
		promptTemplate, ok := prompts[tc.PromptKey]
		if !ok {
			fmt.Printf("  ❌ Prompt key '%s' not found\n", tc.PromptKey)
			continue
		}

		userMessage := fmt.Sprintf("```json\n{\"id\": \"test_row\", \"description\": \"%s\", \"amount\": \"%s\", \"context\": \"%s\"}\n```", tc.Description, tc.Amount, tc.Context)

		result, err := callGemini(apiKey, promptTemplate, userMessage)
		if err != nil {
			fmt.Printf("  ❌ Error calling Gemini: %v\n", err)
			continue
		}

		// Simple assertion
		if strings.Contains(result, tc.Expected) {
			fmt.Printf("  ✅ PASS (LLM returned expected routing: %s)\\n", tc.Expected)
		} else {
			fmt.Printf("  ❌ FAIL (Expected %s but LLM returned: %s)\\n", tc.Expected, result)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/services/mailpool"
)

func main() {
	fmt.Println("Loading system configuration...")
	cfg, _, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if cfg.MailpoolAPIKey == "" {
		log.Fatalf("MailpoolAPIKey is not set in configuration")
	}

	endpoint := cfg.MailpoolEndpoint
	if endpoint == "" {
		endpoint = "https://app.mailpool.io/v1/api"
	}

	// 1. Initialize the Mailpool client with the base URL and custom headers
	client, err := mailpool.NewClientWithResponses(
		endpoint,
		mailpool.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Accept", "application/json")
			// Use the API key from configuration
			req.Header.Set("X-Api-Authorization", cfg.MailpoolAPIKey)
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	// 2. Prepare the query parameters matching ?limit=5&offset=0
	params := &mailpool.GetDomainsParams{
		Limit:  5,
		Offset: 0,
	}

	fmt.Println("Making request to Mailpool API...")

	// 3. Make the API request
	resp, err := client.GetDomains(context.Background(), params)
	if err != nil {
		log.Fatalf("failed to call GetDomains: %v", err)
	}
	defer resp.Body.Close()

	// 4. Handle the raw response to bypass OpenAPI spec mismatch
	// (The generated struct expects DomainOwner.Id to be a string, but the API returns a number)
	if resp.StatusCode == http.StatusOK {
		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.Fatalf("failed to decode response: %v", err)
		}

		total := result["total"]
		fmt.Printf("Total results: %v\n", total)
		fmt.Printf("Result: %v\n", result)

		if data, ok := result["data"].([]interface{}); ok {
			for _, item := range data {
				domain := item.(map[string]interface{})
				fmt.Printf("Found Domain: %s\n", domain["domain"])
			}
		}
	} else {
		fmt.Printf("Failed with status code: %d\n", resp.StatusCode)
	}
}

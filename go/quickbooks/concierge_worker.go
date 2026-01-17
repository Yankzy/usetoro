package quickbooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
)

// ConciergeWorker consumes invoice tasks from Redis and processes them
type ConciergeWorker struct {
	RedisClient *redis.Client
	// DB, Config, etc.
}

type InvoiceTask struct {
	CustomerEmail string  `json:"customer_email"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
}

func NewConciergeWorker(rdb *redis.Client) *ConciergeWorker {
	return &ConciergeWorker{RedisClient: rdb}
}

func (w *ConciergeWorker) Start(ctx context.Context) {
	queueName := "toro:quickbooks:write"
	log.Printf("Starting Concierge Worker on queue: %s", queueName)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// BLPOP with timeout
			result, err := w.RedisClient.BLPop(ctx, 5*time.Second, queueName).Result()
			if err != nil {
				if err != redis.Nil {
					// Timeout is normal
					continue
				} else if err != context.Canceled {
					log.Printf("Redis error: %v", err)
					time.Sleep(1 * time.Second)
				}
				continue
			}

			// result[0] is key, result[1] is value
			payload := result[1]
			w.processTask(payload)
		}
	}
}

func (w *ConciergeWorker) processTask(payload string) {
	var task InvoiceTask
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		log.Printf("Invalid task payload: %v", err)
		return
	}

	log.Printf("Processing invoice for %s: %.2f %s", task.CustomerEmail, task.Amount, task.Currency)

	// Workflow:
	// 1. Get Access Token (mocked for now)
	accessToken := "MOCKED_ACCESS_TOKEN"
	realmID := "MOCKED_REALM_ID"

	// 2. Check/Create Customer
	customerID, err := w.ensureCustomer(realmID, accessToken, task.CustomerEmail)
	if err != nil {
		log.Printf("Error ensuring customer: %v", err)
		// Should retry if 401
		return
	}

	// 3. Create Invoice
	err = w.createInvoice(realmID, accessToken, customerID, task.Amount, task.Currency)
	if err != nil {
		log.Printf("Error creating invoice: %v", err)
		// Handle retries
	} else {
		log.Printf("Invoice created successfully for %s", task.CustomerEmail)
	}
}

func (w *ConciergeWorker) ensureCustomer(realmID, token, email string) (string, error) {
	// Query QBO API: select * from Customer Where PrimaryEmailAddr = 'email'
	// If exists, return Id.
	// Else, POST /v3/company/:realmID/customer
	// Return new Id.

	// Mock implementation
	return "1", nil
}

func (w *ConciergeWorker) createInvoice(realmID, token, customerID string, amount float64, currency string) error {
	url := fmt.Sprintf("https://quickbooks.api.intuit.com/v3/company/%s/invoice", realmID)

	invoicePayload := map[string]interface{}{
		"Line": []map[string]interface{}{
			{
				"Amount":     amount,
				"DetailType": "SalesItemLineDetail",
				"SalesItemLineDetail": map[string]interface{}{
					"ItemRef": map[string]string{"value": "1"}, // Using a default Item ID '1' for simplicity (Services)
				},
			},
		},
		"CustomerRef": map[string]string{"value": customerID},
	}

	body, _ := json.Marshal(invoicePayload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// In real app, do request and check 401 for Retry logic
	// For scaffolding, we just log
	log.Printf("POSTing Invoice to %s", url)
	return nil
}

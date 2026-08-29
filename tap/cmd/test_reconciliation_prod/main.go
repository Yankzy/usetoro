package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Response Envelope
type WorkerResponseEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// OptimizerResponse structures
type BankAllocation struct {
	BankItemID  string `json:"bank_item_id"`
	AmountUnits string `json:"amount_units"`
}

type BookAllocation struct {
	BookItemID  string `json:"book_item_id"`
	AmountUnits string `json:"amount_units"`
}

type SelectedHypothesis struct {
	HypothesisID    string           `json:"hypothesis_id"`
	Utility         float64          `json:"utility"`
	Reason          string           `json:"reason"`
	BankAllocations []BankAllocation `json:"bank_allocations"`
	BookAllocations []BookAllocation `json:"book_allocations"`
}

type RejectedHypothesis struct {
	HypothesisID string `json:"hypothesis_id"`
	Reason       string `json:"reason"`
}

type UnresolvedBankItem struct {
	BankItemID           string   `json:"bank_item_id"`
	Reason               string   `json:"reason"`
	CandidatesConsidered []string `json:"candidates_considered,omitempty"`
}

type BookResidual struct {
	BookItemID           string `json:"book_item_id"`
	StartingAmountUnits  string `json:"starting_amount_units"`
	ConsumedAmountUnits  string `json:"consumed_amount_units"`
	RemainingAmountUnits string `json:"remaining_amount_units"`
}

type OptimizerResponse struct {
	Status              string               `json:"status"`
	ObjectiveValue      *int64               `json:"objective_value,omitempty"`
	SelectedHypotheses  []SelectedHypothesis `json:"selected_hypotheses"`
	RejectedHypotheses  []RejectedHypothesis `json:"rejected_hypotheses"`
	UnresolvedBankItems []UnresolvedBankItem `json:"unresolved_bank_items"`
	BookResiduals       []BookResidual       `json:"book_residuals"`
	Diagnostics         []string             `json:"diagnostics,omitempty"`
	SolverStats         map[string]any       `json:"solver_stats,omitempty"`
}

// formatUnits converts 10,000 units to 1.0000 currency format
func formatUnits(unitsStr string, currency string) string {
	units, err := strconv.ParseInt(unitsStr, 10, 64)
	if err != nil {
		return fmt.Sprintf("%s %s", unitsStr, currency)
	}
	major := float64(units) / 10000.0
	return fmt.Sprintf("%.2f %s (%s units)", major, currency, unitsStr)
}

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	fmt.Println("================================================================")
	fmt.Println("🚀 TORO RECONCILIATION PROD TEST RUNNER")
	fmt.Println("================================================================")
	fmt.Printf("Connecting to NATS at %s...\n", natsURL)
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	currency := "MAD"
	accountID := "test_account_001"

	// Define the test reconciliation request payload
	payload := map[string]interface{}{
		"account_id":    accountID,
		"currency":      currency,
		"evidence_text": "Bank statement line matches posted invoice book item INV-100 directly.",
		"bank_items": []map[string]interface{}{
			{
				"id":           "bank_1",
				"source_type":  "BANK_STATEMENT_LINE",
				"date":         "2026-08-01",
				"amount_units": "1000000", // 100.00 MAD (10000 units = 1 MAD)
				"direction":    "BANK_INFLOW",
				"currency":     currency,
				"description":  "PAYMENT FOR INV-100",
				"reference":    "INV-100",
			},
		},
		"book_items": []map[string]interface{}{
			{
				"id":                     "book_1",
				"source_type":            "POSTED_BOOK_ITEM",
				"origin_period":          "2026-08",
				"date":                   "2026-08-01",
				"remaining_amount_units": "1000000", // 100.00 MAD
				"direction":              "BOOK_BANK_DEBIT",  // Inflow to bank = Debit in book ledger
				"currency":               currency,
				"counterparty_id":        "CLIENT_A",
				"reference":              "INV-100",
				"provenance_refs":        []string{},
			},
		},
	}

	subject := "worker.inbox.reconciliation"
	replyInbox := nats.NewInbox()

	payload["final_destination_subject"] = replyInbox
	payload["reply_subject"] = replyInbox
	payload["reply_to"] = replyInbox

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("Failed to marshal payload: %v", err)
	}

	envelope := map[string]interface{}{
		"final_destination_subject": replyInbox,
		"reply_subject":             replyInbox,
		"reply_to":                  replyInbox,
		"payload":                   string(payloadBytes),
	}

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		log.Fatalf("Failed to marshal envelope: %v", err)
	}

	sub, err := nc.SubscribeSync(replyInbox)
	if err != nil {
		log.Fatalf("Failed to subscribe to reply inbox: %v", err)
	}
	defer sub.Unsubscribe()

	fmt.Printf("📤 Publishing request to subject: %s (Reply Inbox: %s)\n", subject, replyInbox)
	if err := nc.PublishRequest(subject, replyInbox, envelopeBytes); err != nil {
		log.Fatalf("Failed to publish request: %v", err)
	}
	if err := nc.Flush(); err != nil {
		log.Fatalf("Failed to flush NATS: %v", err)
	}

	timeout := 45 * time.Second
	deadline := time.Now().Add(timeout)
	fmt.Printf("⏳ Waiting up to %v for reconciliation results...\n", timeout)

	var workerMsg *nats.Msg
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		msg, err := sub.NextMsg(remaining)
		if err != nil {
			if err == nats.ErrTimeout {
				break
			}
			log.Fatalf("Error waiting for response: %v", err)
		}

		// Check if this message is a JetStream PubAck (e.g. {"stream":"WORKFLOWS", "seq":423})
		var pubAck struct {
			Stream string `json:"stream"`
			Seq    uint64 `json:"seq"`
		}
		if err := json.Unmarshal(msg.Data, &pubAck); err == nil && pubAck.Stream != "" && pubAck.Seq > 0 {
			fmt.Printf("ℹ️  Received JetStream PubAck [Stream: %s, Seq: %d]. Awaiting worker execution response...\n", pubAck.Stream, pubAck.Seq)
			continue
		}

		workerMsg = msg
		break
	}

	if workerMsg == nil {
		log.Fatalf("❌ Timed out after %v without receiving reconciliation worker response.", timeout)
	}

	fmt.Println("\n================================================================")
	fmt.Println("📥 RECONCILIATION RESULT RECEIVED")
	fmt.Println("================================================================")

	var env WorkerResponseEnvelope
	var resp OptimizerResponse
	var rawPayloadJSON string

	if err := json.Unmarshal(workerMsg.Data, &env); err == nil && env.Type != "" {
		fmt.Printf("Response Type: %s\n", env.Type)

		if env.Type == "FAILURE" {
			fmt.Printf("❌ Worker returned error: %s\n", string(env.Payload))
			return
		}

		var innerString string
		if err := json.Unmarshal(env.Payload, &innerString); err == nil {
			rawPayloadJSON = innerString
			if err := json.Unmarshal([]byte(innerString), &resp); err != nil {
				log.Printf("Warning: failed to unmarshal OptimizerResponse from string payload: %v", err)
			}
		} else {
			rawPayloadJSON = string(env.Payload)
			if err := json.Unmarshal(env.Payload, &resp); err != nil {
				log.Printf("Warning: failed to unmarshal OptimizerResponse from raw payload: %v", err)
			}
		}
	} else {
		rawPayloadJSON = string(workerMsg.Data)
		_ = json.Unmarshal(workerMsg.Data, &resp)
	}

	// Print Structured Reconciliation Results
	fmt.Printf("\n📊 SOLVER SUMMARY:\n")
	fmt.Printf("  • Solver Status:             %s\n", resp.Status)
	if resp.ObjectiveValue != nil {
		fmt.Printf("  • Objective Value:           %d\n", *resp.ObjectiveValue)
	}
	fmt.Printf("  • Selected Hypotheses (Won): %d\n", len(resp.SelectedHypotheses))
	fmt.Printf("  • Rejected Hypotheses:       %d\n", len(resp.RejectedHypotheses))
	fmt.Printf("  • Unresolved Bank Items:     %d\n", len(resp.UnresolvedBankItems))
	fmt.Printf("  • Book Residual Updates:     %d\n", len(resp.BookResiduals))

	if len(resp.SelectedHypotheses) > 0 {
		fmt.Println("\n🔗 SELECTED MATCH HYPOTHESES:")
		for idx, h := range resp.SelectedHypotheses {
			fmt.Printf("  [%d] Hypothesis ID: %s (Utility Score: %.2f | Reason: %s)\n", idx+1, h.HypothesisID, h.Utility, h.Reason)
			fmt.Println("      Bank Allocations:")
			for _, b := range h.BankAllocations {
				fmt.Printf("        - Bank Item ID: %s | Amount: %s\n", b.BankItemID, formatUnits(b.AmountUnits, currency))
			}
			fmt.Println("      Book Allocations:")
			for _, k := range h.BookAllocations {
				fmt.Printf("        - Book Item ID: %s | Amount: %s\n", k.BookItemID, formatUnits(k.AmountUnits, currency))
			}
		}
	}

	if len(resp.UnresolvedBankItems) > 0 {
		fmt.Println("\n⚠️  UNRESOLVED BANK ITEMS:")
		for _, b := range resp.UnresolvedBankItems {
			fmt.Printf("  • Bank Item ID: %s (Reason: %s)\n", b.BankItemID, b.Reason)
		}
	}

	if len(resp.BookResiduals) > 0 {
		fmt.Println("\n📝 BOOK RESIDUALS:")
		for _, r := range resp.BookResiduals {
			fmt.Printf("  • Book Item ID: %s | Starting: %s | Consumed: %s | Remaining: %s\n",
				r.BookItemID,
				formatUnits(r.StartingAmountUnits, currency),
				formatUnits(r.ConsumedAmountUnits, currency),
				formatUnits(r.RemainingAmountUnits, currency),
			)
		}
	}

	if len(resp.Diagnostics) > 0 {
		fmt.Println("\n🩺 SOLVER DIAGNOSTICS:")
		for _, d := range resp.Diagnostics {
			fmt.Printf("  • %s\n", d)
		}
	}

	if len(resp.SolverStats) > 0 {
		fmt.Println("\n⏱️  SOLVER STATS:")
		for k, v := range resp.SolverStats {
			fmt.Printf("  • %s: %v\n", k, v)
		}
	}

	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("📄 FULL RAW RESPONSE JSON:")
	fmt.Println("----------------------------------------------------------------")
	var prettyObj interface{}
	if err := json.Unmarshal([]byte(rawPayloadJSON), &prettyObj); err == nil {
		prettyBytes, _ := json.MarshalIndent(prettyObj, "", "  ")
		fmt.Println(string(prettyBytes))
	} else if len(strings.TrimSpace(rawPayloadJSON)) > 0 {
		fmt.Println(rawPayloadJSON)
	} else {
		fmt.Println(string(workerMsg.Data))
	}
	fmt.Println("================================================================")
}

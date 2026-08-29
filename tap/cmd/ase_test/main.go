// Command ase_test sends mock bank transactions over NATS directly to the
// container-native AseLightweightBridgeWorker running inside the protocol container.
//
// Examples:
//
//	# 1. Run a single specific inflow or outflow edge test case
//	go run tap/cmd/ase_test/main.go -edge CUSTOMER_INVOICE_RECEIPT_3421
//	go run tap/cmd/ase_test/main.go -edge SOCIAL_CONTRIBUTION_PAYMENT_444X
//
//	# 2. Run all Inflow or Outflow test cases through the container
//	go run tap/cmd/ase_test/main.go -direction INFLOW
//	go run tap/cmd/ase_test/main.go -direction OUTFLOW
//
//	# 3. List all 101 curated mock test cases
//	go run tap/cmd/ase_test/main.go -list
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/fixtures"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

const (
	defaultDAGName    = "pcm_bank_cash_accounting_dag"
	defaultDomainTool = "pcm_cash_accounting"
)

// ExecutionReport is the structured report received from the container worker.
type ExecutionReport struct {
	TransactionID   string                     `json:"transaction_id"`
	EdgeKey         string                     `json:"edge_key"`
	CashDirection   string                     `json:"cash_direction"`
	RawDescription  string                     `json:"raw_description"`
	RawAmount       string                     `json:"raw_amount"`
	Currency        string                     `json:"currency"`
	FinalState      ase.NodeState              `json:"final_state"`
	HoldReason      string                     `json:"hold_reason,omitempty"`
	Candidates      []ase.ProbabilityCandidate `json:"candidates,omitempty"`
	SelectedEdge    string                     `json:"selected_edge,omitempty"`
	TargetChildNode string                     `json:"target_child_node,omitempty"`
	ExpectedChild   string                     `json:"expected_child,omitempty"`
	MatchedExpected bool                       `json:"matched_expected"`
	ExecutionSteps  []ase.NodeExecutionStep    `json:"execution_steps"`
	Duration        time.Duration              `json:"duration"`
}

func main() {
	edgeFlag := flag.String("edge", "", "Target edge key to test (e.g. CUSTOMER_INVOICE_RECEIPT_3421)")
	directionFlag := flag.String("direction", "", "Run all test cases for direction (INFLOW or OUTFLOW)")
	allFlag := flag.Bool("all", false, "Run all 101 test cases")
	listFlag := flag.Bool("list", false, "List all 101 available test cases")
	dagName := flag.String("dag-name", defaultDAGName, "ASE DAG configuration name")
	domainTool := flag.String("domain-tool", defaultDomainTool, "ASE domain tool")
	timeoutSec := flag.Int("timeout", 60, "Timeout in seconds for each classification")
	verbose := flag.Bool("verbose", false, "Print verbose execution traces and candidate maps")
	flag.Parse()

	// 1. Handle List Mode
	if *listFlag {
		all, err := fixtures.LoadMockTransactions()
		if err != nil {
			fmt.Printf("Error loading mock transactions: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("=======================================================================================================================")
		fmt.Println("                               PCM BANK CASH ACCOUNTING DAG - 101 MOCK TEST CASES                                     ")
		fmt.Println("=======================================================================================================================")
		fmt.Printf("%-4s | %-7s | %-44s | %-32s | %s\n", "#", "DIR", "TARGET EDGE KEY", "EXPECTED CHILD NODE", "SAMPLE NARRATIVE")
		fmt.Println("-----------------------------------------------------------------------------------------------------------------------")

		for i, tx := range all {
			narrative := tx.RawDescription
			if len(narrative) > 40 {
				narrative = narrative[:37] + "..."
			}
			fmt.Printf("%-4d | %-7s | %-44s | %-32s | %s\n", i+1, tx.CashDirection, tx.EdgeKey, tx.ExpectedChildNode, narrative)
		}
		fmt.Println("=======================================================================================================================")
		fmt.Printf("Total: %d test cases (41 Inflow, 60 Outflow)\n", len(all))
		return
	}

	// 2. Connect to NATS
	if os.Getenv("NATS_URL") == "" {
		_ = os.Setenv("NATS_URL", "nats://localhost:4222")
	}

	cfg, _, err := config.Load()
	natsURL := "nats://localhost:4222"
	if err == nil && cfg != nil && cfg.NATS.URL != "" {
		natsURL = cfg.NATS.URL
	}

	nc, err := nats.Connect(natsURL, nats.Name("ase-test-nats-cli"), nats.Timeout(5*time.Second))
	if err != nil {
		fmt.Printf("❌ Failed to connect to NATS at %s: %v\n", natsURL, err)
		os.Exit(1)
	}
	defer nc.Close()

	// 3. Determine test suite to execute
	var testQueue []fixtures.MockTransaction

	if *edgeFlag != "" {
		tx, err := fixtures.GetMockTransactionByEdge(*edgeFlag)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			os.Exit(1)
		}
		testQueue = append(testQueue, *tx)
	} else if *directionFlag != "" {
		filtered, err := fixtures.FilterByDirection(strings.ToUpper(*directionFlag))
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			os.Exit(1)
		}
		testQueue = filtered
	} else if *allFlag {
		all, err := fixtures.LoadMockTransactions()
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			os.Exit(1)
		}
		testQueue = all
	} else {
		fmt.Println("Usage of ase_test:")
		fmt.Println("  go run tap/cmd/ase_test/main.go -edge <EDGE_KEY>       # Test single node/edge via NATS")
		fmt.Println("  go run tap/cmd/ase_test/main.go -direction INFLOW      # Test all 41 inflow nodes via NATS")
		fmt.Println("  go run tap/cmd/ase_test/main.go -direction OUTFLOW     # Test all 60 outflow nodes via NATS")
		fmt.Println("  go run tap/cmd/ase_test/main.go -all                   # Test all 101 nodes via NATS")
		fmt.Println("  go run tap/cmd/ase_test/main.go -list                  # List all available cases")
		os.Exit(0)
	}

	targetInbox := "worker.inbox.ase_lightweight_bridge"
	timeout := time.Duration(*timeoutSec) * time.Second

	fmt.Printf("\n🚀 Dispatching NATS Envelopes to Container Worker: %s (Total Cases: %d)\n\n", targetInbox, len(testQueue))

	passed := 0
	failed := 0

	for i, mockTx := range testQueue {
		fmt.Printf("---------------------------------------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] Testing Edge: %s (%s %s %s)\n", i+1, len(testQueue), mockTx.EdgeKey, mockTx.CashDirection, mockTx.RawAmount, mockTx.Currency)
		fmt.Printf("  • Narrative   : %s\n", mockTx.RawDescription)
		if mockTx.CounterpartyName != "" {
			fmt.Printf("  • Counterparty: %s (ICE: %s)\n", mockTx.CounterpartyName, mockTx.ICENumber)
		}

		replyDID := fmt.Sprintf("did:toro:cli:ase-test:%s", uuid.New().String())
		replySubject := fmt.Sprintf("agents.%s.inbox", replyDID)

		msgChan := make(chan *nats.Msg, 5)
		sub, err := nc.ChanSubscribe(replySubject, msgChan)
		if err != nil {
			fmt.Printf("  ❌ Failed to subscribe to reply subject %s: %v\n", replySubject, err)
			failed++
			continue
		}

		reqPayload := map[string]any{
			"config": map[string]string{
				"dag_name":    *dagName,
				"domain_tool": *domainTool,
			},
			"input": map[string]any{
				"mock_transaction": mockTx,
				"timeout_seconds":  *timeoutSec,
			},
		}

		reqBytes, err := json.Marshal(reqPayload)
		if err != nil {
			fmt.Printf("  ❌ Failed to marshal request: %v\n", err)
			sub.Unsubscribe()
			failed++
			continue
		}

		envelope, err := core.NewEnvelope(
			uuid.NewString(),
			replyDID,
			"did:toro:worker:ase-lightweight-bridge",
			uuid.NewString(),
			core.REQUEST,
			json.RawMessage(reqBytes),
		)
		if err != nil {
			fmt.Printf("  ❌ Failed to construct envelope: %v\n", err)
			sub.Unsubscribe()
			failed++
			continue
		}

		envBytes, _ := json.Marshal(envelope)
		if err := nc.Publish(targetInbox, envBytes); err != nil {
			fmt.Printf("  ❌ Failed to publish to %s: %v\n", targetInbox, err)
			sub.Unsubscribe()
			failed++
			continue
		}
		_ = nc.Flush()

		// Await container reply
		var report *ExecutionReport
		select {
		case msg := <-msgChan:
			var replyEnv core.Envelope
			if err := json.Unmarshal(msg.Data, &replyEnv); err == nil {
				var rep ExecutionReport
				if err := json.Unmarshal(replyEnv.Body, &rep); err == nil {
					report = &rep
				}
			}
		case <-time.After(timeout):
			fmt.Printf("  ❌ Timeout (%v) waiting for response from protocol container worker.\n", timeout)
			fmt.Println("     -> Did you rebuild and run the protocol container with the new lightweight bridge worker?")
		}
		sub.Unsubscribe()

		if report == nil {
			failed++
			continue
		}

		// Display Candidates
		if len(report.Candidates) > 0 {
			fmt.Printf("  • LLM Classification (from Container):\n")
			for rank, c := range report.Candidates {
				fmt.Printf("    [%d] Value: %-38s | Conf: %.2f | Reasoning: %s\n", rank+1, c.Value, c.Confidence, c.Reasoning)
			}
		}

		// Display Trace
		fmt.Printf("  • Selected Edge   : %s\n", report.SelectedEdge)
		fmt.Printf("  • Child Node Route: %s (Expected: %s)\n", report.TargetChildNode, report.ExpectedChild)
		fmt.Printf("  • Final Node State: %s (Duration: %v)\n", report.FinalState, report.Duration.Round(time.Millisecond))
		if report.HoldReason != "" {
			fmt.Printf("  • Hold Reason     : %s\n", report.HoldReason)
		}

		if *verbose && len(report.ExecutionSteps) > 0 {
			fmt.Println("  • Container Step Execution Trace:")
			for _, step := range report.ExecutionSteps {
				fmt.Printf("    - At [%s] -> Edge [%s] (Property: %s, Time: %s)\n",
					step.DAGNodeID, step.SelectedEdge, step.PropertyKey, step.Timestamp.Format("15:04:05.000"))
			}
		}

		if report.MatchedExpected {
			fmt.Println("  ✅ Result: PASS (Correctly routed to child branch inside container)")
			passed++
		} else {
			fmt.Println("  ❌ Result: MISMATCH (Did not route to expected child branch)")
			failed++
		}
	}

	fmt.Println("===================================================================================================")
	fmt.Printf("🏁 Summary: %d Total | %d Passed | %d Failed\n", len(testQueue), passed, failed)
	fmt.Println("===================================================================================================")

	if failed > 0 {
		os.Exit(1)
	}
}

package agents

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
	"github.com/nats-io/nats.go"
)

// Define the Data Contracts
type OCRResult struct {
	Text   string  `json:"text"`
	Amount float64 `json:"amount"`
}

func BillingAgent() {
	// 1. Identity & Connection
	kp, _ := identity.KeyPairFromSeed("agents.billing")
	did := identity.CreateDID(kp.Public)
	log.Printf("💰 Billing Agent Online. DID: %s", did)

	nc, err := transport.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	// 2. Register with Almanac
	registerBilling(nc, kp, did)

	// 3. Subscribe to Inbox (to receive Proposals and Results)
	myInbox := core.BuildAgentInbox(did)
	proposals := make(chan core.Envelope, 10)
	results := make(chan core.Envelope, 10)

	nc.Subscribe(myInbox, func(msg *nats.Msg) {
		var env core.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			return
		}
		switch env.Performative {
		case core.PROPOSE:
			proposals <- env
		case core.INFORM:
			results <- env
		}
	})

	log.Printf("👂 Listening on %s", myInbox)

	// 4. Discovery: Find OCR Capability
	client := lookup.New(nc, did)
	log.Println("🔍 Searching for 'perception.ocr' agents...")
	entries, err := client.FindAgents("perception.ocr", 2*time.Second)
	if err != nil || len(entries) == 0 {
		log.Fatalf("❌ No OCR agents found: %v", err)
	}
	log.Printf("✅ Found %d OCR Agents", len(entries))

	targetAgent := entries[0] // Pick the first one for simplicity

	// 5. Negotiation: Step 1 - Send CFP (Call for Proposal)
	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
	cfpPayload := map[string]interface{}{
		"task_description": "Extract amount from receipt",
		"image_url":        "http://receipt.jpg", // The actual task data can be here or in the Contract
	}

	// We send the CFP to the agent's inbox (or a specific topic if it was a broadcast)
	// Since we found a specific agent, we can send directly to their inbox OR use their topic if we knew it.
	// The Almanac returns Endpoints. Let's use the first endpoint.
	targetTopic := targetAgent.Endpoints[0]

	log.Printf("📤 Sending CFP to %s (%s)", targetAgent.DID, targetTopic)
	sendEnvelope(nc, kp, did, targetAgent.DID, targetTopic, taskID, core.CFP, cfpPayload)

	// 6. Negotiation: Step 2 - Wait for PROPOSE
	log.Println("⏳ Waiting for Proposals...")
	var selectedProposal *core.Envelope
	select {
	case env := <-proposals:
		if env.ConversationID == taskID {
			log.Printf("📩 Received PROPOSE from %s", env.SenderDID)
			selectedProposal = &env
		}
	case <-time.After(5 * time.Second):
		log.Fatal("❌ Timeout waiting for proposal")
	}

	if selectedProposal == nil {
		log.Fatal("❌ No valid proposal received")
	}

	// 7. Negotiation: Step 3 - Send ACCEPT
	log.Printf("🤝 Accepting Proposal from %s", selectedProposal.SenderDID)
	// The contract/input details usually go here or confirmed here.
	acceptPayload := OCRInput{
		ImageURL: "http://receipt.jpg", // Confirming the input
	}
	// We reply to the sender of the proposal, to their inbox (which they should be listening on)
	// We can infer their inbox from DID or use the ReplyTo if we had it, but protocol says DID inbox.
	senderInbox := core.BuildAgentInbox(selectedProposal.SenderDID)
	sendEnvelope(nc, kp, did, selectedProposal.SenderDID, senderInbox, taskID, core.ACCEPT_PROPOSAL, acceptPayload)

	// 8. Execution: Step 4 - Wait for Result (INFORM)
	log.Println("⏳ Waiting for Result...")
	select {
	case env := <-results:
		if env.ConversationID == taskID {
			var result OCRResult
			if err := json.Unmarshal(env.Body, &result); err != nil {
				log.Printf("⚠️ Failed to parse result: %v", err)
				return
			}
			log.Printf("✅ OCR Success! Amount: $%.2f", result.Amount)
			log.Printf("📝 Text: %s", result.Text)

			if result.Amount < 50.00 {
				log.Println("🤖 Auto-Approving small expense.")
			} else {
				log.Println("👮 Flagging for manual review.")
			}
		}
	case <-time.After(10 * time.Second):
		log.Fatal("❌ Timeout waiting for result")
	}
}

// registerBilling sends a heartbeat to the Almanac
func registerBilling(nc *nats.Conn, kp *identity.KeyPair, did string) {
	payload := lookup.RegistrationPayload{
		DID:       did,
		Endpoints: []string{core.BuildAgentInbox(did)},
		Capabilities: []lookup.RegistrationCapability{
			{Type: "finance.billing"},
		},
		Expiry: time.Now().Add(1 * time.Hour),
	}
	data, _ := json.Marshal(payload)
	nc.Publish("almanac.register", data)
}

func sendEnvelope(nc *nats.Conn, kp *identity.KeyPair, senderDID, receiverDID, topic, conversationID string, perf core.Performative, payload interface{}) {
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	env, _ := core.NewEnvelope(msgID, senderDID, receiverDID, conversationID, perf, payload)
	env.Signature = kp.Sign(env.Body)
	data, _ := json.Marshal(env)
	nc.Publish(topic, data)
}

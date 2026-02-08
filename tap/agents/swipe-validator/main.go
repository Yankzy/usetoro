package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/tap"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
	"github.com/nats-io/nats.go"
)

func main() {
	// 1. Setup Identity
	kp, _ := identity.GenerateKeyPair()
	did := identity.DIDFromPubKey(kp.Public)
	log.Printf("🤖 Swipe Generator started. DID: %s", did)

	// 2. Connect
	nc, err := transport.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	// 3. Start Listening for Proposals (Workers answering our tasks)
	// We listen on our specific Inbox
	inbox := tap.BuildAgentInbox(did)
	nc.Subscribe(inbox, func(msg *nats.Msg) {
		handleProposal(nc, kp, did, msg)
	})

	// 4. Broadcast Tasks Loop
	ticker := time.NewTicker(5 * time.Second)
	for range ticker.C {
		broadcastTask(nc, kp, did)
	}
}

func broadcastTask(nc *nats.Conn, kp *identity.KeyPair, did string) {
	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())

	// Create the Task Definition (The "CFP")
	task := tap.TaskDefinition{
		ID:         taskID,
		Domain:     "accounting",
		Complexity: tap.ComplexityEntry, // Level 1 (Easy)
		Reward:     500,                 // 0.05 cents
		Currency:   "USD",
		Payload:    json.RawMessage(`{"image_url": "https://s3/receipt_99.jpg", "question": "Is this a meal?"}`),
		ExpiresAt:  time.Now().Add(1 * time.Hour).Unix(),
	}

	// Wrap in Envelope
	// We don't have a specific destination, so dst is generic or empty for broadcast
	env, _ := tap.NewEnvelope(
		fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		did,
		"did:toro:broadcast", // Public broadcast
		taskID,               // Conversation ID starts as Task ID
		tap.CFP,              // "Call For Proposal"
		task,
	)
	env.Signature = kp.Sign(env.Body) // Sign it

	// Send to the Public Topic for Level 1 Accounting
	subject := tap.BuildTaskSubject("accounting", tap.ComplexityEntry, "verify")

	msgData, _ := json.Marshal(env)
	nc.Publish(subject, msgData)

	log.Printf("📢 Broadcasted Task: %s on %s", taskID, subject)
}

func handleProposal(nc *nats.Conn, kp *identity.KeyPair, myDID string, msg *nats.Msg) {
	var env tap.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return
	}

	if env.Performative == tap.PROPOSE {
		log.Printf("📩 Received PROPOSAL from %s for %s", env.SenderDID, env.ConversationID)

		// AUTOMATICALLY ACCEPT (For Demo)
		// In reality, we would check the price/reputation here.

		contractReq := tap.Contract{
			ID:             fmt.Sprintf("con_%s", env.ConversationID),
			ConversationID: env.ConversationID,
			InitiatorDID:   myDID,         // Us
			AcceptorDID:    env.SenderDID, // The Worker
			Terms:          env.Body,      // The agreed terms (simplified)
			TermsHash:      "hash_of_terms_placeholder",
			Signatures:     make(map[string]string),
			Status:         tap.ContractDraft,
		}

		// Sign the contract
		contractReq.Signatures[myDID] = kp.Sign([]byte(contractReq.TermsHash))

		// Send ACCEPT back to Worker
		replyEnv, _ := tap.NewEnvelope(
			fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			myDID,
			env.SenderDID,
			env.ConversationID,
			tap.ACCEPT_PROPOSAL,
			contractReq,
		)
		replyEnv.Signature = kp.Sign(replyEnv.Body)

		replyBytes, _ := json.Marshal(replyEnv)
		nc.Publish(tap.BuildAgentInbox(env.SenderDID), replyBytes)

		log.Printf("✅ Sent ACCEPT to %s", env.SenderDID)
	}
}

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

// The Contract
type OCRInput struct {
	ImageURL string `json:"image_url"`
}
type OCROutput struct {
	Text   string  `json:"text"`
	Amount float64 `json:"amount"`
}

func OCRAgent() {
	// 1. Setup Identity
	kp, _ := identity.KeyPairFromSeed("agents.ocr")
	did := identity.CreateDID(kp.Public)
	log.Printf("👁️ OCR Agent Online. DID: %s", did)

	// 2. Connect
	nc, err := transport.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	// 3. Register with Almanac
	registerOCR(nc, kp, did)

	// 4. Subscribe to Direct Messages (For Negotiation/Requests)
	myInbox := core.BuildAgentInbox(did)
	nc.Subscribe(myInbox, func(msg *nats.Msg) {
		handleOCRDirectMessage(nc, kp, did, msg)
	})

	// 5. Subscribe to Work (Listening for broadcast requests)
	// We listen to "tasks.ocr.>" to catch any OCR related CFPs
	workTopic := "tasks.ocr.>"
	nc.Subscribe(workTopic, func(msg *nats.Msg) {
		handleOCRWork(nc, kp, did, msg)
	})

	log.Printf("👂 Listening for OCR work on %s...", workTopic)
	select {} // Block forever
}

// registerOCR sends a heartbeat to the Almanac
func registerOCR(nc *nats.Conn, kp *identity.KeyPair, did string) {
	payload := lookup.RegistrationPayload{
		DID:       did,
		Endpoints: []string{core.BuildAgentInbox(did)},
		Capabilities: []lookup.RegistrationCapability{
			{
				Type: "perception.ocr",
				Meta: map[string]interface{}{
					"formats": []string{"jpg", "png", "pdf"},
					"cost":    "low",
				},
			},
		},
		Expiry: time.Now().Add(1 * time.Hour),
	}
	// Sign the payload (simulated here by signing the body if needed, but Almanac checks DID owner)
	// The new lookup.RegistrationPayload has a Signature field, we should sign it if strictly required.
	// For now we'll trust the transport or simple registration flow as seen in trucker.
	// NOTE: simple_trucker.go does not sign the RegistrationPayload explicitly in the construct,
	// but the Almanac might require it. The simple_trucker example didn't set Signature.
	// We will follow simple_trucker pattern.

	data, _ := json.Marshal(payload)
	nc.Publish("almanac.register", data)
}

// handleOCRWork processes broadcast CFPs
func handleOCRWork(nc *nats.Conn, kp *identity.KeyPair, myDID string, msg *nats.Msg) {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return
	}

	// We only care about CFPs (Calls for Proposal)
	if env.Performative == core.CFP {
		log.Printf("📄 Found OCR Job: %s (from %s)", env.ConversationID, env.SenderDID)

		// 1. Analyze Task (Check if we can handle the image format, etc.)
		// For now, we accept everything.

		// 2. Send PROPOSE
		proposalBody := map[string]interface{}{
			"price":        0.05,
			"availability": "immediate",
		}

		replyEnv, _ := core.NewEnvelope(
			fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			myDID,
			env.SenderDID,
			env.ConversationID,
			core.PROPOSE,
			proposalBody,
		)
		replyEnv.Signature = kp.Sign(replyEnv.Body)

		replyBytes, _ := json.Marshal(replyEnv)
		nc.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes)

		log.Printf("📨 Sent Proposal for %s", env.ConversationID)
	}
}

// handleOCRDirectMessage processes ACCEPT_PROPOSAL or REQUEST
func handleOCRDirectMessage(nc *nats.Conn, kp *identity.KeyPair, myDID string, msg *nats.Msg) {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return
	}

	switch env.Performative {
	case core.ACCEPT_PROPOSAL:
		log.Printf("🤝 OCR Offer ACCEPTED! Job %s started.", env.ConversationID)

		// 1. Extract the Contract/Task from the Body
		// The body in ACCEPT might contain the original request details or a reference.
		// For simplicity, let's assume the task details (ImageURL) were in the original CFP
		// or re-sent here. Protocol-wise, the agent handles the 'Task' agreed upon.
		// If the original CFP had the payload, we might need to cache it, or expect it here.
		// We'll assume the inputs are passed in the ACCEPT body for this simple version.
		var input OCRInput
		// Try unmarshal body to see if it has the input directly
		if err := json.Unmarshal(env.Body, &input); err != nil || input.ImageURL == "" {
			// Fallback: simpler assumption for demo
			log.Printf("⚠️ No image URL found in ACCEPT body, assuming default/mock.")
			input.ImageURL = "mock://image"
		}

		// 2. Perform OCR (Simulated)
		go performOCR(nc, kp, myDID, env.SenderDID, env.ConversationID, input)

	case core.REQUEST:
		// Handle direct requests (bypassing negotiation)
		log.Printf("📥 Received Direct OCR Request: %s", env.ConversationID)
		var input OCRInput
		json.Unmarshal(env.Body, &input)
		go performOCR(nc, kp, myDID, env.SenderDID, env.ConversationID, input)
	}
}

// performOCR simulates OCR processing
func performOCR(nc *nats.Conn, kp *identity.KeyPair, myDID, senderDID, conversationID string, input OCRInput) {
	log.Printf("🧠 Processing Image: %s", input.ImageURL)
	time.Sleep(1 * time.Second) // Simulate CV processing

	// 3. Send INFORM (Result)
	result := OCROutput{
		Text:   "Receipt for Lunch\nTotal: $45.50\nDate: 2023-10-27",
		Amount: 45.50,
	}

	replyEnv, _ := core.NewEnvelope(
		fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		myDID,
		senderDID,
		conversationID,
		core.INFORM,
		result,
	)
	replyEnv.Signature = kp.Sign(replyEnv.Body)

	replyBytes, _ := json.Marshal(replyEnv)
	nc.Publish(core.BuildAgentInbox(senderDID), replyBytes)

	log.Printf("✅ OCR Done. Result sent for %s", conversationID)
}

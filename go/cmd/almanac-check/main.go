package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type AlmanacQuery struct {
	CallerDID      string `json:"caller_did,omitempty"`
	CapabilityType string `json:"capability_type,omitempty"`
	DID            string `json:"did,omitempty"`
}

type RegistrationCapability struct {
	Type string                 `json:"type"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

type AlmanacEntry struct {
	DID          string                   `json:"did"`
	Endpoints    []string                 `json:"endpoints"`
	Capabilities []RegistrationCapability `json:"capabilities"`
}

func main() {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	fmt.Println("--- Almanac Self-Check ---")

	var q AlmanacQuery

	// 1. Query by DID
	q = AlmanacQuery{
		CallerDID: "did:toro:admin",
		DID:       "did:toro:agent:cleanup_1",
	}
	fmt.Printf("🔍 Querying DID: %s\n", q.DID)
	check(nc, q)

	// 2. Query by Capability
	q = AlmanacQuery{
		CallerDID:      "did:toro:admin",
		CapabilityType: "accounting.cleanup",
	}
	fmt.Printf("\n🔍 Querying Capability: %s\n", q.CapabilityType)
	check(nc, q)
}

func check(nc *nats.Conn, q AlmanacQuery) {
	data, _ := json.Marshal(q)

	// Create a custom inbox to receive multiple messages if necessary
	inbox := nats.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		fmt.Printf("   ❌ Subscription failed: %v\n", err)
		return
	}
	defer sub.Unsubscribe()

	// Publish with our custom reply subject
	if err := nc.PublishRequest(core.SubjectAlmanacQuery, inbox, data); err != nil {
		fmt.Printf("   ❌ Publish failed: %v\n", err)
		return
	}

	// Wait for the response, ignoring JetStream PubAcks
	var results []AlmanacEntry
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		msg, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}

		// Try unmarshaling. If it's a PubAck, it will fail unmarshaling into []AlmanacEntry
		// but we should check if it's the expected data.
		if err := json.Unmarshal(msg.Data, &results); err == nil {
			// Success! We found the data.
			break
		}

		// If it's not our data, it might be the JetStream ACK {"stream":..., "seq":...}
		// We just continue and wait for the next message.
	}

	if len(results) == 0 {
		fmt.Println("   ⚠️ Not found (or only received ACKs).")
		return
	}

	for _, e := range results {
		fmt.Printf("   ✅ Found: %s\n", e.DID)
		fmt.Printf("      Endpoints: %v\n", e.Endpoints)
		for _, c := range e.Capabilities {
			fmt.Printf("      Capability: %s\n", c.Type)
		}
	}
}

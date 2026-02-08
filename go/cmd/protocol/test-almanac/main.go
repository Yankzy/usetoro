package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/nats-io/nats.go"
)

type RegistrationPayload struct {
	DID          string   `json:"did"`
	Endpoints    []string `json:"endpoints"`
	Capabilities []struct {
		Type string `json:"type"`
	} `json:"capabilities"`
	Expiry    time.Time `json:"expiry"`
	Signature string    `json:"signature"`
}

func main() {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	// Generate Identity
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	did := identity.DIDFromPubKey(pub)

	// Register
	payload := RegistrationPayload{
		DID:       did,
		Endpoints: []string{"http://localhost:8080"},
		Capabilities: []struct {
			Type string `json:"type"`
		}{
			{Type: "test.capability"},
		},
		Expiry:    time.Now().Add(1 * time.Minute),
		Signature: "mock_sig",
	}

	data, _ := json.Marshal(payload)
	err = nc.Publish("almanac.register", data)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Sent Registration for:", did)

	time.Sleep(1 * time.Second)

	// Query
	query := struct {
		Capability string `json:"capability_type"`
	}{
		Capability: "test.capability",
	}
	qData, _ := json.Marshal(query)

	msg, err := nc.Request("almanac.query", qData, 2*time.Second)
	if err != nil {
		log.Fatal("Query failed:", err)
	}

	fmt.Printf("Response: %s\n", string(msg.Data))
}

package lookup

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestAlmanacTools(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	// Try to connect to NATS, skip if not available
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Skipf("NATS not available at %s, skipping Almanac tools test: %v", natsURL, err)
	}
	defer nc.Close()

	// Mock the Almanac service by subscribing to almanac.query
	sub, err := nc.Subscribe("almanac.query", func(msg *nats.Msg) {
		var query AlmanacQuery
		if err := json.Unmarshal(msg.Data, &query); err != nil {
			return
		}

		response := []AlmanacEntry{}

		if query.DID == "did:toro:testagent" {
			response = append(response, AlmanacEntry{
				DID:       "did:toro:testagent",
				Endpoints: []string{"agents.testagent.inbox"},
			})
		} else if query.CapabilityType == "logistics.trucking" {
			response = append(response, AlmanacEntry{
				DID:          "did:toro:trucker1",
				Capabilities: []RegistrationCapability{{Type: "logistics.trucking"}},
			})
			response = append(response, AlmanacEntry{
				DID:          "did:toro:trucker2",
				Capabilities: []RegistrationCapability{{Type: "logistics.trucking"}},
			})
		}

		respData, _ := json.Marshal(response)
		msg.Respond(respData)
	})
	if err != nil {
		t.Fatalf("Failed to subscribe to almanac.query: %v", err)
	}
	defer sub.Unsubscribe()

	t.Run("ResolveAgentByDID", func(t *testing.T) {
		entry, err := ResolveAgentByDID(nc, "did:toro:testcaller", "did:toro:testagent", 1*time.Second)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if entry == nil || entry.DID != "did:toro:testagent" {
			t.Fatalf("Unexpected entry returned: %+v", entry)
		}
	})

	t.Run("FindAgents", func(t *testing.T) {
		entries, err := FindAgents(nc, "did:toro:testcaller", "logistics.trucking", 1*time.Second)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("Expected 2 entries, got: %d", len(entries))
		}
	})

	t.Run("ResolveAgentsByCapability", func(t *testing.T) {
		entries, err := ResolveAgentsByCapability(nc, "did:toro:testcaller", "logistics.trucking", 1*time.Second)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("Expected 2 entries, got: %d", len(entries))
		}
	})

	t.Run("ResolveAgent", func(t *testing.T) {
		query := AlmanacQuery{DID: "did:toro:testagent"}
		entry, err := ResolveAgent(nc, "did:toro:testcaller", query, 1*time.Second)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if entry == nil || entry.DID != "did:toro:testagent" {
			t.Fatalf("Unexpected entry returned: %+v", entry)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := ResolveAgentByDID(nc, "did:toro:testcaller", "did:toro:unknown", 1*time.Second)
		if err == nil {
			t.Fatalf("Expected error for unknown DID, but got none")
		}
	})
}

package lookup

import (
	"encoding/json"
	"log"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/nats-io/nats.go"
)

// RegisterAlmanac registers the agent's capability with the discovery server.
func RegisterAlmanac(nc *nats.Conn, kp *identity.KeyPair, did, capability string) {
	inbox := core.BuildAgentInbox(did)
	
	regPayload := RegistrationPayload{
		DID: did,
		Endpoints: []string{inbox},
		Capabilities: []RegistrationCapability{{Type: capability}},
		Expiry: time.Now().Add(24 * time.Hour),
	}
	regPayload.Signature = kp.Sign([]byte(did))
	reqBytes, err := json.Marshal(regPayload)
	if err != nil {
		log.Fatalf("❌ Failed to marshal Almanac registration: %v", err)
	}

	err = nc.Publish("almanac.register", reqBytes)
	if err != nil {
		log.Fatalf("❌ Failed to register with Almanac: %v", err)
	}
	log.Printf("✅ Registered capability '%s' with Almanac", capability)
}

// FindAgents performs a synchronous NATS Request to find agents by capability.
func FindAgents(nc *nats.Conn, capability string, timeout time.Duration) ([]AlmanacEntry, error) {
	client := New(nc)
	return client.FindAgents(capability, timeout)
}

// ResolveAgentByDID looks up a specific agent by DID.
func ResolveAgentByDID(nc *nats.Conn, did string, timeout time.Duration) (*AlmanacEntry, error) {
	client := New(nc)
	return client.ResolveByDID(did, timeout)
}

// ResolveAgentsByCapability looks up agents by capability type.
func ResolveAgentsByCapability(nc *nats.Conn, capability string, timeout time.Duration) ([]AlmanacEntry, error) {
	client := New(nc)
	return client.ResolveByCapabilityType(capability, timeout)
}

// ResolveAgent looks up agents by multiple criteria.
func ResolveAgent(nc *nats.Conn, query AlmanacQuery, timeout time.Duration) (*AlmanacEntry, error) {
	client := New(nc)
	return client.Resolve(query, timeout)
}

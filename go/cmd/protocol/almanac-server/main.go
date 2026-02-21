package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
)

// --- Data Structures ---

type AgentRecord struct {
	DID          string                          `json:"did"`
	Endpoints    []string                        `json:"endpoints"`
	Capabilities []lookup.RegistrationCapability `json:"capabilities"`
	LastSeen     time.Time                       `json:"last_seen"`
	Expiry       time.Time                       `json:"expiry"`
	// We store the serial number for revocation checks later
	CertSerial string `json:"cert_serial"`
}

type AlmanacServer struct {
	nc      *nats.Conn
	rdb     *redis.Client
	rootCAs *x509.CertPool // The Trust Anchor
}

// --- Main Entry Point ---

func main() {
	// 1. Setup Logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config Load Failed: %v", err)
	}

	// 2. Load Root CA (The Authority)
	// We need this to verify that the Agents aren't fake
	caPath := os.Getenv("TORO_ROOT_CA_PATH")
	if caPath == "" {
		caPath = "/etc/toro/certs/root_ca.crt" // Default path in Docker
	}

	caCertPEM, err := os.ReadFile(caPath)
	if err != nil {
		log.Fatalf("❌ Failed to read Root CA from %s: %v", caPath, err)
	}

	rootCAs := x509.NewCertPool()
	if ok := rootCAs.AppendCertsFromPEM(caCertPEM); !ok {
		log.Fatalf("❌ Failed to parse Root CA PEM")
	}
	log.Println("🔐 Loaded Toro Root CA")

	// 3. Connect to NATS
	url := cfg.NATS.URL
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 4. Connect to Redis
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("❌ Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opt)

	server := &AlmanacServer{
		nc:      nc,
		rdb:     rdb,
		rootCAs: rootCAs,
	}

	// 5. Configure Subscriptions
	// We use the Queue Group "almanac_workers" to load balance registrations
	_, err = nc.QueueSubscribe("almanac.register", "almanac_workers", server.handleRegister)
	if err != nil {
		log.Fatalf("❌ Sub error: %v", err)
	}

	_, err = nc.QueueSubscribe("almanac.query", "almanac_workers", server.handleQuery)
	if err != nil {
		log.Fatalf("❌ Sub error: %v", err)
	}

	log.Println("📖 Almanac Server Online (CA Verified). Listening...")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

// --- Handlers ---

func (s *AlmanacServer) handleRegister(msg *nats.Msg) {
	var payload lookup.RegistrationPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		log.Printf("⚠️ Malformed registration: %v", err)
		return
	}

	// --- STEP 1: CA Verification (The Gatekeeper) ---

	// A. Parse the PEM Block from the payload
	block, _ := pem.Decode([]byte(payload.CertificatePEM))
	if block == nil {
		log.Printf("❌ Registration Rejected: Invalid PEM for DID %s", payload.DID)
		return
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Printf("❌ Registration Rejected: Unparseable Cert for DID %s", payload.DID)
		return
	}

	// B. Verify the Chain of Trust
	// Removed explicit ExtKeyUsage to allow general validation
	opts := x509.VerifyOptions{
		Roots:       s.rootCAs,
		CurrentTime: time.Now(),
	}

	if _, err := cert.Verify(opts); err != nil {
		log.Printf("❌ Registration Rejected: Untrusted Cert for DID %s (Err: %v)", payload.DID, err)
		// TODO: Publish a "Registration Failed" event back to the agent?
		return
	}

	// C. Verify Ownership (DID Match)
	// The CommonName (CN) or URI SAN in the cert MUST match the DID claiming to register
	// Assuming CN holds the DID for simplicity
	if cert.Subject.CommonName != payload.DID {
		log.Printf("❌ Registration Rejected: Cert CN (%s) does not match DID (%s)", cert.Subject.CommonName, payload.DID)
		return
	}

	// --- STEP 2: Persistence ---

	// Store internal record
	record := &AgentRecord{
		DID:          payload.DID,
		Endpoints:    payload.Endpoints,
		Capabilities: payload.Capabilities,
		LastSeen:     time.Now().UTC(),
		Expiry:       payload.Expiry,
		CertSerial:   cert.SerialNumber.String(),
	}

	recordBytes, _ := json.Marshal(record)
	ctx := context.Background()
	pipe := s.rdb.Pipeline()

	// Logic: Use the Expiry from the payload, but cap it at Cert Expiry
	ttl := time.Until(payload.Expiry)
	if time.Now().Add(ttl).After(cert.NotAfter) {
		ttl = time.Until(cert.NotAfter) // Don't let them register past their cert life
	}
	if ttl < 0 {
		log.Printf("⚠️ Cert expired for %s", payload.DID)
		return
	}

	// Main Record
	agentKey := "almanac:agent:" + payload.DID
	pipe.Set(ctx, agentKey, recordBytes, ttl)

	// Capability Indexing
	for _, cap := range record.Capabilities {
		capKey := "almanac:cap:" + strings.ToLower(cap.Type)
		pipe.SAdd(ctx, capKey, payload.DID)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		log.Printf("❌ Redis error: %v", err)
		return
	}

	log.Printf("✅ Verified & Registered: %s", payload.DID)
}

func (s *AlmanacServer) handleQuery(msg *nats.Msg) {
	// Parse the query
	var query lookup.AlmanacQuery
	if err := json.Unmarshal(msg.Data, &query); err != nil {
		return // Ignore bad requests
	}

	ctx := context.Background()

	// 0. Direct DID Lookup (Fast Path)
	if query.DID != "" {
		val, err := s.rdb.Get(ctx, "almanac:agent:"+query.DID).Result()
		if err == nil {
			var record AgentRecord
			// We return a list of 1 for consistency
			if err := json.Unmarshal([]byte(val), &record); err == nil {
				s.sendReply(msg, []AgentRecord{record})
				log.Printf("🔍 Query for DID '%s' -> Found", query.DID)
				return
			}
		}
		// If not found or error, return empty list
		s.sendReply(msg, []AgentRecord{})
		log.Printf("🔍 Query for DID '%s' -> Not Found", query.DID)
		return
	}

	// Use case-insensitive search to match registration behavior
	capKey := "almanac:cap:" + strings.ToLower(query.CapabilityType)

	// 1. Get potential candidates from Set
	candidates, err := s.rdb.SMembers(ctx, capKey).Result()
	if err != nil {
		log.Printf("⚠️ Redis error during query: %v", err)
		return
	}

	var matches []AgentRecord
	var staleDIDs []string

	// 2. Fetch actual records and filter out expired ones
	// We can pipeline this for performance
	pipe := s.rdb.Pipeline()
	for _, did := range candidates {
		pipe.Get(ctx, "almanac:agent:"+did)
	}
	cmds, _ := pipe.Exec(ctx) // Ignore error here, check individual commands

	for i, cmd := range cmds {
		did := candidates[i]
		val, err := cmd.(*redis.StringCmd).Result()
		if err == redis.Nil {
			// Agent expired or missing
			staleDIDs = append(staleDIDs, did)
			continue
		} else if err != nil {
			log.Printf("⚠️ Redis read error for %s: %v", did, err)
			continue
		}

		var record AgentRecord
		if err := json.Unmarshal([]byte(val), &record); err == nil {
			// FILTER: Check if this agent matches the metadata filter
			if len(query.MetaFilter) > 0 {
				if matchMeta(record.Capabilities, query.CapabilityType, query.MetaFilter) {
					matches = append(matches, record)
				}
			} else {
				matches = append(matches, record)
			}
		}
	}

	// 3. Lazy Cleanup (fire and forget)
	if len(staleDIDs) > 0 {
		go func() {
			// Remove stale DIDs from the capability index
			s.rdb.SRem(context.Background(), capKey, sliceToInterface(staleDIDs)...)
		}()
	}

	s.sendReply(msg, matches)
	log.Printf("🔍 Query for '%s' (Meta: %v) -> Found %d agents", query.CapabilityType, query.MetaFilter, len(matches))
}

func (s *AlmanacServer) sendReply(msg *nats.Msg, matches []AgentRecord) {
	respBytes, _ := json.Marshal(matches)
	s.nc.Publish(msg.Reply, respBytes)
}

// matchMeta checks if the agent has a capability of the given type that generally matches the filters
func matchMeta(caps []lookup.RegistrationCapability, targetType string, filter map[string]interface{}) bool {
	targetType = strings.ToLower(targetType)
	for _, c := range caps {
		// Only check capabilities of the requested type
		if strings.ToLower(c.Type) != targetType {
			continue
		}

		// Check if this capability satisfies ALL filter conditions
		match := true
		for k, v := range filter {
			val, exists := c.Meta[k]
			if !exists || val != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Helper for SRem
func sliceToInterface(s []string) []interface{} {
	new := make([]interface{}, len(s))
	for i, v := range s {
		new[i] = v
	}
	return new
}

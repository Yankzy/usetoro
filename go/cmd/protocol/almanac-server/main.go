package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/daemon"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
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
	nc  *nats.Conn
	rdb *redis.Client
	kv  nats.KeyValue // Used for charging Micrions
}

// --- Main Entry Point ---

func main() {
	// 1. Setup Logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config Load Failed: %v", err)
	}

	// CA verification has been removed as per user request.

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

	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("❌ Failed to get JetStream context: %v", err)
	}

	kv, err := micrion.SetupKV(js)
	if err != nil {
		log.Fatalf("❌ Failed to setup Micrions KV: %v", err)
	}

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
		nc:  nc,
		rdb: rdb,
		kv:  kv,
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

	// === Unified Daemon Runner Setup ===

	d := daemon.New(slog.Default(), func() (*config.Config, error) {
		return cfg, nil
	}, ":9090")

	log.Println("📖 Almanac Server & Agents Runner Online. Listening...")

	// 6. Run daemon blocking execution on the OS signal lifecycle hook securely
	if err := d.Run(context.Background()); err != nil {
		log.Fatalf("Protocol Daemon unexpectedly terminated: %v", err)
	}
}

// --- Handlers ---

func (s *AlmanacServer) handleRegister(msg *nats.Msg) {
	var payload lookup.RegistrationPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		log.Printf("⚠️ Malformed registration: %v", err)
		return
	}

	// --- STEP 1: Verify Ownership (Our Keys) ---
	pubKeyHex, err := identity.PubKeyFromDID(payload.DID)
	if err != nil {
		log.Printf("❌ Registration Rejected: Invalid DID format for %s: %v", payload.DID, err)
		return
	}

	valid, err := identity.Verify(pubKeyHex, []byte(payload.DID), payload.Signature)
	if err != nil || !valid {
		log.Printf("❌ Registration Rejected: Invalid Signature for DID %s (Err: %v)", payload.DID, err)
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
		CertSerial:   "", // Removed cert dependencies
	}

	recordBytes, _ := json.Marshal(record)
	ctx := context.Background()
	pipe := s.rdb.Pipeline()

	// Logic: Use the Expiry from the payload
	ttl := time.Until(payload.Expiry)
	if ttl < 0 {
		log.Printf("⚠️ Payload Expiry in the past for %s", payload.DID)
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

	// MicroBurn Toll Enforcement (1,616 Micrions)
	// TEMPORARY BYPASS:
	/*
		if query.CallerDID == "" {
			log.Printf("⚠️ Query empty DID, bypassing toll for now")
			s.nc.Publish(msg.Reply, []byte(`{"error": "402 Payment Required: CallerDID missing"}`))
			return
		} else {
			if _, err := micrion.MicroBurn(s.kv, query.CallerDID, micrion.InfraTollCost); err != nil {
				log.Printf("❌ Query toll failed for %s: %v", query.CallerDID, err)
				s.nc.Publish(msg.Reply, []byte(`{"error": "402 Payment Required: Insufficient Micrions"}`))
				return
			}
		}
	*/

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

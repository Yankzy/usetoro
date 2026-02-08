package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	// Import local packages
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/resolver"
)

// --- Data Structures ---

// AgentRecord is the internal storage format for the Almanac
type AgentRecord struct {
	DID          string                            `json:"did"`
	Endpoints    []string                          `json:"endpoints"`
	Capabilities []resolver.RegistrationCapability `json:"capabilities"`
	LastSeen     time.Time                         `json:"last_seen"`
	Expiry       time.Time                         `json:"expiry"`
}

// AlmanacServer holds the state and NATS connection
type AlmanacServer struct {
	nc  *nats.Conn
	rdb *redis.Client
}

// --- Main Entry Point ---

func main() {
	// 1. Connect to NATS
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 2. Connect to Redis
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("❌ Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opt)

	// Ping Redis to ensure connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}

	server := &AlmanacServer{
		nc:  nc,
		rdb: rdb,
	}

	// 3. Subscribe to Registration Heartbeats
	// Queue Group "almanac_workers" ensures load balancing if we run multiple servers
	_, err = nc.QueueSubscribe("almanac.register", "almanac_workers", server.handleRegister)
	if err != nil {
		log.Fatalf("❌ Failed to subscribe to register: %v", err)
	}

	// 4. Subscribe to Queries
	_, err = nc.QueueSubscribe("almanac.query", "almanac_workers", server.handleQuery)
	if err != nil {
		log.Fatalf("❌ Failed to subscribe to query: %v", err)
	}

	log.Println("📖 Almanac Server Online (Redis Backed). Listening for Agents...")

	// 5. Wait for Shutdown Signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("🔻 Shutting down Almanac Server...")
}

// --- Handlers ---

// handleRegister processes new agent heartbeats
func (s *AlmanacServer) handleRegister(msg *nats.Msg) {
	var payload resolver.RegistrationPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		log.Printf("⚠️ Malformed registration: %v", err)
		return
	}

	// 1. Validate Signature (Security Critical)
	_, err := identity.PubKeyFromDID(payload.DID)
	if err != nil {
		log.Printf("⚠️ Invalid DID format: %s", payload.DID)
		return
	}

	// 2. Transform to Internal Record
	record := &AgentRecord{
		DID:          payload.DID,
		Endpoints:    payload.Endpoints,
		Capabilities: payload.Capabilities, // Store full structure
		LastSeen:     time.Now().UTC(),
		Expiry:       payload.Expiry,
	}

	recordBytes, err := json.Marshal(record)
	if err != nil {
		log.Printf("⚠️ Failed to marshal record: %v", err)
		return
	}

	// 3. Write to Redis
	ctx := context.Background()
	pipe := s.rdb.Pipeline()

	// Key for Agent Record
	agentKey := "almanac:agent:" + payload.DID
	ttl := time.Until(payload.Expiry)
	if ttl < 0 {
		ttl = time.Second // Expire immediately if already past
	}

	// Store the record with TTL
	// Use Set to store the JSON string
	pipe.Set(ctx, agentKey, recordBytes, ttl)

	// Index Capabilities (Case Insensitive Indexing)
	for _, cap := range record.Capabilities {
		capKey := "almanac:cap:" + strings.ToLower(cap.Type)
		pipe.SAdd(ctx, capKey, payload.DID)
		// We don't set TTL on the Set itself because other agents might be in it.
		// We handle stale members lazily in Query.
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		log.Printf("❌ Redis error: %v", err)
		return
	}

	log.Printf("✅ Registered Agent: %s (%d capabilities)", payload.DID, len(record.Capabilities))
}

// handleQuery processes "FindAgents" requests
func (s *AlmanacServer) handleQuery(msg *nats.Msg) {
	// Parse the query
	var query resolver.AlmanacQuery
	if err := json.Unmarshal(msg.Data, &query); err != nil {
		return // Ignore bad requests
	}

	ctx := context.Background()
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

	// Send Reply
	respBytes, _ := json.Marshal(matches)
	s.nc.Publish(msg.Reply, respBytes)

	log.Printf("🔍 Query for '%s' (Meta: %v) -> Found %d agents", query.CapabilityType, query.MetaFilter, len(matches))
}

// matchMeta checks if the agent has a capability of the given type that generally matches the filters
func matchMeta(caps []resolver.RegistrationCapability, targetType string, filter map[string]interface{}) bool {
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

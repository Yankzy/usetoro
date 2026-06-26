package lookup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

const (
	almanacRegisterDLQSubject  = "almanac.register.dlq"
	almanacRegisterDeliverLimit = 5
)

// Registry manages the storage and querying of active actors.
type Registry struct {
	logger *slog.Logger
	nc     *nats.Conn
	js     nats.JetStreamContext
	mu     sync.RWMutex
	// entries maps Actor DID -> entry details.
	entries map[string]AlmanacEntry
	// ttls maps Actor DID -> expiration time.
	ttls map[string]time.Time
}

// NewRegistry initializes the Almanac backend.
func NewRegistry(logger *slog.Logger, nc *nats.Conn, js nats.JetStreamContext) *Registry {
	return &Registry{
		logger:  logger,
		nc:      nc,
		js:      js,
		entries: make(map[string]AlmanacEntry),
		ttls:    make(map[string]time.Time),
	}
}

// Start boots the Almanac listeners.
func (r *Registry) Start(ctx context.Context) error {
    // 1. Listen for registrations (heartbeats) with JetStream durability
	if r.js == nil {
		return fmt.Errorf("almanac: missing JetStream context for registration subscription")
	}
	_, err := r.js.Subscribe(core.SubjectAlmanacRegister, r.handleRegistration,
		nats.Durable("almanac-register"),
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(almanacRegisterDeliverLimit),
	)
	if err != nil {
		return fmt.Errorf("almanac: failed to subscribe to register: %w", err)
	}

	// 2. Listen for discovery queries
	_, err = r.nc.Subscribe(core.SubjectAlmanacQuery, func(msg *nats.Msg) {
		var query AlmanacQuery
		if err := json.Unmarshal(msg.Data, &query); err != nil {
			r.logger.Error("Almanac: malformed query", "error", err)
			return
		}

		r.mu.RLock()
		defer r.mu.RUnlock()

		var results []AlmanacEntry
		now := time.Now()

		for did, entry := range r.entries {
			// Skip expired entries
			if ttl, ok := r.ttls[did]; ok && ttl.Before(now) {
				continue
			}

			// Apply filters
			match := true
			if query.DID != "" && query.DID != did {
				match = false
			}
			if query.CapabilityType != "" {
				capMatch := false
				for _, cap := range entry.Capabilities {
					if cap.Type == query.CapabilityType || cap.Meta["activity_type"] == query.CapabilityType {
						capMatch = true
						break
					}
				}
				if !capMatch {
					match = false
				}
			}

			if match {
				results = append(results, entry)
			}
		}

		resp, _ := json.Marshal(results)
		msg.Respond(resp)
	})
	if err != nil {
		return fmt.Errorf("almanac: failed to subscribe to query: %w", err)
	}

	r.logger.Info("📡 Almanac Discovery Server active")

	// 3. Cleanup loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.cleanup()
		}
	}
}

func (r *Registry) handleRegistration(msg *nats.Msg) {
	md, _ := msg.Metadata()
	if md != nil && md.NumDelivered >= almanacRegisterDeliverLimit {
		r.emitRegistrationDLQ(msg, "exceeded max deliveries", md)
		return
	}

	var payload RegistrationPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		r.logger.Error("Almanac: malformed registration", "error", err)
		r.emitRegistrationDLQ(msg, "malformed payload", md)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[payload.DID] = AlmanacEntry{DID: payload.DID, Endpoints: payload.Endpoints, Capabilities: payload.Capabilities}
	expiry := payload.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Minute)
	}
	r.ttls[payload.DID] = expiry
	r.logger.Debug("Almanac: registered actor", "did", payload.DID, "caps", len(payload.Capabilities))
	if err := msg.Ack(); err != nil {
		r.logger.Error("Almanac: failed to ack registration", "error", err)
	}
}

func (r *Registry) emitRegistrationDLQ(msg *nats.Msg, reason string, md *nats.MsgMetadata) {
	payload := map[string]interface{}{
		"subject":   msg.Subject,
		"reason":    reason,
		"data":      base64.StdEncoding.EncodeToString(msg.Data),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if md != nil {
		payload["metadata"] = map[string]interface{}{
			"stream":            md.Stream,
			"consumer":          md.Consumer,
			"stream_sequence":   md.Sequence.Stream,
			"consumer_sequence": md.Sequence.Consumer,
			"num_delivered":     md.NumDelivered,
			"timestamp":         md.Timestamp,
		}
	}
	encoded, _ := json.Marshal(payload)
	if err := r.nc.Publish(almanacRegisterDLQSubject, encoded); err != nil {
		r.logger.Error("Almanac: failed to publish registration DLQ", "error", err)
	}
	if err := msg.Term(); err != nil {
		r.logger.Error("Almanac: failed to terminate registration msg after DLQ", "error", err)
	}
}

func (r *Registry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for did, ttl := range r.ttls {
		if ttl.Before(now) {
			delete(r.entries, did)
			delete(r.ttls, did)
			r.logger.Debug("Almanac: evicted expired actor", "did", did)
		}
	}
}

// GetAllEntries returns a snapshot of all active actors (for the API).
func (r *Registry) GetAllEntries() []AlmanacEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	now := time.Now()
	var entries []AlmanacEntry
	for did, entry := range r.entries {
		if ttl, ok := r.ttls[did]; ok && ttl.After(now) {
			entries = append(entries, entry)
		}
	}
	return entries
}

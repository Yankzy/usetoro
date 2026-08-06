package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/nats-io/nats.go"
)

// NatsDomainStatePersister implements ase.StatePersister by proxying domain SQL persistence calls
// over NATS Request-Reply to an external microservice, while delegating core infrastructure operations
// (such as Redis locking and active agent caching) to the underlying ASE core infrastructure store.
type NatsDomainStatePersister struct {
	domainName string
	nc         *nats.Conn
	infraStore ase.StatePersister
	timeout    time.Duration
}

// NewNatsDomainStatePersister creates a state persister that routes domain persistence over NATS.
func NewNatsDomainStatePersister(domainName string, nc *nats.Conn, infraStore ase.StatePersister, timeout time.Duration) *NatsDomainStatePersister {
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &NatsDomainStatePersister{
		domainName: domainName,
		nc:         nc,
		infraStore: infraStore,
		timeout:    timeout,
	}
}

// -----------------------------------------------------------------------------
// INFRASTRUCTURE OPERATIONS (Delegated to Core ASE Infrastructure Store)
// -----------------------------------------------------------------------------

func (s *NatsDomainStatePersister) AcquireLock(ctx context.Context, nodeID string) (bool, error) {
	if s.infraStore != nil {
		return s.infraStore.AcquireLock(ctx, nodeID)
	}
	return true, nil // Fallback
}

func (s *NatsDomainStatePersister) ReleaseLock(ctx context.Context, nodeID string) error {
	if s.infraStore != nil {
		return s.infraStore.ReleaseLock(ctx, nodeID)
	}
	return nil
}

func (s *NatsDomainStatePersister) CacheActiveAgent(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	if s.infraStore != nil {
		return s.infraStore.CacheActiveAgent(ctx, node)
	}
	return nil
}

func (s *NatsDomainStatePersister) GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState)) (*ase.AutonomousSemanticEngineNode, error) {
	if s.infraStore != nil {
		return s.infraStore.GetCachedAgent(ctx, nodeID, onStateChange)
	}
	return nil, nil
}

func (s *NatsDomainStatePersister) RemoveCachedAgent(ctx context.Context, nodeID string) error {
	if s.infraStore != nil {
		return s.infraStore.RemoveCachedAgent(ctx, nodeID)
	}
	return nil
}

// -----------------------------------------------------------------------------
// DOMAIN-SPECIFIC PERSISTENCE (Proxied over NATS to External Microservice)
// -----------------------------------------------------------------------------

func (s *NatsDomainStatePersister) PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	return s.requestStateAction(ctx, "persist_node", map[string]any{"node": node})
}

func (s *NatsDomainStatePersister) PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	return s.requestStateAction(ctx, "persist_hold", map[string]any{"node": node})
}

func (s *NatsDomainStatePersister) PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	return s.requestStateAction(ctx, "persist_ready", map[string]any{"node": node})
}

func (s *NatsDomainStatePersister) UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error {
	return s.requestStateAction(ctx, "update_state", map[string]any{"node_id": nodeID, "state": state})
}

func (s *NatsDomainStatePersister) GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error) {
	if s.nc == nil {
		return nil, fmt.Errorf("nats_domain_store: NATS connection is nil")
	}

	reqPayload := map[string]any{
		"node_id": nodeID,
		"domain":  s.domainName,
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_store: failed to marshal trace request: %w", err)
	}

	subject := fmt.Sprintf("domain.%s.state.get_trace", s.domainName)
	ctxTimeout, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	msg, err := s.nc.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_store: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Trace []byte `json:"trace"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("nats_domain_store: failed to unmarshal trace response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("nats_domain_store: remote error from %s: %s", subject, resp.Error)
	}

	return resp.Trace, nil
}

func (s *NatsDomainStatePersister) requestStateAction(ctx context.Context, action string, payload map[string]any) error {
	if s.nc == nil {
		return fmt.Errorf("nats_domain_store: NATS connection is nil")
	}

	payload["domain"] = s.domainName
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nats_domain_store: failed to marshal %s request: %w", action, err)
	}

	subject := fmt.Sprintf("domain.%s.state.%s", s.domainName, action)
	ctxTimeout, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	msg, err := s.nc.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return fmt.Errorf("nats_domain_store: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("nats_domain_store: failed to unmarshal %s response: %w", action, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("nats_domain_store: remote error from %s: %s", subject, resp.Error)
	}

	return nil
}

package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// NatsDomainProxy implements the DomainTool interface by forwarding domain operations
// over NATS Request-Reply to an external microservice (e.g. Python service).
type NatsDomainProxy struct {
	DomainName string
	Timeout    time.Duration
}

// NewNatsDomainProxy creates a new NATS-backed domain proxy for an external domain.
func NewNatsDomainProxy(domainName string) *NatsDomainProxy {
	return &NatsDomainProxy{
		DomainName: domainName,
		Timeout:    120 * time.Second,
	}
}

// BuildAgents sends a NATS request to domain.<domain_name>.agents.build
func (p *NatsDomainProxy) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	if deps.NC == nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS connection is nil")
	}

	reqPayload := map[string]any{
		"envelope": env,
		"dag_name": dagName,
		"domain":   p.DomainName,
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to marshal build_agents request: %w", err)
	}

	subject := fmt.Sprintf("domain.%s.agents.build", p.DomainName)
	timeout := p.getTimeout(deps)
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := deps.NC.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Agents []*ase.AutonomousSemanticEngineNode `json:"agents"`
		Error  string                              `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to unmarshal build_agents response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("nats_domain_proxy: remote error from %s: %s", subject, resp.Error)
	}

	return resp.Agents, nil
}

// GenerateAlertPayload sends a NATS request to domain.<domain_name>.alert.generate
func (p *NatsDomainProxy) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	if deps.NC == nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS connection is nil")
	}

	reqPayload := map[string]any{
		"node_id":   a.NodeID,
		"tenant_id": a.TenantID,
		"realm_id":  a.RealmID,
		"dag_name":  a.DagName,
		"payload":   a.Payload,
		"domain":    p.DomainName,
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to marshal alert request: %w", err)
	}

	subject := fmt.Sprintf("domain.%s.alert.generate", p.DomainName)
	timeout := p.getTimeout(deps)
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := deps.NC.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Payload map[string]interface{} `json:"payload"`
		Error   string                 `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to unmarshal alert response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("nats_domain_proxy: remote error from %s: %s", subject, resp.Error)
	}

	return resp.Payload, nil
}

// ResumeAgent sends a NATS request to domain.<domain_name>.agents.resume
func (p *NatsDomainProxy) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	if deps.NC == nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS connection is nil")
	}

	reqPayload := map[string]any{
		"node_id":  nodeID,
		"dag_name": dagName,
		"domain":   p.DomainName,
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to marshal resume request: %w", err)
	}

	subject := fmt.Sprintf("domain.%s.agents.resume", p.DomainName)
	timeout := p.getTimeout(deps)
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := deps.NC.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Agent *ase.AutonomousSemanticEngineNode `json:"agent"`
		Error string                            `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("nats_domain_proxy: failed to unmarshal resume response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("nats_domain_proxy: remote error from %s: %s", subject, resp.Error)
	}

	return resp.Agent, nil
}

// GetBacktrackingInstructions sends a NATS request to domain.<domain_name>.backtracking.instructions
func (p *NatsDomainProxy) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	return "System instructions for backtracking", newContext
}

// GetClassifier returns a NATS-backed domain classifier.
func (p *NatsDomainProxy) GetClassifier(deps ToolDependencies) ase.Classifier {
	return NewNatsDomainClassifier(p.DomainName, deps.NC, p.getTimeout(deps))
}

// GetStatePersister returns a NATS-backed state persister.
func (p *NatsDomainProxy) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	return NewNatsDomainStatePersister(p.DomainName, deps.NC, deps.Store, p.getTimeout(deps))
}

func (p *NatsDomainProxy) getTimeout(deps ToolDependencies) time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 120 * time.Second
}

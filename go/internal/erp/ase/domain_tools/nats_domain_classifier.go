package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/nats-io/nats.go"
)

// NatsDomainClassifier implements ase.Classifier by forwarding think batch evaluations
// to an external domain microservice over NATS Request-Reply.
type NatsDomainClassifier struct {
	domainName string
	nc         *nats.Conn
	timeout    time.Duration
}

// NewNatsDomainClassifier creates a classifier that dispatches batch evaluation tasks over NATS.
func NewNatsDomainClassifier(domainName string, nc *nats.Conn, timeout time.Duration) *NatsDomainClassifier {
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &NatsDomainClassifier{
		domainName: domainName,
		nc:         nc,
		timeout:    timeout,
	}
}

func (c *NatsDomainClassifier) BuildGenericThinkFunc(promptKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		return c.dispatchThink(ctx, "generic", promptKey, batch)
	}
}

func (c *NatsDomainClassifier) BuildDynamicThinkFunc(provider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		return c.dispatchThink(ctx, "dynamic", provider, batch)
	}
}

func (c *NatsDomainClassifier) BuildPayloadRouterThinkFunc(payloadKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		return c.dispatchThink(ctx, "router", payloadKey, batch)
	}
}

func (c *NatsDomainClassifier) dispatchThink(ctx context.Context, mode string, key string, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
	if c.nc == nil {
		return nil, fmt.Errorf("nats_domain_classifier: NATS connection is nil")
	}

	reqPayload := map[string]any{
		"mode":        mode,
		"key":         key,
		"domain_name": c.domainName,
		"batch":       batch,
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_classifier: failed to marshal think request: %w", err)
	}

	subject := fmt.Sprintf("domain.%s.classify.%s", c.domainName, mode)
	ctxTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	msg, err := c.nc.RequestWithContext(ctxTimeout, subject, reqBytes)
	if err != nil {
		return nil, fmt.Errorf("nats_domain_classifier: NATS request failed for %s: %w", subject, err)
	}

	var resp struct {
		Results map[string]ase.NodeClassification `json:"results"`
		Error   string                            `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("nats_domain_classifier: failed to unmarshal think response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("nats_domain_classifier: remote error from %s: %s", subject, resp.Error)
	}

	return resp.Results, nil
}

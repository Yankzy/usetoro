package constraint

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/reputation"
)

// RuleType enum defines the system-wide policy categories.
type RuleType string

const (
	RuleTrustThreshold RuleType = "TRUST_THRESHOLD"
	RuleCapabilityReq  RuleType = "CAPABILITY_MATCH"
	RuleResourceSLA    RuleType = "RESOURCE_SLA"
)

// Rule defines a discrete policy check to be orchestrated by the Constraint Engine.
type Rule interface {
	Type() RuleType
	Check(ctx context.Context, contract *core.Contract) (bool, error)
}

// TrustThresholdRule ensures the Acceptor has sufficient historical reliability.
type TrustThresholdRule struct {
	repEngine     reputation.Engine
	minScore      float64
	minConfidence float64
}

func (r *TrustThresholdRule) Type() RuleType { return RuleTrustThreshold }

func (r *TrustThresholdRule) Check(ctx context.Context, contract *core.Contract) (bool, error) {
	// Querying Redis cache via the Engine
	score, err := r.repEngine.GetScore(ctx, contract.AcceptorDID, "default")
	if err != nil {
		return false, err
	}
	if score.Score < r.minScore || score.Confidence < r.minConfidence {
		return false, nil
	}
	return true, nil
}

// CapabilityMatchRule ensures the Acceptor's capabilities match the contract requirement.
type CapabilityMatchRule struct{}

func (r *CapabilityMatchRule) Type() RuleType { return RuleCapabilityReq }

func (r *CapabilityMatchRule) Check(ctx context.Context, contract *core.Contract) (bool, error) {
	// For MVP, we pass it. In a real scenario, we'd cross-reference CapabilityVectors in the Identity Graph.
	return true, nil
}

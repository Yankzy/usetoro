package domain_tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/redis/go-redis/v9"
)

type MarketingStore struct {
	redis  *redis.Client
	cfg    *config.Config
	client *http.Client
}

func NewMarketingStore(rdb *redis.Client, cfg *config.Config) *MarketingStore {
	return &MarketingStore{
		redis:  rdb,
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *MarketingStore) PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	// Transient nodes don't have a DB table, just update cache
	return s.CacheActiveAgent(ctx, node)
}

func (s *MarketingStore) PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	return s.CacheActiveAgent(ctx, node)
}

func (s *MarketingStore) PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	// 1. Check intent
	intent := ""
	if topMacro := node.TopCandidate("macro_intent"); topMacro != nil {
		intent = topMacro.Value
	}

	// Only send outbound email for specific intents
	if intent != "DEMO_REQUEST" && intent != "COLD_OUTREACH" && intent != "ACCOUNT_BASED_MARKETING" && intent != "WIN_BACK_CAMPAIGN" {
		return nil
	}

	// Generate Mailpool Payload
	recipient := ""
	if email, ok := node.Payload["email"].(string); ok {
		recipient = email
	} else if email, ok := node.Payload["recipient"].(string); ok {
		recipient = email
	}

	if recipient == "" {
		return fmt.Errorf("marketing_store: recipient email missing in payload")
	}

	// This assumes the Mailpool API format for sending emails
	payload := map[string]interface{}{
		"to": []map[string]string{
			{"email": recipient},
		},
		"subject": "Toro Outreach",
		"html":    "<p>Hello, this is an automated outreach from Toro.</p>",
	}
	
	// Add context reasoning to email for debugging/testing purposes
	if topMacro := node.TopCandidate("macro_intent"); topMacro != nil {
		payload["html"] = fmt.Sprintf("<p>Intent: %s</p><p>Reasoning: %s</p>", intent, topMacro.Reasoning)
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.MailpoolEndpoint+"/v1/emails", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.MailpoolAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mailpool API error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("mailpool API failed with status %d", resp.StatusCode)
	}

	return nil
}

func (s *MarketingStore) CacheActiveAgent(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	b, err := json.Marshal(node)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, activeAgentPrefix+node.NodeID, b, 15*time.Minute).Err()
}

func (s *MarketingStore) GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState)) (*ase.AutonomousSemanticEngineNode, error) {
	b, err := s.redis.Get(ctx, activeAgentPrefix+nodeID).Bytes()
	if err != nil {
		return nil, err
	}
	var node ase.AutonomousSemanticEngineNode
	if err := json.Unmarshal(b, &node); err != nil {
		return nil, err
	}
	node.InitInternalState()
	if onStateChange != nil {
		node.SetOnStateChange(onStateChange)
	}
	return &node, nil
}

func (s *MarketingStore) RemoveCachedAgent(ctx context.Context, nodeID string) error {
	return s.redis.Del(ctx, activeAgentPrefix+nodeID).Err()
}

func (s *MarketingStore) AcquireLock(ctx context.Context, nodeID string) (bool, error) {
	return s.redis.SetNX(ctx, lockPrefix+nodeID, "1", 30*time.Second).Result()
}

func (s *MarketingStore) ReleaseLock(ctx context.Context, nodeID string) error {
	return s.redis.Del(ctx, lockPrefix+nodeID).Err()
}

func (s *MarketingStore) GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error) {
	node, err := s.GetCachedAgent(ctx, nodeID, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(node.ExecutionTrace)
}

func (s *MarketingStore) UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error {
	node, err := s.GetCachedAgent(ctx, nodeID, nil)
	if err != nil {
		return err
	}
	node.CurrentState = state
	return s.CacheActiveAgent(ctx, node)
}

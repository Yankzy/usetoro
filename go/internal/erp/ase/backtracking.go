package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
)

// AutomatedBacktrackResponse represents the structural JSON response from the LLM
// indicating the node to backtrack to.
type AutomatedBacktrackResponse struct {
	TargetDAGNodeID string `json:"target_dag_node_id"`
	ExtractedRule   string `json:"extracted_rule,omitempty"`
	RuleKeyword     string `json:"rule_keyword,omitempty"`
}

// AutomatedBacktrackingPrompt is the system prompt used to instruct the LLM
// on how to analyze human context and identify erroneous past classification decisions.
const AutomatedBacktrackingPrompt = `You are an Autonomous Backtracking Agent.
A micro-agent was placed on HOLD. A human has provided new context correcting a past assumption.

You will be provided with:
1. Domain-specific context.
2. New Human Context: The correction provided by the human.
3. Execution Trace: A list of the steps and decisions the agent made.

Analyze the new context against the execution trace. 
Identify the FIRST step in the trace that made a decision directly contradicted by the human context.
%s
You must return a JSON object with exactly one key "target_dag_node_id" containing the ID of the earliest wrong step. 
If the trace is correct and the context just helps the CURRENT stuck step, return "NONE".
`

// AutomatedBacktrackAndResume injects human context, identifies the mistake, backtracks, and resumes execution.
func (cs *ClassifierService) AutomatedBacktrackAndResume(ctx context.Context, node *AutonomousSemanticEngineNode, newContext string, dag *DAG, store StatePersister, domainSystemPrompt string, userPrompt string) error {
	node.Mu.Lock()
	node.ContextUpdates = append(node.ContextUpdates, newContext)
	
	traceCopy := make([]NodeExecutionStep, len(node.ExecutionTrace))
	copy(traceCopy, node.ExecutionTrace)
	node.Mu.Unlock()

	if cs.rt == nil {
		// Fallback if no local LLM client is available.
		// For simplicity, we just resume without backtracking, or we could dispatch via NATS.
		cs.logger.Warn("llm client required for automated backtracking, skipping backtrack phase")
		return node.Resume(ctx, dag, store, "")
	}

	systemPrompt := fmt.Sprintf(AutomatedBacktrackingPrompt, domainSystemPrompt)

	var resp AutomatedBacktrackResponse
	respStr, err := cs.rt.Exec(ctx, userPrompt, systemPrompt)
	if err != nil {
		return fmt.Errorf("automated backtracking llm call failed: %w", err)
	}

	cleanResp := strings.TrimSpace(respStr)
	if strings.HasPrefix(cleanResp, "```json") {
		cleanResp = strings.TrimPrefix(cleanResp, "```json")
		cleanResp = strings.TrimSuffix(cleanResp, "```")
	} else if strings.HasPrefix(cleanResp, "```") {
		cleanResp = strings.TrimPrefix(cleanResp, "```")
		cleanResp = strings.TrimSuffix(cleanResp, "```")
	}
	cleanResp = strings.TrimSpace(cleanResp)

	if err := json.Unmarshal([]byte(cleanResp), &resp); err != nil {
		return fmt.Errorf("automated backtracking json parse failed: %w", err)
	}

	if resp.ExtractedRule != "" && cs.db != nil {
		err := cs.db.CreateMemoryRule(ctx, database.CreateMemoryRuleParams{
			RealmID:     node.TenantID,
			EntityValue: resp.RuleKeyword,
			Instruction: resp.ExtractedRule,
		})
		if err != nil {
			cs.logger.Error("failed to save extracted memory rule", "node_id", node.NodeID, "error", err)
		} else {
		}
	}

	if resp.TargetDAGNodeID != "NONE" && resp.TargetDAGNodeID != "" {
		if err := node.Backtrack(resp.TargetDAGNodeID); err != nil {
			return fmt.Errorf("failed to backtrack to %s: %w", resp.TargetDAGNodeID, err)
		}
		return node.Resume(ctx, dag, store, resp.TargetDAGNodeID)
	}

	// If no backtrack is needed (NONE), check whether the current hold gate has a
	// resume child. If so, resume directly there instead of looping through the gate
	// that originally trapped the agent.
	node.Mu.RLock()
	lastStepDAGNodeID := ""
	if len(node.ExecutionTrace) > 0 {
		lastStepDAGNodeID = node.ExecutionTrace[len(node.ExecutionTrace)-1].DAGNodeID
	}
	node.Mu.RUnlock()

	if lastStepDAGNodeID != "" {
		lastNode := dag.GetNode(lastStepDAGNodeID)
		if lastNode != nil {
			if resumeChild := lastNode.ResumeChild(); resumeChild != nil {
				resumeChild.Accept(node)
				return nil
			}
		}
	}

	return node.Resume(ctx, dag, store, "")
}

// Backtrack rewinds the agent's state to a previous step in its ExecutionTrace.
// It removes any classification candidates set at or after that step, updates entropy,
// truncates the trace, and prepares the node to be re-injected.
func (n *AutonomousSemanticEngineNode) Backtrack(targetDAGNodeID string) error {
	n.Mu.Lock()
	defer n.Mu.Unlock()

	targetIndex := -1
	for i, step := range n.ExecutionTrace {
		if step.DAGNodeID == targetDAGNodeID {
			targetIndex = i
			break
		}
	}

	if targetIndex == -1 {
		return fmt.Errorf("target node %s not found in execution trace", targetDAGNodeID)
	}

	// Identify all properties classified from the target step onwards.
	for i := targetIndex; i < len(n.ExecutionTrace); i++ {
		key := n.ExecutionTrace[i].PropertyKey
		if key == "" {
			key = n.ExecutionTrace[i].Kind
		}
		delete(n.Candidates, key)
		delete(n.PropertyEntropies, key)
	}

	// Truncate the trace so the next execution appends cleanly.
	n.ExecutionTrace = n.ExecutionTrace[:targetIndex]

	// Reset state variables.
	n.CurrentState = StateTriaging
	n.HoldReason = ""

	// Recalculate unified confidence with dynamic denominator.
	var sumConfidence float64
	for _, candidates := range n.Candidates {
		if len(candidates) > 0 {
			best := candidates[0]
			for i := 1; i < len(candidates); i++ {
				if candidates[i].Confidence > best.Confidence {
					best = candidates[i]
				}
			}
			sumConfidence += best.Confidence
		}
	}

	totalProperties := len(n.Candidates)
	if totalProperties < 4 {
		totalProperties = 4
	}

	n.UnifiedConfidence = sumConfidence / float64(totalProperties)
	n.UpdatedAt = time.Now().UTC()

	return nil
}

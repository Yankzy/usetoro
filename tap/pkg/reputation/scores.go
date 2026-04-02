package reputation

import "time"

// ReputationScore is a multi-dimensional measurement of an agent's reliability
// within a specific capability domain context. It is not a broad scalar.
type ReputationScore struct {
	AgentDID   string    `json:"agent_did"`
	Capability string    `json:"capability"` // The specific domain (e.g., "logistics", "bookkeeping")
	Score      float64   `json:"score"`      // Generally bound between 0.0 to 1.0 or 0 to 100
	Confidence float64   `json:"confidence"` // Bound between 0.0 to 1.0 (Higher confidence with more interactions)
	LastUpdated time.Time `json:"last_updated"`
}

// Evaluation encapsulates feedback from a task execution or dispute.
type Evaluation struct {
	TaskID    string  `json:"task_id"`
	Submitter string  `json:"submitter_did"`
	Target    string  `json:"target_did"`
	Outcome   string  `json:"outcome"`     // e.g., "SUCCESS", "FAILED", "DISPUTED_LOST"
	Weight    float64 `json:"weight"`      // Multiplier depending on task complexity/reward
	Timestamp int64   `json:"timestamp"`
}

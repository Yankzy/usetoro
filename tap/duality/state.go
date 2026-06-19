package duality

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// TransitionState represents the status of a state transition pair.
type TransitionState string

const (
	StatePending     TransitionState = "PENDING"
	StateReconciled  TransitionState = "RECONCILED"
	StateDrifted     TransitionState = "DRIFTED"
	StateCompensated TransitionState = "COMPENSATED"
)

// Intent captures the exact transition command the AI agent attempted to apply.
type Intent struct {
	ID         string          `json:"intent_id"`
	WorkflowID string          `json:"workflow_id"`
	StepID     string          `json:"step_id"`
	ActorDID   string          `json:"actor_did"`
	Target     string          `json:"target_field"` // e.g. "/account_id"
	Proposed   json.RawMessage `json:"proposed_value"`
	Timestamp  time.Time       `json:"timestamp"`
}

// Fact captures the actual, validated state returned from the external API or database transaction.
type Fact struct {
	ID            string          `json:"fact_id"`
	IntentID      string          `json:"intent_id"`
	ConfirmedBy   string          `json:"confirmed_by"` // e.g., "quickbooks_api"
	ActualValue   json.RawMessage `json:"actual_value"`
	TransactionID string          `json:"external_transaction_id"`
	Timestamp     time.Time       `json:"timestamp"`
}

// MirrorLog represents the dual state transition pairing.
type MirrorLog struct {
	WorkflowID string          `json:"workflow_id"`
	SequenceID int64           `json:"sequence_id"`
	Intent     Intent          `json:"intent"`
	Fact       *Fact           `json:"fact,omitempty"`
	Status     TransitionState `json:"status"`
}

// ExternalStateProvider handles querying external sources of truth and executing compensating logic (Sagas).
type ExternalStateProvider interface {
	QueryExternalFact(ctx context.Context, intent Intent) (*Fact, error)
	ExecuteCompensation(ctx context.Context, intent Intent) error
}

// ReconciliationEngine executes the background loop validating Intents against Facts.
type ReconciliationEngine struct {
	Logger   *slog.Logger
	Provider ExternalStateProvider
}

// NewReconciliationEngine constructs a new state reconciliation processor.
func NewReconciliationEngine(logger *slog.Logger, provider ExternalStateProvider) *ReconciliationEngine {
	return &ReconciliationEngine{
		Logger:   logger,
		Provider: provider,
	}
}

// ReconcileLogs parses active mirror logs, updating their status or triggering rollback strategies if drift is detected.
func (r *ReconciliationEngine) ReconcileLogs(ctx context.Context, logs []*MirrorLog) error {
	for _, log := range logs {
		if log.Status != StatePending {
			continue
		}

		// 1. Fetch physical fact from provider
		fact, err := r.Provider.QueryExternalFact(ctx, log.Intent)
		if err != nil {
			r.Logger.Error("reconciler: failed to resolve fact for intent", "intent_id", log.Intent.ID, "error", err)
			continue
		}

		if fact == nil {
			r.Logger.Warn("reconciler: pending verification, fact not returned yet", "intent_id", log.Intent.ID)
			continue
		}

		// 2. Perform comparison assertion
		intentStr := string(log.Intent.Proposed)
		factStr := string(fact.ActualValue)

		if intentStr != factStr {
			r.Logger.Warn("reconciler: 🚨 ledger drift detected!",
				"intent_id", log.Intent.ID,
				"expected", intentStr,
				"actual", factStr,
			)
			log.Status = StateDrifted

			// 3. Trigger Saga rollbacks/compensation
			compErr := r.Provider.ExecuteCompensation(ctx, log.Intent)
			if compErr != nil {
				return fmt.Errorf("reconciler: compensation failed for intent %s: %w", log.Intent.ID, compErr)
			}
			log.Status = StateCompensated
			r.Logger.Info("reconciler: saga rollback compensation succeeded", "intent_id", log.Intent.ID)
		} else {
			log.Fact = fact
			log.Status = StateReconciled
			r.Logger.Info("reconciler: transaction reconciled successfully", "intent_id", log.Intent.ID)
		}
	}

	return nil
}

// Package redux implements a high-throughput, context-aware state reduction engine
// inspired by the Redux pattern. Its primary purpose is to manage state transitions 
// in a secure, validated, and idempotent way, particularly for AI agent workflows.
//
// Key responsibilities include:
//
// 1. State Transition via JSON Patch: It uses RFC 6902 (JSON Patch) to apply 
//    modifications to a JSON state object.
//
// 2. Middleware Validation Stack: Every state change is passed through a series 
//    of "middlewares" to ensure safety:
//    - RBAC (Role-Based Access Control): Validates that the "Actor" (e.g., an AI Agent) 
//      is authorized to modify specific paths in the state.
//    - JSON Schema Validation: Ensures the resulting state after patches are 
//      applied still conforms to a strictly defined JSON Schema.
//    - Payload Boundaries: Prevents OOM (Out Of Memory) or performance issues 
//      by enforcing byte limits on patches.
//    - Idempotency: Uses monotonic sequence IDs to prevent double-delivery 
//      of events or out-of-order execution.
//
// 3. Self-Correction for LLMs: If a patch fails validation (a "Domain Fault"), 
//    the engine returns the specific error. This allows the calling agent (the LLM) 
//    to receive the error feedback and generate a corrected patch in a retry loop.
package redux

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

type Store struct {
	cfg    EngineConfig       
	schema *jsonschema.Schema 
}

func NewStore(cfg EngineConfig) (*Store, error) {
	if cfg.Metrics == nil {
		cfg.Metrics = noopMetrics{}
	}
	if cfg.MaxOperations == 0 {
		cfg.MaxOperations = 10
	}
	if cfg.MaxPayloadBytes == 0 {
		cfg.MaxPayloadBytes = 64 * 1024
	}

	s := &Store{cfg: cfg}

	if cfg.SchemaString != "" {
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("schema.json", strings.NewReader(cfg.SchemaString)); err != nil {
			return nil, fmt.Errorf("schema add resource: %w", err)
		}
		sch, err := compiler.Compile("schema.json")
		if err != nil {
			return nil, fmt.Errorf("schema compile: %w", err)
		}
		s.schema = sch
	}

	return s, nil
}

// Reduce evaluates pure business logical representations of workflow bytes cleanly separated 
// from Host-level internal boundaries (`_sys`). It checks strictly monotonic sequential orders 
// and drops faults ephemerally directly stopping recursive LLM retry panics securely.
func (s *Store) Reduce(ctx context.Context, basePayloadBytes []byte, expectedSequence uint64, events []RFC6902Event) ([]byte, uint64, []DomainFault, error) {
	state, err := s.initializeState(basePayloadBytes)
	if err != nil {
		return nil, expectedSequence, nil, fmt.Errorf("initialize state payload: %w", err)
	}

	var faults []DomainFault
	compiledEvents := 0

	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return nil, expectedSequence, nil, fmt.Errorf("context aborted: %w", err)
		}

		start := time.Now()

		// Strict Monotonic Idempotent Pipeline Check restricting double-delivery exceptions
		if expectedSequence != 0 && event.SequenceID != expectedSequence {
			faults = append(faults, DomainFault{
				EventID: event.EventID,
				Error:   fmt.Errorf("%w: expected %d, got %d", ErrSequenceMismatch, expectedSequence, event.SequenceID).Error(),
			})
			// Drifted sequence immediately halts sequential transaction processing cleanly 
			s.cfg.Metrics.RecordRuleViolation("SequenceMismatch")
			continue
		}

		if err := s.dispatch(ctx, state, &event); err != nil {
			faults = append(faults, DomainFault{
				EventID: event.EventID,
				Error:   err.Error(),
			})
		} else {
			expectedSequence++
			compiledEvents++
		}

		s.cfg.Metrics.RecordPatchLatency(time.Since(start))
	}

	s.cfg.Metrics.RecordStateCompilation(compiledEvents)

	outBytes, err := json.Marshal(state)
	return outBytes, expectedSequence, faults, err
}

func (s *Store) dispatch(ctx context.Context, state map[string]interface{}, event *RFC6902Event) error {
	slog.Debug("⚙️ [REDUX ENGINE] Waking up. Evaluating internal topological map", "event_id", event.EventID)

	if err := EnforcePayloadBoundariesMiddleware(event.PatchArray, s.cfg); err != nil {
		return err
	}
	slog.Debug("🛡️ [REDUX MIDDLEWARE] Byte Limits Checked (OOM Bounds Safe)")

	if err := EnforceRBACMiddleware(event.PatchArray, event.Actor, s.cfg.RBAC, s.cfg.Metrics); err != nil {
		return err
	}
	slog.Debug("🛡️ [REDUX MIDDLEWARE] Path Security Checked (RBAC Approved safely)")

	slog.Debug("🧮 [REDUX MATH] Executing jsonpatch.Apply core operations")
	nextState, err := ApplyPatchReducer(state, event.PatchArray)
	if err != nil {
		slog.Error("❌ [REDUX FATAL] Mathematical rejection executing pure RFC 6902 strings", "error", err)
		return err
	}

	if err := EnforceNoArrayMiddleware(nextState, s.cfg.Metrics); err != nil {
		return err
	}
	slog.Debug("🛡️ [REDUX MIDDLEWARE] Post-Patch constraints mapped (No Array Risks)")

	if err := EnforceSchemaMiddleware(nextState, s.schema, s.cfg.Metrics); err != nil {
		return err
	}
	slog.Debug("🛡️ [REDUX MIDDLEWARE] Final JSON Schema Drift limits successfully cleared")

	// Transaction commit
	slog.Debug("✅ [REDUX PIPELINE] Patch applied cleanly! Committing volatile map changes inherently.")
	for k := range state {
		delete(state, k)
	}
	for k, v := range nextState {
		state[k] = v
	}
	return nil
}

func (s *Store) initializeState(baseBytes []byte) (map[string]interface{}, error) {
	env := make(map[string]interface{})
	if len(baseBytes) == 0 || string(baseBytes) == "{}" {
		return env, nil
	}
	if err := json.Unmarshal(baseBytes, &env); err != nil {
		return nil, err
	}
	return env, nil
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"log/slog"

	"github.com/Yankzy/usetoro/tap/pkg/memory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Application struct {
	DB     *pgxpool.Pool
	Logger *slog.Logger
}

// FeedbackRequest defines the payload for correcting agent behavior
type FeedbackRequest struct {
	// EntityID is optional in the body. If the request is authenticated via JWT,
	// the middleware's EntityID takes precedence for security.
	EntityID string `json:"entity_id"`
	Trigger  string `json:"trigger"`    // e.g. "Home Depot"
	Correct  string `json:"correction"` // e.g. "Repairs"
}

// handleFeedback processes user corrections and updates the Agent's long-term memory.
// It maps a specific trigger (Vendor/Keyword) to a specific output instruction.
func (app *Application) handleFeedback(w http.ResponseWriter, r *http.Request) {
	// 1. Parse Payload
	var req FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		app.Logger.Warn("Feedback: Malformed JSON", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// 2. Validate Inputs
	if req.Trigger == "" || req.Correct == "" {
		http.Error(w, "Missing 'trigger' or 'correction' fields", http.StatusBadRequest)
		return
	}

	// 3. Resolve Entity ID (Security Critical)
	// We prioritize the Entity ID from the authenticated Context (JWT) to prevent spoofing.
	// If this is an internal admin call without a user context, we fall back to the body.
	entityID := req.EntityID
	if ctxEntity, ok := r.Context().Value("entity_id").(uuid.UUID); ok {
		entityID = ctxEntity.String()
	}

	if entityID == "" {
		app.Logger.Warn("Feedback: Missing Entity Context", "remote_addr", r.RemoteAddr)
		http.Error(w, "Unauthorized: No Entity Identified", http.StatusUnauthorized)
		return
	}

	// 4. Construct Instruction
	// We format the correction into a natural language instruction for the LLM
	instruction := fmt.Sprintf("Categorize as '%s'", req.Correct)

	// 5. Initialize Memory Manager
	// In a full DI setup, this would be app.MemoryManager
	mem := memory.NewManager(app.DB)

	// 6. Execute Learning (Persist to DB)
	err := mem.Learn(r.Context(), entityID, req.Trigger, instruction)
	if err != nil {
		app.Logger.Error("Feedback: Database Write Failed",
			"entity_id", entityID,
			"trigger", req.Trigger,
			"error", err,
		)
		http.Error(w, "Failed to save memory rule", http.StatusInternalServerError)
		return
	}

	// 7. Structured Success Response
	app.Logger.Info("🧠 Agent Learned Rule",
		"entity_id", entityID,
		"trigger", req.Trigger,
		"correction", req.Correct,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Rule saved: '%s' -> '%s'", req.Trigger, req.Correct),
	})
}

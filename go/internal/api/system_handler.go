package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/tap/pkg/lookup"
)

// HandleListActors returns the list of all active Agents and Workers discovered via the Almanac.
// GET /v1/system/actors
func (h *Handler) HandleListActors(w http.ResponseWriter, r *http.Request) {
	if h.NATS == nil {
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "NATS infrastructure unavailable")
		return
	}

	// 1. Build an Almanac Client
	// We use "api-gateway" as the DID for discovery purposes.
	client := lookup.New(h.NATS.Conn(), "did:toro:system:api-gateway")

	// 2. Query all actors
	// An empty query to FindAgents (with empty capability) returns all in our implementation.
	actors, err := client.FindAgents("", 2*time.Second)
	if err != nil {
		h.Logger.Error("Failed to query Almanac", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Discovery service timeout")
		return
	}

	// 3. Return results
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"total":  len(actors),
		"actors": actors,
	}); err != nil {
		h.Logger.Error("Failed to encode actors list", "error", err)
	}
}

// HandleGetWorkflowStatus returns the live state of a workflow instance, hydrated with its blueprint.
// GET /v1/workflows/:id
func (h *Handler) HandleGetWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		JSONError(w, h.Logger, http.StatusBadRequest, "Invalid workflow ID format")
		return
	}

	// 1. Fetch from DB
	wf, err := h.DB.GetWorkflow(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		h.Logger.Error("Workflow not found", "id", idStr, "error", err)
		JSONError(w, h.Logger, http.StatusNotFound, "Workflow not found")
		return
	}

	// 2. Extract workflow name from state JSONB
	var state map[string]interface{}
	if err := json.Unmarshal(wf.State, &state); err != nil {
		h.Logger.Error("Failed to unmarshal workflow state", "error", err)
		JSONError(w, h.Logger, http.StatusInternalServerError, "Corrupt workflow state")
		return
	}

	wfDefName, _ := state["workflow_def"].(string)

	// 3. Fetch full blueprint from Orchestrator via NATS
	var blueprint map[string]interface{}
	if h.NATS != nil && wfDefName != "" {
		msg, err := h.NATS.Conn().Request("workflow.query.blueprint", []byte(wfDefName), 1*time.Second)
		if err == nil {
			json.Unmarshal(msg.Data, &blueprint)
		} else {
			h.Logger.Warn("Failed to fetch blueprint from Orchestrator", "error", err)
		}
	}

	// 4. Resolve actors for the visualizer
	candidates := h.discoverActorsForBlueprint(blueprint)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"instance_id": idStr,
		"entity_id":   uuid.UUID(wf.EntityID.Bytes).String(),
		"status":      state["status"],
		"state":       state,
		"blueprint":   blueprint,
		"actors":      candidates,
	}); err != nil {
		h.Logger.Error("Failed to encode workflow status", "error", err)
	}
}

// discoverActorsForBlueprint queries the Almanac for all activity types mentioned in a workflow.
func (h *Handler) discoverActorsForBlueprint(blueprint map[string]interface{}) map[string][]lookup.AlmanacEntry {
	candidates := make(map[string][]lookup.AlmanacEntry)
	if blueprint == nil {
		return candidates
	}

	steps, ok := blueprint["steps"].([]interface{})
	if !ok {
		return candidates
	}

	client := lookup.New(h.NATS.Conn(), "did:toro:system:api-gateway")

	for _, s := range steps {
		step, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		actType, _ := step["activity_type"].(string)
		if actType == "" || candidates[actType] != nil {
			continue
		}

		actors, err := client.FindAgents(actType, 1*time.Second)
		if err == nil {
			candidates[actType] = actors
		}
	}
	return candidates
}

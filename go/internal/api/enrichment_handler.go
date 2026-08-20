package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/google/uuid"
)

var (
	enrichmentEngineInstance *enrichment.MoroccanEnrichmentEngine
	enrichmentEngineOnce     sync.Once
)

// getEnrichmentEngine provides a thread-safe singleton instance of the MoroccanEnrichmentEngine.
func getEnrichmentEngine() *enrichment.MoroccanEnrichmentEngine {
	enrichmentEngineOnce.Do(func() {
		enrichmentEngineInstance = enrichment.NewMoroccanEnrichmentEngine()
	})
	return enrichmentEngineInstance
}

// HandleMoroccanEnrichment handles POST /api/v1/enrichment/morocco
func (h *Handler) HandleMoroccanEnrichment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req enrichment.EnrichmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.RequestID == "" {
		req.RequestID = "req_" + uuid.New().String()
	}

	engine := getEnrichmentEngine()
	resp := engine.EnrichBatch(r.Context(), req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleGetForeignProviderRunningTotals handles GET /api/v1/enrichment/morocco/foreign-totals
func (h *Handler) HandleGetForeignProviderRunningTotals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	realmID := r.URL.Query().Get("realm_id")
	providerIDStr := r.URL.Query().Get("provider_id")

	if realmID == "" || providerIDStr == "" {
		http.Error(w, "Missing required query parameters 'realm_id' and 'provider_id'", http.StatusBadRequest)
		return
	}

	providerID, err := uuid.Parse(providerIDStr)
	if err != nil {
		http.Error(w, "Invalid provider_id UUID", http.StatusBadRequest)
		return
	}

	engine := getEnrichmentEngine()
	totals := engine.GetForeignTracker().GetRunningTotals(r.Context(), realmID, providerID, time.Now())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(totals)
}

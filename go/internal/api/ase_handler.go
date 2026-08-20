package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/debug"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type aseConfigJSON struct {
	ID              pgtype.UUID        `json:"id"`
	UserID          pgtype.UUID        `json:"user_id"`
	Name            string             `json:"name"`
	DagConfig       []byte             `json:"dag_config,omitempty"`
	HyperParameters []byte             `json:"hyper_parameters,omitempty"`
	Prompts         []byte             `json:"prompts,omitempty"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
	UpdatedAt       pgtype.Timestamptz `json:"updated_at"`
}

func (h *Handler) HandleListASEConfigs(rw http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = r.URL.Query().Get("tenant_id")
	}

	var configs []database.ToroCoreAseDag
	var err error

	if userID == "" {
		configs, err = h.DB.ListAllASEConfigs(r.Context())
	} else {
		uid, parseErr := uuid.Parse(userID)
		if parseErr != nil {
			http.Error(rw, "invalid user_id", http.StatusBadRequest)
			return
		}
		configs, err = h.DB.ListASEConfigsByUser(r.Context(), pgtype.UUID{Bytes: uid, Valid: true})
	}

	if err != nil {
		h.Logger.Error("failed to list configs", "error", err)
		http.Error(rw, "failed to list configs", http.StatusInternalServerError)
		return
	}

	resp := make([]aseConfigJSON, len(configs))
	for i, c := range configs {
		resp[i] = aseConfigJSON{
			ID:              c.ID,
			UserID:          c.UserID,
			Name:            c.Name,
			DagConfig:       c.DagConfig,
			HyperParameters: c.HyperParameters,
			Prompts:         c.Prompts,
			CreatedAt:       c.CreatedAt,
			UpdatedAt:       c.UpdatedAt,
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(resp)
}

func (h *Handler) HandleGetASEConfig(rw http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = r.URL.Query().Get("tenant_id")
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(rw, "name query parameter is required", http.StatusBadRequest)
		return
	}

	var dbRow database.ToroCoreAseDag
	var err error

	if userID != "" {
		uid, parseErr := uuid.Parse(userID)
		if parseErr == nil {
			dbRow, err = h.DB.GetASEConfigByUser(r.Context(), database.GetASEConfigByUserParams{
				UserID: pgtype.UUID{Bytes: uid, Valid: true},
				Name:   name,
			})
		} else {
			err = fmt.Errorf("invalid user UUID")
		}
	} else {
		dbRow, err = h.DB.GetASEConfigGlobalByName(r.Context(), name)
	}

	if err != nil {
		http.Error(rw, "config not found", http.StatusNotFound)
		return
	}

	var cfg struct {
		ID              pgtype.UUID     `json:"id"`
		Prompts         json.RawMessage `json:"prompts"`
		DAG             json.RawMessage `json:"dag"`
		HyperParameters json.RawMessage `json:"hyper_parameters"`
	}
	cfg.ID = dbRow.ID
	
	if len(dbRow.DagConfig) > 0 {
		cfg.DAG = dbRow.DagConfig
	} else {
		cfg.DAG = []byte(`{"nodes": {}, "entry_node": ""}`)
	}
	
	if len(dbRow.HyperParameters) > 0 {
		cfg.HyperParameters = dbRow.HyperParameters
	} else {
		cfg.HyperParameters = []byte(`{}`)
	}
	
	if len(dbRow.Prompts) > 0 {
		cfg.Prompts = dbRow.Prompts
	} else {
		cfg.Prompts = []byte(`{}`)
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(cfg)
}

func (h *Handler) HandleUpsertASEConfig(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID          string          `json:"user_id"`
		TenantID        string          `json:"tenant_id,omitempty"`
		Name            string          `json:"name"`
		DagConfig       json.RawMessage `json:"dag"`
		HyperParameters json.RawMessage `json:"hyper_parameters"`
		Prompts         json.RawMessage `json:"prompts"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(rw, "name is required", http.StatusBadRequest)
		return
	}

	uidStr := req.UserID
	if uidStr == "" {
		uidStr = req.TenantID
	}

	var uid pgtype.UUID
	if uidStr != "" {
		parsed, err := uuid.Parse(uidStr)
		if err == nil {
			uid = pgtype.UUID{Bytes: parsed, Valid: true}
		}
	}

	cfg, err := h.DB.UpsertASEConfig(r.Context(), database.UpsertASEConfigParams{
		UserID:          uid,
		Name:            req.Name,
		DagConfig:       req.DagConfig,
		HyperParameters: req.HyperParameters,
		Prompts:         req.Prompts,
	})

	if err != nil {
		h.Logger.Error("failed to upsert config", "error", err)
		http.Error(rw, "failed to upsert config", http.StatusInternalServerError)
		return
	}

	ase.InvalidateConfigCache(uidStr, req.Name)
	ase.GetConfig(uidStr, req.Name)

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(cfg)
}

func (h *Handler) HandleASEDebugUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(debug.IndexHTML)
}

type aseDagVersionJSON struct {
	ID            pgtype.UUID        `json:"id"`
	DagID         pgtype.UUID        `json:"dag_id"`
	VersionNumber int32              `json:"version_number"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
}

func (h *Handler) HandleListASEDagVersions(rw http.ResponseWriter, r *http.Request) {
	dagIDStr := r.URL.Query().Get("dag_id")
	if dagIDStr == "" {
		http.Error(rw, "missing dag_id parameter", http.StatusBadRequest)
		return
	}

	uid, err := uuid.Parse(dagIDStr)
	if err != nil {
		http.Error(rw, "invalid dag_id", http.StatusBadRequest)
		return
	}

	versions, err := h.DB.ListASEDagVersions(r.Context(), pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		h.Logger.Error("failed to list DAG versions", "error", err)
		http.Error(rw, "failed to list DAG versions", http.StatusInternalServerError)
		return
	}

	resp := make([]aseDagVersionJSON, len(versions))
	for i, v := range versions {
		resp[i] = aseDagVersionJSON{
			ID:            v.ID,
			DagID:         v.DagID,
			VersionNumber: v.VersionNumber,
			CreatedAt:     v.CreatedAt,
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(resp)
}

type getASEDagVersionResponse struct {
	ID              pgtype.UUID        `json:"id"`
	DagID           pgtype.UUID        `json:"dag_id"`
	VersionNumber   int32              `json:"version_number"`
	DagConfig       json.RawMessage    `json:"dag"`
	HyperParameters json.RawMessage    `json:"hyper_parameters"`
	Prompts         json.RawMessage    `json:"prompts"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
}

func (h *Handler) HandleGetASEDagVersion(rw http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(rw, "missing id parameter", http.StatusBadRequest)
		return
	}

	uid, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(rw, "invalid id", http.StatusBadRequest)
		return
	}

	version, err := h.DB.GetASEDagVersion(r.Context(), pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		http.Error(rw, "version not found", http.StatusNotFound)
		return
	}

	var resp getASEDagVersionResponse
	resp.ID = version.ID
	resp.DagID = version.DagID
	resp.VersionNumber = version.VersionNumber
	resp.CreatedAt = version.CreatedAt

	if len(version.DagConfig) > 0 {
		resp.DagConfig = version.DagConfig
	} else {
		resp.DagConfig = []byte(`{"nodes": {}, "entry_node": ""}`)
	}

	if len(version.HyperParameters) > 0 {
		resp.HyperParameters = version.HyperParameters
	} else {
		resp.HyperParameters = []byte(`{}`)
	}

	if len(version.Prompts) > 0 {
		resp.Prompts = version.Prompts
	} else {
		resp.Prompts = []byte(`{}`)
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(resp)
}

func (h *Handler) HandleRestoreASEDagVersion(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(rw, "missing id in request body", http.StatusBadRequest)
		return
	}

	uid, err := uuid.Parse(req.ID)
	if err != nil {
		http.Error(rw, "invalid id", http.StatusBadRequest)
		return
	}

	version, err := h.DB.GetASEDagVersion(r.Context(), pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		http.Error(rw, "version not found", http.StatusNotFound)
		return
	}

	// Update active config table
	updated, err := h.DB.UpdateASEConfigByID(r.Context(), database.UpdateASEConfigByIDParams{
		ID:              version.DagID,
		DagConfig:       version.DagConfig,
		HyperParameters: version.HyperParameters,
		Prompts:         version.Prompts,
	})
	if err != nil {
		h.Logger.Error("failed to restore version", "error", err)
		http.Error(rw, "failed to restore version", http.StatusInternalServerError)
		return
	}

	// Invalidate cache
	userIDStr := ""
	if updated.UserID.Valid {
		userIDStr = uuid.UUID(updated.UserID.Bytes).String()
	}
	ase.InvalidateConfigCache(userIDStr, updated.Name)

	// Fetch current configuration to force reload & return
	ase.GetConfig(userIDStr, updated.Name)

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(aseConfigJSON{
		ID:              updated.ID,
		UserID:          updated.UserID,
		Name:            updated.Name,
		DagConfig:       updated.DagConfig,
		HyperParameters: updated.HyperParameters,
		Prompts:         updated.Prompts,
		CreatedAt:       updated.CreatedAt,
		UpdatedAt:       updated.UpdatedAt,
	})
}

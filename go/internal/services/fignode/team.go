package fignode

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
)

func (h *Handler) HandleTeamInvite(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		writeError(w, 401, "UNAUTHORIZED", "Missing user context")
		return
	}

	var req TeamInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "INVALID_BODY", "Invalid request body")
		return
	}

	if req.Email == "" || req.TenantID == "" || req.FirmName == "" {
		writeError(w, 400, "MISSING_FIELDS", "email, tenantId, and firmName are required")
		return
	}

	// Generate 8-character token
	tokenBytes := make([]byte, 4)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(w, 500, "INTERNAL", "Failed to generate token")
		return
	}
	token := strings.ToUpper(hex.EncodeToString(tokenBytes))

	// Cleanup old invites asynchronously to save response time
	go func() {
		err := h.db.DeleteExpiredTeamInvites(r.Context())
		if err != nil {
			h.logger.Error("failed to cleanup expired team invites", "error", err)
		}
	}()

	_, err := h.db.CreateTeamInvite(r.Context(), database.CreateTeamInviteParams{
		Token:    token,
		Email:    req.Email,
		TenantID: req.TenantID,
		FirmName: req.FirmName,
	})
	if err != nil {
		h.logger.Error("failed to create team invite", "error", err)
		writeError(w, 500, "INTERNAL", "Failed to create team invite")
		return
	}

	if h.emailSender != nil {
		go func() {
			err := h.emailSender.SendTeamInvite(req.Email, token, req.TenantID, req.FirmName)
			if err != nil {
				h.logger.Error("failed to send team invite email", "error", err)
			}
		}()
	} else {
		h.logger.Warn("emailSender is nil, skipping sending invite email")
	}

	writeJSON(w, 200, TeamInviteResponse{
		Success: true,
		Token:   token,
	})
}

func (h *Handler) HandleTeamInviteValidate(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		writeError(w, 401, "UNAUTHORIZED", "Missing user context")
		return
	}

	var req TeamInviteValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "INVALID_BODY", "Invalid request body")
		return
	}

	if req.Token == "" {
		writeError(w, 400, "MISSING_FIELDS", "token is required")
		return
	}

	invite, err := h.db.DeleteAndReturnTeamInvite(r.Context(), req.Token)
	if err != nil {
		// Differentiate between generic not found vs db error
		if err.Error() == "no rows in result set" {
			writeError(w, 400, "INVALID_TOKEN", "Invite token is invalid or already used")
			return
		}
		h.logger.Error("failed to validate team invite", "error", err)
		writeError(w, 500, "INTERNAL", "Failed to validate team invite")
		return
	}

	writeJSON(w, 200, TeamInviteValidateResponse{
		Valid:    true,
		Email:    invite.Email,
		TenantID: invite.TenantID,
		FirmName: invite.FirmName,
	})
}

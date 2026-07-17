package api

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
)

type ConversionPayload struct {
	CustomerEmail   string `json:"customer_email"`
	ConversionValue int    `json:"conversion_value"`
	Source          string `json:"source"`
	TenantID        string `json:"tenant_id"` // Simplified for the exercise
}

// HandleConversionWebhook processes downstream conversion events for Revenue Attribution.
// Endpoint: POST /api/webhooks/conversion
func (h *Handler) HandleConversionWebhook(w http.ResponseWriter, r *http.Request) {
	var payload ConversionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if payload.CustomerEmail == "" || payload.ConversionValue <= 0 || payload.TenantID == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	tenantUUID := parseUUIDString(payload.TenantID)
	if !tenantUUID.Valid {
		http.Error(w, "invalid tenant_id format", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Determine Window Days
	windowDays := int32(5)
	settings, err := h.DB.GetTenantSettings(ctx, tenantUUID)
	if err == nil {
		windowDays = settings.AttributionWindowDays
	}

	// 2. Lookup Prospect by Email (simplified approach)
	// Assuming there's a prospect with this email for the tenant
	// If the system supports looking up prospect by email, we'd do it here.
	// For this exercise, let's assume we can query FindLastTouch directly if we have prospect_id.
	// Wait, FindLastTouch takes prospect_id. I need to get prospect_id by email first.
	// We'll add a helper query or do it directly if we have a GetProspectByEmail query.
	// If no prospect is found, we can still record an Unattributed conversion.
	prospectID, err := h.DB.GetProspectIDByEmail(ctx, database.GetProspectIDByEmailParams{
		Email:    payload.CustomerEmail,
		TenantID: tenantUUID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			// No prospect found, just log unattributed conversion and return OK
			_, err = h.DB.CreateConversion(ctx, database.CreateConversionParams{
				TenantID:        tenantUUID,
				CustomerEmail:   payload.CustomerEmail,
				ConversionValue: int32(payload.ConversionValue),
				Source:          pgtype.Text{String: payload.Source, Valid: payload.Source != ""},
				ProspectID:      pgtype.UUID{Valid: false},
			})
			if err != nil {
				h.Logger.Error("failed to save unattributed conversion", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	// 3. Find Last Touch (Matchmaker)
	touch, err := h.DB.FindLastTouch(ctx, database.FindLastTouchParams{
		ProspectID: prospectID,
		WindowDays: windowDays,
	})

	// 4. Save Conversion
	conv, err := h.DB.CreateConversion(ctx, database.CreateConversionParams{
		TenantID:        tenantUUID,
		CustomerEmail:   payload.CustomerEmail,
		ConversionValue: int32(payload.ConversionValue),
		Source:          pgtype.Text{String: payload.Source, Valid: payload.Source != ""},
		ProspectID:      prospectID,
	})
	if err != nil {
		h.Logger.Error("failed to create conversion", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 5. Attribute if a match was found
	if err == nil && touch.ID.Valid {
		_, err = h.DB.CreateAttribution(ctx, database.CreateAttributionParams{
			ConversionID:    conv.ID,
			EmailLogID:      touch.ID,
			CampaignID:      touch.CampaignID,
			ListID:          touch.ListID,
			AttributedValue: int32(payload.ConversionValue),
		})
		if err != nil {
			h.Logger.Error("failed to create attribution", "error", err)
			// don't fail the webhook, conversion is saved
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

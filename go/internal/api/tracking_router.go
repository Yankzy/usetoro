package api

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
)

// isHuman evaluates the User-Agent string to filter out known bots and Apple Mail Privacy Protection.
func isHuman(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	if ua == "" {
		return false
	}
	
	// Apple Mail Privacy Protection (MPP) proxy
	if strings.Contains(ua, "mozilla/5.0") && !strings.Contains(ua, "windows") && !strings.Contains(ua, "macintosh") && !strings.Contains(ua, "linux") && !strings.Contains(ua, "android") && !strings.Contains(ua, "iphone") && !strings.Contains(ua, "ipad") {
		// Extremely barebones Mozilla/5.0 is the typical Apple Proxy signature
		return false
	}
	
	// Common security scanners and bots
	botSignatures := []string{
		"bot", "spider", "crawler", "googleimageproxy", "barracuda", "proofpoint", "mimecast", "yahoomailproxy", "headless",
	}
	for _, sig := range botSignatures {
		if strings.Contains(ua, sig) {
			return false
		}
	}
	
	return true
}

func parseUUIDString(id string) pgtype.UUID {
	var uuid pgtype.UUID
	err := uuid.Scan(id)
	if err != nil {
		uuid.Valid = false
	} else {
		uuid.Valid = true
	}
	return uuid
}

// HandleTrackOpen handles tracking pixels for revenue attribution and generic open events.
// Endpoint: GET /api/track/open?p={prospect_id}&c={campaign_id}&l={list_id}
func (h *Handler) HandleTrackOpen(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	c := r.URL.Query().Get("c")
	l := r.URL.Query().Get("l")

	if p == "" {
		serveTransparentGIF(w)
		return
	}

	userAgent := r.UserAgent()
	ipAddress := r.RemoteAddr
	human := isHuman(userAgent)

	_ = h.DB.LogEmailEvent(r.Context(), database.LogEmailEventParams{
		ProspectID: parseUUIDString(p),
		CampaignID: parseUUIDString(c),
		ListID:     parseUUIDString(l),
		NatsMsgID:  pgtype.Text{String: "pixel", Valid: true},
		EventType:  "opened",
		Metadata:   []byte(`{}`),
		UserAgent:  pgtype.Text{String: userAgent, Valid: userAgent != ""},
		IpAddress:  pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		IsHuman:    pgtype.Bool{Bool: human, Valid: true},
	})

	serveTransparentGIF(w)
}

// HandleTrackClick handles click redirects for revenue attribution.
// Endpoint: GET /api/track/click?p={prospect_id}&c={campaign_id}&l={list_id}&url={base64_encoded_url}
func (h *Handler) HandleTrackClick(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	c := r.URL.Query().Get("c")
	l := r.URL.Query().Get("l")
	b64URL := r.URL.Query().Get("url")

	if p == "" || b64URL == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	decodedURLBytes, err := base64.URLEncoding.DecodeString(b64URL)
	if err != nil {
		// fallback
		decodedURLBytes, err = base64.StdEncoding.DecodeString(b64URL)
		if err != nil {
			http.Error(w, "invalid url encoding", http.StatusBadRequest)
			return
		}
	}
	targetURL := string(decodedURLBytes)

	userAgent := r.UserAgent()
	ipAddress := r.RemoteAddr
	human := isHuman(userAgent)

	_ = h.DB.LogEmailEvent(r.Context(), database.LogEmailEventParams{
		ProspectID: parseUUIDString(p),
		CampaignID: parseUUIDString(c),
		ListID:     parseUUIDString(l),
		NatsMsgID:  pgtype.Text{String: "click", Valid: true},
		EventType:  "clicked",
		Metadata:   []byte(`{"url":"` + targetURL + `"}`),
		UserAgent:  pgtype.Text{String: userAgent, Valid: userAgent != ""},
		IpAddress:  pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		IsHuman:    pgtype.Bool{Bool: human, Valid: true},
	})

	http.Redirect(w, r, targetURL, http.StatusFound)
}

func serveTransparentGIF(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)

	transparentGIF := []byte{
		0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0xff, 0xff, 0xff,
		0x00, 0x00, 0x00, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
	}
	w.Write(transparentGIF)
}

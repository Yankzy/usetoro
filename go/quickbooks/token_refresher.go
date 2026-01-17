package quickbooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// TokenRefresherService handles access token rotation for QuickBooks connections
type TokenRefresherService struct {
	DB *sql.DB
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func NewTokenRefresher(db *sql.DB) *TokenRefresherService {
	return &TokenRefresherService{DB: db}
}

func (s *TokenRefresherService) Start(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanAndRefresh()
		}
	}
}

func (s *TokenRefresherService) scanAndRefresh() {
	// Query for tokens that are expiring in < 10 minutes (assuming standard 60m expiry)
	// For simplicity, we'll check if last_refreshed is older than 50 minutes.
	// Note: in production, you should store 'expires_at' or calculate based on exact logic.
	rows, err := s.DB.Query(`
		SELECT id, realm_id, refresh_token 
		FROM quickbooks_quickbooksconnection 
		WHERE status = 'active' 
		AND last_refreshed < NOW() - INTERVAL '50 minutes'
	`)
	if err != nil {
		log.Printf("Error scanning for expiring tokens: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var realmID string
		var encryptedRefreshToken string // In real app, decrypt this first!

		if err := rows.Scan(&id, &realmID, &encryptedRefreshToken); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		// TODO: Decrypt refreshToken here
		refreshToken := encryptedRefreshToken 

		newTokens, err := s.refreshAccessToken(refreshToken)
		if err != nil {
			log.Printf("Failed to refresh token for realm %s: %v", realmID, err)
			// Handle error, maybe mark as disconnected if it's a 400 invalid grant
			continue
		}

		// TODO: Encrypt new tokens
		
		_, err = s.DB.Exec(`
			UPDATE quickbooks_quickbooksconnection 
			SET access_token = $1, refresh_token = $2, last_refreshed = NOW() 
			WHERE id = $3
		`, newTokens.AccessToken, newTokens.RefreshToken, id)
		
		if err != nil {
			log.Printf("Error updating tokens for realm %s: %v", realmID, err)
		} else {
			log.Printf("Successfully refreshed token for realm %s", realmID)
		}
	}
}

func (s *TokenRefresherService) refreshAccessToken(refreshToken string) (*TokenResponse, error) {
	// Intuit OAuth2 Endpoint
	tokenEndpoint := "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
	
	// You need ClientID and ClientSecret here. 
	// In a real app, inject these via config.
	clientID := "YOUR_CLIENT_ID"
	clientSecret := "YOUR_CLIENT_SECRET"

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequest("POST", tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

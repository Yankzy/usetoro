package quickbooks

import (
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type mockTokenSource struct {
	token *oauth2.Token
	err   error
}

func (m *mockTokenSource) Token() (*oauth2.Token, error) {
	return m.token, m.err
}

func TestNotifyTokenSource_MergeRefreshToken(t *testing.T) {
	initialToken := &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "persistent-refresh",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	mock := &mockTokenSource{
		token: &oauth2.Token{
			AccessToken:  "new-access",
			RefreshToken: "", // QBO often omits this on refresh
			Expiry:       time.Now().Add(2 * time.Hour),
		},
	}

	var updatedToken *BearerToken
	onUpdate := func(token *BearerToken) error {
		updatedToken = token
		return nil
	}

	nts := &notifyTokenSource{
		src:            mock,
		lastKnownToken: initialToken,
		onTokenUpdated: onUpdate,
	}

	gotToken, err := nts.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	// 1. Check if the returned token has the merged refresh token
	if gotToken.RefreshToken != "persistent-refresh" {
		t.Errorf("Returned token RefreshToken = %q, want %q", gotToken.RefreshToken, "persistent-refresh")
	}

	// 2. Check if the callback received the merged refresh token
	if updatedToken == nil {
		t.Fatal("onTokenUpdated callback was not called")
	}
	if updatedToken.RefreshToken != "persistent-refresh" {
		t.Errorf("Callback RefreshToken = %q, want %q", updatedToken.RefreshToken, "persistent-refresh")
	}

	// 3. Check if AccessToken was updated
	if updatedToken.AccessToken != "new-access" {
		t.Errorf("Callback AccessToken = %q, want %q", updatedToken.AccessToken, "new-access")
	}
}

func TestNotifyTokenSource_RotatedRefreshToken(t *testing.T) {
	initialToken := &oauth2.Token{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	mock := &mockTokenSource{
		token: &oauth2.Token{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh", // QBO rotated the token
			Expiry:       time.Now().Add(2 * time.Hour),
		},
	}

	var updatedToken *BearerToken
	onUpdate := func(token *BearerToken) error {
		updatedToken = token
		return nil
	}

	nts := &notifyTokenSource{
		src:            mock,
		lastKnownToken: initialToken,
		onTokenUpdated: onUpdate,
	}

	gotToken, err := nts.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	// 1. Check if the returned token has the NEW rotated refresh token
	if gotToken.RefreshToken != "new-refresh" {
		t.Errorf("Returned token RefreshToken = %q, want %q", gotToken.RefreshToken, "new-refresh")
	}

	// 2. Check if the callback received the NEW rotated refresh token
	if updatedToken == nil {
		t.Fatal("onTokenUpdated callback was not called")
	}
	if updatedToken.RefreshToken != "new-refresh" {
		t.Errorf("Callback RefreshToken = %q, want %q", updatedToken.RefreshToken, "new-refresh")
	}
}

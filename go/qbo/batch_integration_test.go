//go:build integration
// +build integration

package quickbooks

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchIntegration tests the batch API against QBO sandbox
// Run with: go test -tags=integration -v ./qbo -run TestBatchIntegration
//
// Required environment variables:
// - QBO_CLIENT_ID: Your app's client ID
// - QBO_CLIENT_SECRET: Your app's client secret
// - QBO_REALM_ID: The company/realm ID to test against
// - QBO_ACCESS_TOKEN: A valid access token
// - QBO_REFRESH_TOKEN: A valid refresh token
func TestBatchIntegration(t *testing.T) {
	// Skip if not running integration tests
	if os.Getenv("QBO_CLIENT_ID") == "" {
		t.Skip("Skipping integration test: QBO_CLIENT_ID not set")
	}

	clientID := os.Getenv("QBO_CLIENT_ID")
	clientSecret := os.Getenv("QBO_CLIENT_SECRET")
	realmID := os.Getenv("QBO_REALM_ID")
	accessToken := os.Getenv("QBO_ACCESS_TOKEN")
	refreshToken := os.Getenv("QBO_REFRESH_TOKEN")

	require.NotEmpty(t, clientID, "QBO_CLIENT_ID must be set")
	require.NotEmpty(t, clientSecret, "QBO_CLIENT_SECRET must be set")
	require.NotEmpty(t, realmID, "QBO_REALM_ID must be set")
	require.NotEmpty(t, accessToken, "QBO_ACCESS_TOKEN must be set")
	require.NotEmpty(t, refreshToken, "QBO_REFRESH_TOKEN must be set")

	// Create bearer token
	token := &BearerToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	// Initialize client (using sandbox)
	client, err := NewClient(clientID, clientSecret, realmID, false, "65", token, nil)
	require.NoError(t, err, "Failed to create QBO client")

	t.Run("BatchCreateVendors", func(t *testing.T) {
		testBatchCreateVendors(t, client)
	})

	t.Run("BatchPartialFailure", func(t *testing.T) {
		testBatchPartialFailure(t, client)
	})

	t.Run("BatchSizeLimit", func(t *testing.T) {
		testBatchSizeLimit(t, client)
	})
}

func testBatchCreateVendors(t *testing.T, client *Client) {
	// Create unique vendor names using timestamp
	timestamp := time.Now().Unix()

	builder := NewBatchBuilder()
	builder.AddCreate("Vendor", Vendor{
		DisplayName: String(concat("Test Vendor 1 - ", timestamp)),
	})
	builder.AddCreate("Vendor", Vendor{
		DisplayName: String(concat("Test Vendor 2 - ", timestamp)),
	})
	builder.AddCreate("Vendor", Vendor{
		DisplayName: String(concat("Test Vendor 3 - ", timestamp)),
	})

	// Execute batch
	response, err := client.Batch(builder.Build())
	require.NoError(t, err, "Batch request failed")
	require.NotNil(t, response, "Response should not be nil")

	// Verify all 3 operations succeeded
	assert.Len(t, response.BatchItemResponse, 3, "Should have 3 responses")

	createdVendorIDs := make([]string, 0, 3)
	for i, item := range response.BatchItemResponse {
		assert.Equal(t, concat("bid-", i+1), item.BId, "bId should match")
		assert.False(t, item.HasError(), "Operation should succeed: %s", item.GetError())
		assert.NotNil(t, item.Vendor, "Vendor should be created")
		if item.Vendor != nil {
			assert.NotEmpty(t, item.Vendor.Id, "Vendor ID should be set")
			createdVendorIDs = append(createdVendorIDs, item.Vendor.Id)
		}
	}

	t.Logf("Successfully created %d vendors: %v", len(createdVendorIDs), createdVendorIDs)

	// Cleanup: delete created vendors
	if len(createdVendorIDs) > 0 {
		t.Cleanup(func() {
			deleteBuilder := NewBatchBuilder()
			for _, id := range createdVendorIDs {
				deleteBuilder.AddDelete("Vendor", Vendor{Id: id})
			}
			_, _ = client.Batch(deleteBuilder.Build())
		})
	}
}

func testBatchPartialFailure(t *testing.T, client *Client) {
	timestamp := time.Now().Unix()

	builder := NewBatchBuilder()
	// Valid vendor
	builder.AddCreate("Vendor", Vendor{
		DisplayName: String(concat("Valid Vendor - ", timestamp)),
	})
	// Invalid vendor (missing required DisplayName)
	builder.AddCreate("Vendor", Vendor{
		// DisplayName is intentionally missing
	})

	response, err := client.Batch(builder.Build())
	require.NoError(t, err, "Batch request should not fail at HTTP level")
	require.NotNil(t, response)

	assert.Len(t, response.BatchItemResponse, 2, "Should have 2 responses")

	// First should succeed
	assert.False(t, response.BatchItemResponse[0].HasError(), "First vendor should succeed")
	assert.NotNil(t, response.BatchItemResponse[0].Vendor)

	// Second should fail
	assert.True(t, response.BatchItemResponse[1].HasError(), "Second vendor should fail")
	assert.NotEmpty(t, response.BatchItemResponse[1].GetError(), "Should have error message")
	t.Logf("Expected error for invalid vendor: %s", response.BatchItemResponse[1].GetError())

	// Cleanup successful vendor
	if response.BatchItemResponse[0].Vendor != nil {
		t.Cleanup(func() {
			deleteBuilder := NewBatchBuilder()
			deleteBuilder.AddDelete("Vendor", Vendor{
				Id: response.BatchItemResponse[0].Vendor.Id,
			})
			_, _ = client.Batch(deleteBuilder.Build())
		})
	}
}

func testBatchSizeLimit(t *testing.T, client *Client) {
	// Test that we can't exceed 30 operations
	builder := NewBatchBuilder()
	for i := 0; i < 31; i++ {
		builder.AddCreate("Vendor", Vendor{
			DisplayName: String(concat("Vendor ", i)),
		})
	}

	_, err := client.Batch(builder.Build())
	require.Error(t, err, "Should reject batch with >30 operations")
	assert.Contains(t, err.Error(), "exceeds maximum", "Error should mention limit")
}

// Helper functions
func concat(parts ...interface{}) string {
	result := ""
	for _, p := range parts {
		result += String(p)
	}
	return result
}

func String(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return string(rune(val + '0'))
	case int64:
		return string(rune(val + '0'))
	default:
		return ""
	}
}

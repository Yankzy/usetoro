package workers

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveSender_RealUser verifies ResolveSender resolves a known user
// against the live database. Run inside the protocol container:
//
//	docker compose exec protocol go test ./go/internal/workers/... -run TestResolveSender_RealUser -v
func TestResolveSender_RealUser(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping integration test (run inside protocol container)")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err, "failed to connect to database")
	defer pool.Close()

	db := database.New(pool)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sender, err := ResolveSender(ctx, logger, db, pool, "yankz@fignode.com", "accounting@toro-synthetic-bookkeeping.inbound.usetoro.io", "")

	require.NoError(t, err, "ResolveSender should succeed for a known user")
	assert.True(t, sender.EntityID.Valid, "EntityID should be valid")
	assert.NotEmpty(t, sender.EntityIDStr, "EntityIDStr should not be empty")
	assert.Equal(t, "yankz@fignode.com", sender.FromHandle)
	assert.Equal(t, "accounting@toro-synthetic-bookkeeping.inbound.usetoro.io", sender.ToHandle)
	assert.Equal(t, "accounting", sender.AgentAlias)
	assert.Equal(t, "toro-synthetic-bookkeeping", sender.Subdomain)

	t.Logf("✅ ResolveSender succeeded:")
	t.Logf("   EntityID:    %s", sender.EntityIDStr)
	t.Logf("   FromHandle:  %s", sender.FromHandle)
	t.Logf("   ToHandle:    %s", sender.ToHandle)
	t.Logf("   AgentAlias:  %s", sender.AgentAlias)
	t.Logf("   Subdomain:   %s", sender.Subdomain)

	// Also verify that an unknown user correctly returns an error
	unknownSender, unknownErr := ResolveSender(ctx, logger, db, pool, "nobody@doesnotexist.com", "rap_morocco@a.usetoro.io", "")
	assert.Error(t, unknownErr, "ResolveSender should fail for unknown user")
	assert.False(t, unknownSender.EntityID.Valid, "EntityID should not be valid for unknown user")
	assert.Contains(t, unknownErr.Error(), "unauthorized sender")
	_ = io.Discard // silence unused import if needed

	t.Logf("✅ Unknown user correctly rejected: %s", unknownErr.Error())
}

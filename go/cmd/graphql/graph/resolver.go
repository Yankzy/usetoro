package graph

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	"crypto/ed25519"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Resolver struct {
	DB              *pgxpool.Pool
	Redis           *redis.Client
	PrivateKey      ed25519.PrivateKey
	Logger          *slog.Logger
	Store           *store.Store
	EmailSender     auth.EmailSender
	CoAMapper       *ai.CoAMapper
	EntityResolver  *ai.EntityResolver
	CleanupEnricher *cleanup.CleanupEnricher
}

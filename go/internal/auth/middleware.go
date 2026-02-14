package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

// Context keys to avoid string allocation / collision
type contextKey string

const (
	UserIDKey   contextKey = "user_id"
	TenantIDKey contextKey = "tenant_id"
	RoleKey     contextKey = "role"
	// JTIKey is the JWT ID, used for revocation
	JTIKey contextKey = "jti"
)

type Authenticator struct {
	PublicKey ed25519.PublicKey
	Redis     *redis.Client
	DB        *database.Queries
}

// ---------------------------------------------------------------------
// Core Logic: VerifyToken
// This is the shared brain. It checks signature + redis revocation.
// ---------------------------------------------------------------------
func (a *Authenticator) VerifyToken(ctx context.Context, tokenString string) (*UserClaims, error) {
	// 1. Parse & Verify Signature (CPU Only)
	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(t *jwt.Token) (interface{}, error) {
		return a.PublicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("token is invalid")
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}

	// 2. Check Revocation (Redis)
	// We check if the unique JTI (JWT ID) is in the blocklist
	if a.Redis != nil {
		isRevoked, _ := a.Redis.Exists(ctx, "blacklist:"+claims.ID).Result()
		if isRevoked > 0 {
			return nil, errors.New("token has been revoked")
		}
	}

	// 3. Check User Status (DB)
	// Ensure user exists and is active
	if a.DB != nil {
		// We use a separate context or the request context.
		// Since we are in the middleware/verification flow, request context is fine.
		user, err := a.DB.GetUserByID(ctx, pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true})
		if err != nil {
			// If user not found or DB error, fail validation
			return nil, fmt.Errorf("user validation failed: %w", err)
		}
		if !user.IsActive.Bool {
			return nil, errors.New("user is inactive")
		}
	}

	return claims, nil
}

// RevokeToken adds the token's JTI to the blacklist
func (a *Authenticator) RevokeToken(ctx context.Context, jti string, expiration time.Duration) error {
	if a.Redis == nil {
		return nil
	}
	return a.Redis.Set(ctx, "blacklist:"+jti, "revoked", expiration).Err()
}

// injectContext puts the claims into the request context
func injectContext(r *http.Request, claims *UserClaims) *http.Request {
	ctx := context.WithValue(r.Context(), UserIDKey, claims.UserID)
	ctx = context.WithValue(ctx, TenantIDKey, claims.TenantID)
	ctx = context.WithValue(ctx, RoleKey, claims.Role)
	ctx = context.WithValue(ctx, JTIKey, claims.ID)
	return r.WithContext(ctx)
}

// Middleware 1: StandardHeader (For REST APIs like cmd/gate, cmd/auth)
// Expects: "Authorization: Bearer <token>"
// Modified for GraphQL: Does NOT block. Returns context without claims if invalid/missing.
// Resolvers must check for claims.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// No auth? Just proceed. Context will lack UserIDKey.
			// Resolvers needing auth will check and fail.
			// Public resolvers (Login, Signup, Introspection) will work.
			next.ServeHTTP(w, r)
			return
		}

		// Remove "Bearer " prefix if present
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := a.VerifyToken(r.Context(), tokenString)
		if err != nil {
			// If header is present but invalid, we technically SHOULD fail (401),
			// BUT for GraphQL usually it's better to return null/error in the field.
			// However, if the token is explicitly bad (expired/fake), returning 401 is arguably correct and safer.
			// Let's keep 401 for *invalid* tokens to prevent confusion.
			// But careful: Introspection might send garbage sometimes? Unlikely.
			// 401 Soft Fail for GraphQL:
			// If the token is invalid (expired, bad signature, etc.), we intentionally DO NOT return 401.
			// Why? Because browsers/Playground often send old/bad tokens. A 401 text response breaks the JSON parser
			// in Playground/GraphiQL ("Unexpected token U").
			//
			// Instead, we simply proceed without injecting claims (treating as anonymous).
			// Protected resolvers will check for claims and return a proper GraphQL error (JSON).
			// Public resolvers (Login, Introspection) will work fine.

			// Optional: valid place to log "Invalid token: %v"
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, injectContext(r, claims))
	})
}

// ---------------------------------------------------------------------
// Middleware 2: QueryParam (For WebSockets like cmd/ws)
// Expects URL: "wss://api.toro.io/ws?token=<token>"
// OR Header: "Authorization: Bearer <token>"
// ---------------------------------------------------------------------
func (a *Authenticator) QueryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Check Authorization Header first (More secure, avoids logging)
		tokenString := ""
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// 2. Fallback to Query Parameter (Backward compatibility)
		if tokenString == "" {
			tokenString = r.URL.Query().Get("token")
		}
		if tokenString == "" {
			// Fallback: Check 'access_token' (common convention)
			tokenString = r.URL.Query().Get("access_token")
		}

		if tokenString == "" {
			http.Error(w, "Missing 'token' query parameter or Authorization header", http.StatusUnauthorized)
			return
		}

		claims, err := a.VerifyToken(r.Context(), tokenString)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, injectContext(r, claims))
	})
}

// ---------------------------------------------------------------------
// Middleware 3: AdminOnly (Role Based Access Control)
// Use this AFTER one of the middlewares above to enforce admin rights
// ---------------------------------------------------------------------
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := r.Context().Value(RoleKey).(string)
		if !ok || role != "admin" {
			http.Error(w, "Forbidden: Admins only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

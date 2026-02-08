package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

	if err != nil || !token.Valid {
		return nil, errors.New("invalid token signature")
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

// ---------------------------------------------------------------------
// Middleware 1: StandardHeader (For REST APIs like cmd/gate, cmd/auth)
// Expects: "Authorization: Bearer <token>"
// ---------------------------------------------------------------------
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Missing Authorization Header", http.StatusUnauthorized)
			return
		}

		// Remove "Bearer " prefix if present
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := a.VerifyToken(r.Context(), tokenString)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, injectContext(r, claims))
	})
}

// ---------------------------------------------------------------------
// Middleware 2: QueryParam (For WebSockets like cmd/ws)
// Expects URL: "wss://api.toro.io/ws?token=<token>"
// ---------------------------------------------------------------------
func (a *Authenticator) QueryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenString := r.URL.Query().Get("token")
		if tokenString == "" {
			// Fallback: Check 'access_token' (common convention)
			tokenString = r.URL.Query().Get("access_token")
		}

		if tokenString == "" {
			http.Error(w, "Missing 'token' query parameter", http.StatusUnauthorized)
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

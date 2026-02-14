package dataloader

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Middleware injects data loaders into the request context
func Middleware(db *pgxpool.Pool) func(http.Handler) http.Handler {
	loaders := NewLoaders(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), loadersKey, loaders)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

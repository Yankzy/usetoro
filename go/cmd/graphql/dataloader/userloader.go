package dataloader

import (
	"context"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/graph-gophers/dataloader"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Expose StringKey for convenience
type StringKey = dataloader.StringKey

type UserLoader struct {
	*dataloader.Loader
}

func NewUserLoader(db *pgxpool.Pool) *UserLoader {
	return &UserLoader{
		Loader: dataloader.NewBatchedLoader(func(ctx context.Context, keys dataloader.Keys) []*dataloader.Result {
			// Convert keys to UUIDs
			ids := make([]pgtype.UUID, len(keys))
			for i, key := range keys {
				var uuid pgtype.UUID
				if err := uuid.Scan(key.String()); err != nil {
					// Return error for this specific key
					results := make([]*dataloader.Result, len(keys))
					results[i] = &dataloader.Result{Error: err}
					return results
				}
				ids[i] = uuid
			}

			// Query DB
			q := database.New(db)
			users, err := q.GetUsersByIDs(ctx, ids)
			if err != nil {
				// If DB fetch fails, all keys fail
				results := make([]*dataloader.Result, len(keys))
				for i := range results {
					results[i] = &dataloader.Result{Error: err}
				}
				return results
			}

			// Map users to results in standard O(n) way
			userMap := make(map[string]database.ToroCoreUser)
			for _, u := range users {
				userMap[fmt.Sprintf("%x", u.ID.Bytes)] = u // using hex string of bytes as key match
			}

			results := make([]*dataloader.Result, len(keys))
			for i, key := range keys {
				// We need to match the key format.
				// The input key string might be standard UUID string.
				// The pgtype.UUID Bytes are 16 bytes.

				// Let's assume input keys are standard UUID strings.
				// We need a robust way to map back.
				// Let's rely on the order-preserving property if possible?
				// No, sql doesn't guarantee order with IN/ANY unless specified.

				// Re-parsing key to UUID bytes to match map key
				var uuid pgtype.UUID
				_ = uuid.Scan(key.String()) // verified above

				u, ok := userMap[fmt.Sprintf("%x", uuid.Bytes)]

				if ok {
					results[i] = &dataloader.Result{Data: u}
				} else {
					// Not found is not necessarily an error, but for loader it means nil data
					results[i] = &dataloader.Result{Data: nil, Error: nil}
				}
			}
			return results
		}, dataloader.WithWait(2*time.Millisecond)),
	}
}

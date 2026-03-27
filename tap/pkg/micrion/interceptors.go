package micrion

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go"
)

// InfraTollCost defines the standard 1,616 Micrion toll for fundamental operations.
const InfraTollCost = 1616

// ============================================================================
// EVENT BUS INTERCEPTOR
// ============================================================================

// TolledEventBus wraps an EventBus to enforce a micrion toll on all outbound messages.
type TolledEventBus struct {
	underlying agent.EventBus
	wm         *WalletManager
	did        string
}

// NewTolledEventBus creates a new EventBus interceptor.
func NewTolledEventBus(underlying agent.EventBus, wm *WalletManager, did string) *TolledEventBus {
	return &TolledEventBus{
		underlying: underlying,
		wm:         wm,
		did:        did,
	}
}

func (t *TolledEventBus) Publish(subject string, data []byte) error {
	if _, err := t.wm.MicroBurn(context.Background(), t.did, InfraTollCost); err != nil {
		return fmt.Errorf("insufficient micrions for NATS publish (need %d): %w", InfraTollCost, err)
	}
	return t.underlying.Publish(subject, data)
}

func (t *TolledEventBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	if _, err := t.wm.MicroBurn(ctx, t.did, InfraTollCost); err != nil {
		return nil, fmt.Errorf("insufficient micrions for NATS request (need %d): %w", InfraTollCost, err)
	}
	return t.underlying.RequestWithContext(ctx, subject, data)
}

func (t *TolledEventBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	// Subscriptions do not cost micrions, only active outbound operations.
	return t.underlying.QueueSubscribe(subj, queue, cb, opts...)
}

// ============================================================================
// DATABASE INTERCEPTOR
// ============================================================================

// DBTX defines the common database abstraction used by sqlc
type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

// TolledDBTX wraps a database.DBTX (like pgxpool.Pool) to charge micrions for every DB hit.
type TolledDBTX struct {
	underlyingDB DBTX
	wm           *WalletManager
	did          string
}

// NewTolledDBTX creates a new Postgres DB interceptor.
func NewTolledDBTX(db DBTX, wm *WalletManager, did string) *TolledDBTX {
	return &TolledDBTX{
		underlyingDB: db,
		wm:           wm,
		did:          did,
	}
}

func (t *TolledDBTX) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if _, err := t.wm.MicroBurn(ctx, t.did, InfraTollCost); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("insufficient micrions for DB Exec (need %d): %w", InfraTollCost, err)
	}
	return t.underlyingDB.Exec(ctx, sql, args...)
}

func (t *TolledDBTX) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if _, err := t.wm.MicroBurn(ctx, t.did, InfraTollCost); err != nil {
		return nil, fmt.Errorf("insufficient micrions for DB Query (need %d): %w", InfraTollCost, err)
	}
	return t.underlyingDB.Query(ctx, sql, args...)
}

func (t *TolledDBTX) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if _, err := t.wm.MicroBurn(ctx, t.did, InfraTollCost); err != nil {
		return errorRow{err: fmt.Errorf("insufficient micrions for DB QueryRow (need %d): %w", InfraTollCost, err)}
	}
	return t.underlyingDB.QueryRow(ctx, sql, args...)
}

// errorRow implements pgx.Row for returning errors directly during QueryRow interception.
type errorRow struct {
	err error
}

func (e errorRow) Scan(dest ...any) error {
	return e.err
}

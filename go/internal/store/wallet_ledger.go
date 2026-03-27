package store

import (
	"context"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// WalletLedger implements the micrion.FiatLedger interface using Postgres.
type WalletLedger struct {
	queries *database.Queries
}

// NewWalletLedger creates a new Postgres-backed fiat ledger.
func NewWalletLedger(queries *database.Queries) *WalletLedger {
	return &WalletLedger{
		queries: queries,
	}
}

func (l *WalletLedger) LogPurchase(ctx context.Context, entityID string, stripeSessionID string, usdAmount int64, micrionAmount int64) error {
	eID, err := uuid.Parse(entityID)
	if err != nil {
		return err
	}
	pgID := pgtype.UUID{Bytes: eID, Valid: true}

	// Make sure the wallet exists
	_, err = l.queries.CreateWallet(ctx, pgID)
	if err != nil {
		return err
	}

	pgSession := pgtype.Text{String: stripeSessionID, Valid: stripeSessionID != ""}
	pgUSD := pgtype.Int8{Int64: usdAmount, Valid: true}

	_, err = l.queries.LogPurchase(ctx, database.LogPurchaseParams{
		EntityID:               pgID,
		StripeSessionID:        pgSession,
		UsdAmount:              pgUSD,
		MicrionAmount:          micrionAmount,
	})
	return err
}

func (l *WalletLedger) LogBulkBurn(ctx context.Context, entityID string, agentDID string, burnedAmount int64, natsRevision uint64) error {
	if entityID == "system" || entityID == "SYSTEM" {
		return nil // Natively bypass the Wallet Ledger SQL table entirely for core infrastructure algorithms
	}

	eID, err := uuid.Parse(entityID)
	if err != nil {
		return err
	}
	pgID := pgtype.UUID{Bytes: eID, Valid: true}
	pgRev := pgtype.Int8{Int64: int64(natsRevision), Valid: true}

	_, err = l.queries.LogBulkBurn(ctx, database.LogBulkBurnParams{
		EntityID:       pgID,
		NatsRevision:   pgRev,
		MicrionAmount:  burnedAmount,
	})
	return err
}

func (l *WalletLedger) GetFiatPurchased(ctx context.Context, entityID string) (int64, error) {
	eID, err := uuid.Parse(entityID)
	if err != nil {
		return 0, err
	}
	pgID := pgtype.UUID{Bytes: eID, Valid: true}

	wallet, err := l.queries.GetWallet(ctx, pgID)
	if err != nil {
		return 0, err
	}
	return wallet.TotalPurchasedMicrions, nil
}

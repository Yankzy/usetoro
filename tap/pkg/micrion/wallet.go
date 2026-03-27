package micrion

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
)

// FiatLedger defines the boundary between the NATS execution layer (TAP SDK)
// and the canonical PostgreSQL database (Toro Core).
type FiatLedger interface {
	// LogPurchase records a permanent USD to Micrion conversion from Stripe.
	LogPurchase(ctx context.Context, entityID string, stripeSessionID string, usdAmount int64, micrionAmount int64) error

	// LogBulkBurn atomically records NATS micro-burns into Postgres. The natsRevision
	// serves as the idempotency lock to prevent double-charging the firm.
	LogBulkBurn(ctx context.Context, entityID string, agentDID string, burnedAmount int64, natsRevision uint64) error

	// GetFiatPurchased returns the total historically purchased Micrions for this entity.
	GetFiatPurchased(ctx context.Context, entityID string) (int64, error)
}

// WalletManager orchestrates the dual-layer system. It holds both the FiatLedger
// (Postgres/Core) and the high-speed execution layer (NATS KV).
type WalletManager struct {
	ledger FiatLedger
	kv     nats.KeyValue
}

func NewWalletManager(ledger FiatLedger, kv nats.KeyValue) *WalletManager {
	return &WalletManager{
		ledger: ledger,
		kv:     kv,
	}
}

// HandleStripePurchase is invoked by a Toro Core processor or Webhook when Stripe signals payment success.
func (m *WalletManager) HandleStripePurchase(ctx context.Context, entityID, agentDID, txID string, usdAmount, micrionAmount int64) error {
	// 1. Write the purchase to the Fiat Ledger
	if err := m.ledger.LogPurchase(ctx, entityID, txID, usdAmount, micrionAmount); err != nil {
		return fmt.Errorf("failed to log purchase to fiat ledger: %w", err)
	}

	// 2. Push liquidity into the NATS Execution Layer
	if err := m.TopUp(agentDID, micrionAmount, entityID); err != nil {
		return fmt.Errorf("failed to top up execution layer: %w", err)
	}

	return nil
}

// GetAggregateBalance returns the currently unburned balance sitting in the KV layer.
func (m *WalletManager) GetAggregateBalance(ctx context.Context, agentDID string) (int64, error) {
	state, err := GetExecutionState(m.kv, agentDID)
	if err != nil {
		return 0, err
	}
	return state.Balance, nil
}

package pcm

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
)

type PcmBankReconciliationStore struct {
	deps domain_tools.ToolDependencies
}

func NewPcmBankReconciliationStore(deps domain_tools.ToolDependencies) *PcmBankReconciliationStore {
	return &PcmBankReconciliationStore{deps: deps}
}

func (s *PcmBankReconciliationStore) parseUUID(u string) pgtype.UUID {
	parsed, err := uuid.Parse(u)
	if err != nil {
		return pgtype.UUID{Valid: false}
	}
	var bytes [16]byte
	copy(bytes[:], parsed[:])
	return pgtype.UUID{Bytes: bytes, Valid: true}
}

func (s *PcmBankReconciliationStore) parseText(t string) pgtype.Text {
	return pgtype.Text{String: t, Valid: t != ""}
}

func (s *PcmBankReconciliationStore) PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	state := node.GetState()
	if state == ase.StateReadyForSync {
		return s.PersistReadyForSync(ctx, node)
	} else if state == ase.StateHoldMissingCtx || state == ase.StateHoldAmbiguous {
		return s.PersistHoldReason(ctx, node)
	}
	return nil
}

func (s *PcmBankReconciliationStore) PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	_, err := s.deps.DB.InsertOrderReconciliation(ctx, database.InsertOrderReconciliationParams{
		EntityID: s.parseUUID(node.TenantID),
		RealmID:  s.parseText(node.RealmID),
		Status:   "HOLDING",
	})
	if err != nil {
		s.deps.Logger.Error("Failed to persist hold reason", "error", err)
		return err
	}
	return nil
}

func (s *PcmBankReconciliationStore) PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	_, err := s.deps.DB.InsertOrderReconciliation(ctx, database.InsertOrderReconciliationParams{
		EntityID: s.parseUUID(node.TenantID),
		RealmID:  s.parseText(node.RealmID),
		Status:   "MATCHED",
	})
	if err != nil {
		s.deps.Logger.Error("Failed to persist ready for sync", "error", err)
		return err
	}
	return nil
}

func (s *PcmBankReconciliationStore) GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error) {
	return []byte("[]"), nil
}

func (s *PcmBankReconciliationStore) UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error {
	return nil
}

func (s *PcmBankReconciliationStore) CacheActiveAgent(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	return nil
}

func (s *PcmBankReconciliationStore) GetCachedAgent(ctx context.Context, nodeID string, cb func(*ase.AutonomousSemanticEngineNode, ase.NodeState, ase.NodeState)) (*ase.AutonomousSemanticEngineNode, error) {
	return nil, nil
}

func (s *PcmBankReconciliationStore) RemoveCachedAgent(ctx context.Context, nodeID string) error {
	return nil
}

func (s *PcmBankReconciliationStore) AcquireLock(ctx context.Context, nodeID string) (bool, error) {
	return true, nil
}

func (s *PcmBankReconciliationStore) ReleaseLock(ctx context.Context, nodeID string) error {
	return nil
}

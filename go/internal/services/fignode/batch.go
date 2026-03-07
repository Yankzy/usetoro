package fignode

import (
	"context"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type BatchService struct {
	db     *database.Queries
	logger *slog.Logger
	icons  *IconService
}

func NewBatchService(db *database.Queries, logger *slog.Logger, icons *IconService) *BatchService {
	return &BatchService{db: db, logger: logger, icons: icons}
}

func (s *BatchService) ServeBatch(ctx context.Context, userID uuid.UUID) ([]TransactionResponse, error) {
	pgUID := pgtype.UUID{Bytes: userID, Valid: true}

	txns, err := s.db.GetBatchTransactions(ctx, database.GetBatchTransactionsParams{
		UserID:     pgUID,
		BatchLimit: BatchSize,
	})
	if err != nil {
		return nil, err
	}

	txIDs := make([]string, len(txns))
	result := make([]TransactionResponse, len(txns))
	for i, t := range txns {
		txIDs[i] = t.ID
		result[i] = txToResponse(t, s.icons)
	}

	if len(txIDs) > 0 {
		if _, err := s.db.InsertBatch(ctx, database.InsertBatchParams{
			UserID:         pgUID,
			TransactionIds: txIDs,
		}); err != nil {
			s.logger.Error("failed to record batch", "error", err, "user_id", userID)
		}
	}

	return result, nil
}

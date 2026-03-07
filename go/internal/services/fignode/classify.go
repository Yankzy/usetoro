package fignode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ClassifyService struct {
	pool   *pgxpool.Pool
	db     *database.Queries
	logger *slog.Logger
}

func NewClassifyService(pool *pgxpool.Pool, db *database.Queries, logger *slog.Logger) *ClassifyService {
	return &ClassifyService{pool: pool, db: db, logger: logger}
}

func (s *ClassifyService) Classify(ctx context.Context, userID uuid.UUID, txnID, category, action string) (*ClassifyResponse, error) {
	valid, err := s.db.ValidateCategory(ctx, category)
	if err != nil {
		return nil, fmt.Errorf("validate category: %w", err)
	}
	if !valid {
		return nil, &appError{Code: "INVALID_CATEGORY", Message: "Category does not exist", Status: 400}
	}
	if action != "APPROVE" && action != "RECLASSIFY" {
		return nil, &appError{Code: "INVALID_ACTION", Message: "Action must be APPROVE or RECLASSIFY", Status: 400}
	}

	pgUID := pgtype.UUID{Bytes: userID, Valid: true}

	already, err := s.db.CheckUserAlreadyClassified(ctx, database.CheckUserAlreadyClassifiedParams{
		TransactionID: txnID, UserID: pgUID,
	})
	if err != nil {
		return nil, fmt.Errorf("check duplicate: %w", err)
	}
	if already {
		return nil, &appError{Code: "ALREADY_CLASSIFIED", Message: "User already classified this transaction", Status: 409}
	}

	var resp ClassifyResponse
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qtx := s.db.WithTx(tx)

		txn, err := qtx.GetTransactionForClassify(ctx, txnID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &appError{Code: "NOT_FOUND", Message: "Transaction not found", Status: 404}
			}
			return fmt.Errorf("lock transaction: %w", err)
		}
		if txn.Status != "open" {
			return &appError{Code: "NOT_FOUND", Message: "Transaction already processed", Status: 404}
		}

		_, err = qtx.InsertClassification(ctx, database.InsertClassificationParams{
			TransactionID: txnID,
			UserID:        pgUID,
			Category:      category,
			Action:        action,
			ApprovedBy:    pgUID, // Currently auto-approving by the employee themselves, could be adjusted for Junior -> Senior flow
		})
		if err != nil {
			return fmt.Errorf("insert classification: %w", err)
		}

		// Direct clearance model for employees:
		if err := qtx.ClearTransaction(ctx, txnID); err != nil {
			return fmt.Errorf("clear transaction: %w", err)
		}

		if err := qtx.IncrementEmployeeCleared(ctx, pgUID); err != nil {
			return fmt.Errorf("increment cleared: %w", err)
		}

		if err := qtx.UpdateStreak(ctx, pgUID); err != nil {
			return fmt.Errorf("update streak: %w", err)
		}

		profile, err := qtx.GetEmployeeProfileForUpdate(ctx, pgUID)
		if err != nil {
			return fmt.Errorf("get profile for badges: %w", err)
		}
		if err := CheckAndAwardBadges(ctx, qtx, pgUID, profile); err != nil {
			return fmt.Errorf("award badges: %w", err)
		}

		resp = ClassifyResponse{
			TransactionID: txnID,
			Category:      category,
			Action:        action,
			Status:        "cleared",
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// appError is a typed business-logic error surfaced to HTTP handlers.
type appError struct {
	Code    string
	Message string
	Status  int
}

func (e *appError) Error() string { return e.Message }

func AsAppError(err error) (*appError, bool) {
	var ae *appError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

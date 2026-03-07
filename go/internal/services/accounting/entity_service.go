package accounting

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp"
)

// EntityRepository defines the data access methods needed for fetching entities.
type EntityRepository interface {
	GetAccountsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error)
	GetVendorsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpVendor, error)
	GetCustomersByRealm(ctx context.Context, realmID string) ([]database.ShadowErpCustomer, error)
}

// EntityService handles pulling base ERPEntities natively from shadow databases
type EntityService struct {
	logger *slog.Logger
	repo   EntityRepository
}

// NewEntityService creates a new EntityService
func NewEntityService(logger *slog.Logger, repo EntityRepository) *EntityService {
	return &EntityService{
		logger: logger,
		repo:   repo,
	}
}

// FetchAccounts retrieves all accounts for a realm
func (s *EntityService) FetchAccounts(ctx context.Context, realmID string) ([]erp.Account, error) {
	if realmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	rows, err := s.repo.GetAccountsByRealm(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts for realm: %w", err)
	}

	var accounts []erp.Account
	for _, r := range rows {
		var balance float64
		if r.CurrentBalance.Valid {
			if v, err := r.CurrentBalance.Float64Value(); err == nil {
				balance = v.Float64
			}
		}

		currency := ""
		if r.CurrencyRefValue.Valid {
			currency = r.CurrencyRefValue.String
		}

		accType := r.AccountType

		accSubType := ""
		if r.AccountSubType.Valid {
			accSubType = r.AccountSubType.String
		}

		classification := ""
		if r.Classification.Valid {
			classification = r.Classification.String
		}

		fullyQualifiedName := ""
		if r.FullyQualifiedName.Valid {
			fullyQualifiedName = r.FullyQualifiedName.String
		}

		accounts = append(accounts, erp.Account{
			ToroID:             fmt.Sprintf("%x-%x-%x-%x-%x", r.ID.Bytes[0:4], r.ID.Bytes[4:6], r.ID.Bytes[6:8], r.ID.Bytes[8:10], r.ID.Bytes[10:16]),
			RealmID:            r.RealmID,
			ExternalID:         r.ErpID,
			Name:               r.Name,
			AccountType:        accType,
			AccountSubType:     accSubType,
			Classification:     classification,
			FullyQualifiedName: fullyQualifiedName,
			Active:             r.Active.Bool,
			CurrentBalance:     balance,
			SyncToken:          r.SyncToken,
			Currency:           currency,
			CreatedAt:          r.CreatedAt.Time,
			UpdatedAt:          r.UpdatedAt.Time,
		})
	}

	return accounts, nil
}

// FetchVendors retrieves all vendors for a realm
func (s *EntityService) FetchVendors(ctx context.Context, realmID string) ([]erp.Vendor, error) {
	if realmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	rows, err := s.repo.GetVendorsByRealm(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get vendors for realm: %w", err)
	}

	var vendors []erp.Vendor
	for _, r := range rows {
		vendors = append(vendors, erp.Vendor{
			ToroID:      fmt.Sprintf("%x-%x-%x-%x-%x", r.ID.Bytes[0:4], r.ID.Bytes[4:6], r.ID.Bytes[6:8], r.ID.Bytes[8:10], r.ID.Bytes[10:16]),
			RealmID:     r.RealmID,
			ExternalID:  r.ErpID,
			DisplayName: r.DisplayName,
			Active:      true, // Usually true unless marked deleted (we skip deleted anyway)
			SyncToken:   r.SyncToken,
			CreatedAt:   r.CreatedAt.Time,
			UpdatedAt:   r.UpdatedAt.Time,
		})
	}

	return vendors, nil
}

// FetchCustomers retrieves all customers for a realm
func (s *EntityService) FetchCustomers(ctx context.Context, realmID string) ([]erp.Customer, error) {
	if realmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	rows, err := s.repo.GetCustomersByRealm(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get customers for realm: %w", err)
	}

	var customers []erp.Customer
	for _, r := range rows {
		customers = append(customers, erp.Customer{
			ToroID:      fmt.Sprintf("%x-%x-%x-%x-%x", r.ID.Bytes[0:4], r.ID.Bytes[4:6], r.ID.Bytes[6:8], r.ID.Bytes[8:10], r.ID.Bytes[10:16]),
			RealmID:     r.RealmID,
			ExternalID:  r.ErpID,
			DisplayName: r.DisplayName,
			Active:      true, // Usually true unless marked deleted
			SyncToken:   r.SyncToken,
			CreatedAt:   r.CreatedAt.Time,
			UpdatedAt:   r.UpdatedAt.Time,
		})
	}

	return customers, nil
}

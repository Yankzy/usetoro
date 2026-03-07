package accounting

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/erp"
)

// UploadReceiptInput contains everything needed to upload a receipt.
// Deprecated: use erp.UploadReceiptInput instead
type UploadReceiptInput = erp.UploadReceiptInput

// UploadedReceipt is returned after a successful receipt upload.
// Deprecated: use erp.UploadedReceipt instead
type UploadedReceipt = erp.UploadedReceipt

// AttachableService handles the upload and linking of receipt files to ERP transactions.
type AttachableService struct {
	logger  *slog.Logger
	factory erp.ProviderFactory
}

// NewAttachableService creates a new AttachableService.
func NewAttachableService(logger *slog.Logger, factory erp.ProviderFactory) *AttachableService {
	return &AttachableService{
		logger:  logger,
		factory: factory,
	}
}

// UploadReceipt uploads the given file to the ERP as an attachment and links it to
// the specified transaction entity. It returns the created attachment metadata.
func (s *AttachableService) UploadReceipt(ctx context.Context, input erp.UploadReceiptInput) (*erp.UploadedReceipt, error) {
	return s.uploadReceipt(ctx, input, nil)
}

// uploadReceipt is the internal implementation, accepting an optional override
// for testing (nil means use the real Provider from factory).
func (s *AttachableService) uploadReceipt(
	ctx context.Context,
	input erp.UploadReceiptInput,
	providerOverride *erp.Provider,
) (*erp.UploadedReceipt, error) {
	if input.RealmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}
	if input.FileName == "" {
		return nil, fmt.Errorf("FileName is required")
	}
	if input.EntityID == "" || input.EntityType == "" {
		return nil, fmt.Errorf("EntityID and EntityType are required to link the receipt")
	}
	if input.Data == nil {
		return nil, fmt.Errorf("Data reader is required")
	}

	var provider *erp.Provider = providerOverride
	if provider == nil {
		var err error
		// For AttachableService, input.RealmID actually refers to the entity/tenant in Toro's context,
		// but historically it's been the physical realm ID. We'll use the factory's realm resolver for now
		// (assuming quickbooks_online since that's what we are replacing).
		provider, err = s.factory.GetProviderForRealm(ctx, "quickbooks_online", input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get ERP provider: %w", err)
		}
	}

	// 1. Upload file + metadata
	created, err := provider.UploadReceipt(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to upload attachable to ERP: %w", err)
	}

	s.logger.Info("✅ Uploaded receipt to ERP",
		"realm_id", input.RealmID,
		"attachable_id", created.AttachmentID,
		"entity_id", input.EntityID,
		"entity_type", input.EntityType,
		"file_name", input.FileName,
	)

	return created, nil
}

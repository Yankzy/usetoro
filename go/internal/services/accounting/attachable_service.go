package accounting

import (
	"bytes"
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

// AttachableService handles the upload and linking of attachable files to ERP transactions.
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

// UploadAttachable handles the "Multipart Request Upload & Link" flow.
// It constructs the specific two-part QBO API request needed for Attachables.
func (s *AttachableService) UploadAttachable(ctx context.Context, realmID string, entityType string, entityID string, fileBytes []byte, filename string, contentType string) error {
	s.logger.Info("uploading multipart attachable", "realm_id", realmID, "entity_type", entityType, "entity_id", entityID, "filename", filename)

	provider, err := s.factory.GetProviderForRealm(ctx, "quickbooks_online", realmID)
	if err != nil {
		return fmt.Errorf("failed to get provider for realm: %w", err)
	}

	_, err = provider.UploadReceipt(ctx, erp.UploadReceiptInput{
		RealmID:     realmID,
		EntityType:  entityType,
		EntityID:    entityID,
		FileName:    filename,
		ContentType: contentType,
		Data:        bytes.NewReader(fileBytes),
	})

	return err
}

package accounting

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	quickbooks "github.com/Yankzy/usetoro/qbo"
)

// attachableUploader is an internal interface satisfied by *quickbooks.Client.
// Extracted to enable unit testing without live HTTP.
type attachableUploader interface {
	UploadAttachable(*quickbooks.Attachable, io.Reader) (*quickbooks.Attachable, error)
	QueryAttachables(string) ([]quickbooks.Attachable, error)
}

// UploadReceiptInput contains everything needed to upload a receipt and link it
// to an existing QBO transaction.
type UploadReceiptInput struct {
	RealmID     string
	FileName    string
	ContentType quickbooks.ContentType
	Data        io.Reader
	EntityID    string // QBO transaction ID to link the receipt to
	EntityType  string // "Purchase" or "Bill"
	Note        string // optional description shown on the attachment in QBO
}

// UploadedReceipt is returned after a successful receipt upload.
type UploadedReceipt struct {
	AttachableID   string
	FileAccessURI  string // direct QBO storage URL
	LinkedEntityID string
}

// AttachableService handles the upload and linking of receipt files to QBO transactions.
type AttachableService struct {
	logger   *slog.Logger
	clientFn QBOClientFn // reuses the same function type as TransactionService
}

// NewAttachableService creates a new AttachableService.
func NewAttachableService(logger *slog.Logger, clientFn QBOClientFn) *AttachableService {
	return &AttachableService{
		logger:   logger,
		clientFn: clientFn,
	}
}

// UploadReceipt uploads the given file to QBO as an Attachable and links it to
// the specified transaction entity. It returns the created Attachable metadata.
//
// Duplicate detection: if a file with the same FileName is already linked to
// the same EntityID, the existing AttachableID is returned without re-uploading.
func (s *AttachableService) UploadReceipt(ctx context.Context, input UploadReceiptInput) (*UploadedReceipt, error) {
	return s.uploadReceipt(ctx, input, nil)
}

// uploadReceipt is the internal implementation, accepting an optional uploader override
// for testing (nil means use the real *quickbooks.Client from clientFn).
func (s *AttachableService) uploadReceipt(
	ctx context.Context,
	input UploadReceiptInput,
	uploaderOverride attachableUploader,
) (*UploadedReceipt, error) {
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

	var uploader attachableUploader = uploaderOverride
	if uploader == nil {
		client, err := s.clientFn(ctx, input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO client: %w", err)
		}
		uploader = client
	}

	// 1. Duplicate detection — query QBO for an existing attachable linked to this entity
	existing, err := s.findExistingAttachable(uploader, input.EntityID, input.FileName)
	if err != nil {
		// Non-fatal: log and continue with upload
		s.logger.Warn("Duplicate check failed, proceeding with upload", "error", err)
	} else if existing != nil {
		s.logger.Info("Receipt already exists for entity, skipping upload",
			"attachable_id", existing.Id,
			"entity_id", input.EntityID,
		)
		return &UploadedReceipt{
			AttachableID:   existing.Id,
			FileAccessURI:  existing.FileAccessUri,
			LinkedEntityID: input.EntityID,
		}, nil
	}

	// 2. Build the Attachable metadata with EntityRef to link it upon upload
	attachable := &quickbooks.Attachable{
		FileName:    input.FileName,
		ContentType: input.ContentType,
		Note:        input.Note,
		AttachableRef: []quickbooks.AttachableRef{
			{
				EntityRef: quickbooks.ReferenceType{
					Value: input.EntityID,
					Type:  input.EntityType,
				},
			},
		},
	}

	// 3. Upload file + metadata in a single multipart request
	created, err := uploader.UploadAttachable(attachable, input.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to upload attachable to QBO: %w", err)
	}

	s.logger.Info("✅ Uploaded receipt to QBO",
		"realm_id", input.RealmID,
		"attachable_id", created.Id,
		"entity_id", input.EntityID,
		"entity_type", input.EntityType,
		"file_name", input.FileName,
	)

	return &UploadedReceipt{
		AttachableID:   created.Id,
		FileAccessURI:  created.FileAccessUri,
		LinkedEntityID: input.EntityID,
	}, nil
}

// findExistingAttachable queries QBO for an Attachable already linked to entityID
// with the given fileName. Returns nil (not an error) when none is found.
func (s *AttachableService) findExistingAttachable(
	uploader attachableUploader,
	entityID, fileName string,
) (*quickbooks.Attachable, error) {
	query := fmt.Sprintf(
		`SELECT * FROM Attachable WHERE AttachableRef.EntityRef.value = '%s' AND FileName = '%s' MAXRESULTS 1`,
		entityID, fileName,
	)
	results, err := uploader.QueryAttachables(query)
	if err != nil {
		// QueryAttachables returns an error when the query yields no results — treat as "not found"
		return nil, nil //nolint:nilerr
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

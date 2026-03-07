package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"

	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/erp"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

// UploadReceiptInput contains everything needed to upload a receipt.
// Deprecated: use erp.UploadReceiptInput instead
type UploadReceiptInput = erp.UploadReceiptInput

// UploadedReceipt is returned after a successful receipt upload.
// Deprecated: use erp.UploadedReceipt instead
type UploadedReceipt = erp.UploadedReceipt

// AttachableService handles the upload and linking of attachable files to ERP transactions.
type AttachableService struct {
	logger       *slog.Logger
	qboConnector *connectors.QBOConnector
	factory      erp.ProviderFactory
}

// NewAttachableService creates a new AttachableService.
func NewAttachableService(logger *slog.Logger, qboConnector *connectors.QBOConnector, factory erp.ProviderFactory) *AttachableService {
	return &AttachableService{
		logger:       logger,
		qboConnector: qboConnector,
		factory:      factory,
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

// UploadAttachable handles the "Multipart Request Upload & Link" flow natively via the QBOConnector.
// It constructs the specific two-part QBO API request needed for Attachables.
func (s *AttachableService) UploadAttachable(ctx context.Context, realmID string, entityType string, entityID string, fileBytes []byte, filename string, contentType string) error {
	s.logger.Info("uploading multipart attachable directly via QBOConnector", "realm_id", realmID, "entity_type", entityType, "entity_id", entityID, "filename", filename)

	client, err := s.qboConnector.ClientForRealm(ctx, realmID)
	if err != nil {
		return fmt.Errorf("failed to get qbo client for realm: %w", err)
	}

	endpointUrl, err := url.Parse(client.GetEndpoint() + "upload")
	if err != nil {
		return fmt.Errorf("failed to parse upload url: %w", err)
	}

	urlValues := url.Values{}
	urlValues.Add("minorversion", "75") // Hardcoded to 75 as standard for QBO minor versions
	endpointUrl.RawQuery = urlValues.Encode()

	// Build the Attachable Request
	attachable := quickbooks.Attachable{
		FileName:    filename,
		ContentType: quickbooks.ContentType(contentType),
		AttachableRef: []quickbooks.AttachableRef{
			{
				EntityRef: quickbooks.ReferenceType{
					Value: entityID,
					Type:  entityType,
				},
			},
		},
	}

	var buffer bytes.Buffer
	mWriter := multipart.NewWriter(&buffer)

	// Add file metadata part
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "file_metadata_01", "attachment.json"))
	metadataHeader.Set("Content-Type", "application/json")

	metadataContent, err := mWriter.CreatePart(metadataHeader)
	if err != nil {
		return fmt.Errorf("failed to create metadata part: %w", err)
	}

	j, err := json.Marshal(attachable)
	if err != nil {
		return fmt.Errorf("failed to marshal attachable metadata: %w", err)
	}

	if _, err = metadataContent.Write(j); err != nil {
		return fmt.Errorf("failed to write metadata part: %w", err)
	}

	// Add file content part
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "file_content_01", filename))
	fileHeader.Set("Content-Type", contentType)

	fileContent, err := mWriter.CreatePart(fileHeader)
	if err != nil {
		return fmt.Errorf("failed to create file content part: %w", err)
	}

	if _, err = io.Copy(fileContent, bytes.NewReader(fileBytes)); err != nil {
		return fmt.Errorf("failed to copy file bytes: %w", err)
	}

	if err := mWriter.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpointUrl.String(), &buffer)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Add("Content-Type", mWriter.FormDataContentType())
	req.Header.Add("Accept", "application/json")

	resp, err := client.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	s.logger.Info("successfully uploaded and linked attachable", "realm_id", realmID, "entity_type", entityType, "entity_id", entityID)
	return nil
}

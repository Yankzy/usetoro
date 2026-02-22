package accounting

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	quickbooks "github.com/Yankzy/usetoro/qbo"
)

// ─── Mock implementation ─────────────────────────────────────────────────────

// mockUploader satisfies attachableUploader.
type mockUploader struct {
	uploadResult *quickbooks.Attachable
	uploadErr    error
	queryResult  []quickbooks.Attachable
	queryErr     error
	capturedMeta *quickbooks.Attachable
	capturedData io.Reader
	uploadCalled bool
	queryCalled  bool
	lastQuery    string
}

func (m *mockUploader) UploadAttachable(a *quickbooks.Attachable, data io.Reader) (*quickbooks.Attachable, error) {
	m.uploadCalled = true
	m.capturedMeta = a
	m.capturedData = data
	if m.uploadErr != nil {
		return nil, m.uploadErr
	}
	if m.uploadResult != nil {
		return m.uploadResult, nil
	}
	return &quickbooks.Attachable{
		Id:            "att-default",
		FileAccessUri: "https://qbo.intuit.com/files/att-default",
	}, nil
}

func (m *mockUploader) QueryAttachables(query string) ([]quickbooks.Attachable, error) {
	m.queryCalled = true
	m.lastQuery = query
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.queryResult, nil
}

// callAttach is a helper that calls the internal uploadReceipt with the mock.
func callAttach(svc *AttachableService, input UploadReceiptInput, u attachableUploader) (*UploadedReceipt, error) {
	return svc.uploadReceipt(context.Background(), input, u)
}

func newAttachSvc() *AttachableService {
	return NewAttachableService(logger(), failClientFn("should not call clientFn in unit tests"))
}

// ─── Constructor ─────────────────────────────────────────────────────────────

func TestNewAttachableService_IsNonNil(t *testing.T) {
	svc := NewAttachableService(logger(), failClientFn("unused"))
	assert.NotNil(t, svc)
}

// ─── Validation ──────────────────────────────────────────────────────────────

func TestUploadReceipt_MissingRealmID(t *testing.T) {
	svc := newAttachSvc()
	_, err := callAttach(svc, UploadReceiptInput{
		FileName:   "r.pdf",
		EntityID:   "e1",
		EntityType: "Purchase",
		Data:       strings.NewReader("data"),
	}, &mockUploader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realmID is required")
}

func TestUploadReceipt_MissingFileName(t *testing.T) {
	svc := newAttachSvc()
	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		EntityID:   "e1",
		EntityType: "Purchase",
		Data:       strings.NewReader("data"),
	}, &mockUploader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FileName is required")
}

func TestUploadReceipt_MissingEntityID(t *testing.T) {
	svc := newAttachSvc()
	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:  "r1",
		FileName: "r.pdf",
		Data:     strings.NewReader("data"),
		// EntityID omitted
	}, &mockUploader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EntityID and EntityType are required")
}

func TestUploadReceipt_MissingEntityType(t *testing.T) {
	svc := newAttachSvc()
	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:  "r1",
		FileName: "r.pdf",
		EntityID: "e1",
		Data:     strings.NewReader("data"),
		// EntityType omitted
	}, &mockUploader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EntityID and EntityType are required")
}

func TestUploadReceipt_NilDataReader(t *testing.T) {
	svc := newAttachSvc()
	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "r.pdf",
		EntityID:   "e1",
		EntityType: "Purchase",
		Data:       nil,
	}, &mockUploader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Data reader is required")
}

// ─── Happy path ───────────────────────────────────────────────────────────────

func TestUploadReceipt_HappyPath(t *testing.T) {
	uploader := &mockUploader{
		uploadResult: &quickbooks.Attachable{
			Id:            "att-001",
			FileAccessUri: "https://qbo.intuit.com/files/att-001",
		},
	}
	svc := newAttachSvc()

	result, err := callAttach(svc, UploadReceiptInput{
		RealmID:     "realm-1",
		FileName:    "receipt.pdf",
		ContentType: quickbooks.PDF,
		Data:        strings.NewReader("pdf-bytes"),
		EntityID:    "purchase-123",
		EntityType:  "Purchase",
		Note:        "Office supplies",
	}, uploader)

	require.NoError(t, err)
	assert.Equal(t, "att-001", result.AttachableID)
	assert.Equal(t, "https://qbo.intuit.com/files/att-001", result.FileAccessURI)
	assert.Equal(t, "purchase-123", result.LinkedEntityID)
}

func TestUploadReceipt_EntityRefIsCorrectlyLinked(t *testing.T) {
	uploader := &mockUploader{}
	svc := newAttachSvc()

	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:     "r1",
		FileName:    "receipt.jpg",
		ContentType: quickbooks.JPEG,
		Data:        strings.NewReader("jpg"),
		EntityID:    "bill-456",
		EntityType:  "Bill",
	}, uploader)

	require.NoError(t, err)
	require.NotNil(t, uploader.capturedMeta)
	require.Len(t, uploader.capturedMeta.AttachableRef, 1)
	ref := uploader.capturedMeta.AttachableRef[0].EntityRef
	assert.Equal(t, "bill-456", ref.Value)
	assert.Equal(t, "Bill", ref.Type)
}

func TestUploadReceipt_FileNameAndContentTypeInMeta(t *testing.T) {
	uploader := &mockUploader{}
	svc := newAttachSvc()

	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:     "r1",
		FileName:    "invoice.png",
		ContentType: quickbooks.PNG,
		Data:        strings.NewReader("png"),
		EntityID:    "p1",
		EntityType:  "Purchase",
	}, uploader)

	require.NoError(t, err)
	assert.Equal(t, "invoice.png", uploader.capturedMeta.FileName)
	assert.Equal(t, quickbooks.PNG, uploader.capturedMeta.ContentType)
}

func TestUploadReceipt_NoteIsPassedThrough(t *testing.T) {
	uploader := &mockUploader{}
	svc := newAttachSvc()

	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "r.pdf",
		Data:       strings.NewReader("pdf"),
		EntityID:   "p1",
		EntityType: "Purchase",
		Note:       "Reimbursable: team offsite",
	}, uploader)

	require.NoError(t, err)
	assert.Equal(t, "Reimbursable: team offsite", uploader.capturedMeta.Note)
}

// ─── Upload error ─────────────────────────────────────────────────────────────

func TestUploadReceipt_UploadErrorPropagates(t *testing.T) {
	uploader := &mockUploader{uploadErr: errors.New("QBO upload: 413 payload too large")}
	svc := newAttachSvc()

	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "huge.pdf",
		Data:       strings.NewReader("big"),
		EntityID:   "p1",
		EntityType: "Purchase",
	}, uploader)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload attachable")
	assert.Contains(t, err.Error(), "413 payload too large")
}

// ─── clientFn error (public UploadReceipt) ────────────────────────────────────

func TestUploadReceipt_ClientFnErrorPropagates(t *testing.T) {
	svc := NewAttachableService(logger(), failClientFn("bad credentials"))
	// Call PUBLIC UploadReceipt — this resolves clientFn
	_, err := svc.UploadReceipt(context.Background(), UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "r.pdf",
		Data:       strings.NewReader("pdf"),
		EntityID:   "p1",
		EntityType: "Purchase",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get QBO client")
	assert.Contains(t, err.Error(), "bad credentials")
}

// ─── Deduplication ────────────────────────────────────────────────────────────

func TestUploadReceipt_DeduplicateReturnsExistingWithoutUpload(t *testing.T) {
	existing := quickbooks.Attachable{
		Id:            "att-existing",
		FileAccessUri: "https://qbo.intuit.com/files/existing",
	}
	uploader := &mockUploader{queryResult: []quickbooks.Attachable{existing}}
	svc := newAttachSvc()

	result, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "receipt.pdf",
		Data:       strings.NewReader("pdf"),
		EntityID:   "p1",
		EntityType: "Purchase",
	}, uploader)

	require.NoError(t, err)
	assert.Equal(t, "att-existing", result.AttachableID)
	assert.Equal(t, "https://qbo.intuit.com/files/existing", result.FileAccessURI)
	assert.False(t, uploader.uploadCalled, "UploadAttachable must NOT be called when duplicate found")
}

func TestUploadReceipt_DeduplicateQueryContainsEntityAndFileName(t *testing.T) {
	uploader := &mockUploader{} // empty queryResult → no duplicate
	svc := newAttachSvc()

	_, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "my-receipt.pdf",
		Data:       strings.NewReader("data"),
		EntityID:   "txn-999",
		EntityType: "Purchase",
	}, uploader)

	require.NoError(t, err)
	assert.True(t, uploader.queryCalled)
	assert.Contains(t, uploader.lastQuery, "txn-999")
	assert.Contains(t, uploader.lastQuery, "my-receipt.pdf")
}

func TestUploadReceipt_QueryErrorIsNonFatalAndProceedsWithUpload(t *testing.T) {
	// Simulate a QBO query error — service should log a warning and still upload
	uploader := &mockUploader{queryErr: errors.New("network timeout")}
	svc := newAttachSvc()

	result, err := callAttach(svc, UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "r.pdf",
		Data:       strings.NewReader("pdf"),
		EntityID:   "p1",
		EntityType: "Purchase",
	}, uploader)

	require.NoError(t, err)
	assert.True(t, uploader.uploadCalled, "should proceed with upload despite query error")
	assert.Equal(t, "att-default", result.AttachableID)
}

// ─── findExistingAttachable unit tests ───────────────────────────────────────

func TestFindExistingAttachable_ReturnsNilWhenNotFound(t *testing.T) {
	svc := newAttachSvc()
	uploader := &mockUploader{queryResult: []quickbooks.Attachable{}}
	result, err := svc.findExistingAttachable(uploader, "e1", "r.pdf")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestFindExistingAttachable_ReturnsNilOnQueryError(t *testing.T) {
	// Query errors are intentionally swallowed — treated as "not found"
	svc := newAttachSvc()
	uploader := &mockUploader{queryErr: errors.New("QBO 500")}
	result, err := svc.findExistingAttachable(uploader, "e1", "r.pdf")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestFindExistingAttachable_ReturnsFirstResultWhenFound(t *testing.T) {
	svc := newAttachSvc()
	att := quickbooks.Attachable{Id: "att-found", FileAccessUri: "https://qbo/found"}
	uploader := &mockUploader{queryResult: []quickbooks.Attachable{att}}
	result, err := svc.findExistingAttachable(uploader, "e1", "r.pdf")
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "att-found", result.Id)
}

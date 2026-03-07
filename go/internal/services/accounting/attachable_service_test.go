package accounting

import (
	"context"
	"io"
	"strings"
	"testing"

	"log/slog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Yankzy/usetoro/internal/erp"
)

func attachLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type attachMockProviderFactory struct {
	provider *erp.Provider
}

func (m *attachMockProviderFactory) GetProviderForTenant(ctx context.Context, tenantID string) (*erp.Provider, error) {
	return m.provider, nil
}
func (m *attachMockProviderFactory) GetProviderForRealm(ctx context.Context, erpSystem, realmID string) (*erp.Provider, error) {
	if m.provider == nil {
		return nil, context.DeadlineExceeded
	}
	return m.provider, nil
}

func newAttachSvc(p *erp.Provider) *AttachableService {
	return NewAttachableService(attachLogger(), nil, &attachMockProviderFactory{provider: p})
}

func TestUploadReceipt_MissingRealmID(t *testing.T) {
	svc := newAttachSvc(nil)
	_, err := svc.UploadReceipt(context.Background(), erp.UploadReceiptInput{
		FileName:   "r.pdf",
		EntityID:   "e1",
		EntityType: "Purchase",
		Data:       strings.NewReader("data"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realmID is required")
}

func TestUploadReceipt_Success(t *testing.T) {
	var gotInput erp.UploadReceiptInput
	p := &erp.Provider{
		UploadReceipt: func(ctx context.Context, input erp.UploadReceiptInput) (*erp.UploadedReceipt, error) {
			gotInput = input
			return &erp.UploadedReceipt{AttachmentID: "att-1"}, nil
		},
	}
	svc := newAttachSvc(p)
	res, err := svc.UploadReceipt(context.Background(), erp.UploadReceiptInput{
		RealmID:    "r1",
		FileName:   "r.pdf",
		EntityID:   "e1",
		EntityType: "Purchase",
		Data:       strings.NewReader("data"),
	})
	require.NoError(t, err)
	assert.Equal(t, "att-1", res.AttachmentID)
	assert.Equal(t, "r.pdf", gotInput.FileName)
}

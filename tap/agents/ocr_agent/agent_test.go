package ocr_agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeOCRResultCache struct {
	values map[string]string
	getErr error
	setErr error
	delErr error
	setKey string
	setTTL time.Duration
	delKey string
}

func (f *fakeOCRResultCache) Get(_ context.Context, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	value, ok := f.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (f *fakeOCRResultCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	f.setKey = key
	f.setTTL = ttl
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	return nil
}

func (f *fakeOCRResultCache) Del(_ context.Context, key string) error {
	f.delKey = key
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.values, key)
	return nil
}

func TestOCRTaskPayload_Unmarshal(t *testing.T) {
	payloadJSON := `{
		"session_id": "051d1255-7b70-4b44-968e-fe996b806bbf",
		"external_id": "msg-999",
		"from_handle": "yankz@fignode.com",
		"to_handle": "rap_atlas_sarl@a.usetoro.io",
		"s3_key": "4b49cc2e-facture.pdf",
		"sha256": "812192fb4890feb1d46d6245e7d33355ed627a508d02a8c6e1e17fd209b22055",
		"attachment_name": "facture.pdf",
		"attachment_index": 0,
		"total_attachments": 2,
		"final_destination_subject": "worker.inbox.pcm",
		"subject": "Invoices",
		"agent_alias": "rap_atlas_sarl"
	}`

	var payload OCRTaskPayload
	err := json.Unmarshal([]byte(payloadJSON), &payload)
	require.NoError(t, err)

	assert.Equal(t, "051d1255-7b70-4b44-968e-fe996b806bbf", payload.SessionID)
	assert.Equal(t, "facture.pdf", payload.AttachmentName)
	assert.Equal(t, 0, payload.AttachmentIndex)
	assert.Equal(t, 2, payload.TotalAttachments)
	assert.Equal(t, "worker.inbox.pcm", payload.FinalDestinationSubject)
}

func TestOCRExtraction_Structure(t *testing.T) {
	ext := OCRExtraction{
		DocType:    "invoice",
		FileName:   "facture.pdf",
		Confidence: 0.98,
		Data: map[string]interface{}{
			"vendor_name": "Atlas SARL",
			"total_mad":   1250.50,
		},
	}

	bytes, err := json.Marshal(ext)
	require.NoError(t, err)

	var unmarshaled OCRExtraction
	err = json.Unmarshal(bytes, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, "invoice", unmarshaled.DocType)
	assert.Equal(t, "facture.pdf", unmarshaled.FileName)
	assert.Equal(t, 0.98, unmarshaled.Confidence)
	assert.Equal(t, "Atlas SARL", unmarshaled.Data["vendor_name"])
}

func TestOCRTaskPayload_GetFileURL(t *testing.T) {
	p1 := OCRTaskPayload{DocumentURL: "https://s3.amazonaws.com/bucket/doc.pdf"}
	assert.Equal(t, "https://s3.amazonaws.com/bucket/doc.pdf", p1.GetFileURL())

	p2 := OCRTaskPayload{S3URL: "https://s3.amazonaws.com/bucket/doc.pdf"}
	assert.Equal(t, "https://s3.amazonaws.com/bucket/doc.pdf", p2.GetFileURL())

	p3 := OCRTaskPayload{ImageURL: "https://s3.amazonaws.com/bucket/img.png"}
	assert.Equal(t, "https://s3.amazonaws.com/bucket/img.png", p3.GetFileURL())

	p4 := OCRTaskPayload{S3Key: "https://s3.amazonaws.com/bucket/presigned.pdf"}
	assert.Equal(t, "https://s3.amazonaws.com/bucket/presigned.pdf", p4.GetFileURL())
}

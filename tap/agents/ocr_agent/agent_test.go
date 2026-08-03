package ocr_agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		SHA256:     "sha256test",
		S3Key:      "s3keytest",
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

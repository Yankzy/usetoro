package workers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPcmInboundMessage_UnmarshalMultiAttachment(t *testing.T) {
	rawJSON := `{
		"session_id": "051d1255-7b70-4b44-968e-fe996b806bbf",
		"external_id": "msg-12345",
		"from_handle": "yankz@fignode.com",
		"to_handle": "rap_atlas_sarl@a.usetoro.io",
		"subject": "Invoices and Statements",
		"agent_alias": "rap_atlas_sarl",
		"total_attachments": 2,
		"attachments": [
			{
				"name": "facture.pdf",
				"document_url": "https://s3.example.com/key-1"
			},
			{
				"name": "releve.pdf",
				"document_url": "https://s3.example.com/key-2"
			}
		],
		"ocr_extractions": [
			{
				"doc_type": "invoice",
				"file_name": "facture.pdf",
				"data": {"total_mad": 5000}
			},
			{
				"doc_type": "bank_statement",
				"file_name": "releve.pdf",
				"data": {"period": "2026-07"}
			}
		]
	}`

	var msg PcmInboundMessage
	err := json.Unmarshal([]byte(rawJSON), &msg)
	require.NoError(t, err)

	assert.Equal(t, "051d1255-7b70-4b44-968e-fe996b806bbf", msg.SessionID)
	assert.Equal(t, "yankz@fignode.com", msg.FromHandle)
	assert.Equal(t, "rap_atlas_sarl@a.usetoro.io", msg.ToHandle)
	assert.Equal(t, 2, msg.TotalAttachments)
	assert.Len(t, msg.Attachments, 2)
	assert.Equal(t, "facture.pdf", msg.Attachments[0].Name)
	assert.Equal(t, "releve.pdf", msg.Attachments[1].Name)
	assert.Len(t, msg.OCRExtractions, 2)
	assert.Equal(t, "invoice", msg.OCRExtractions[0].DocType)
	assert.Equal(t, "bank_statement", msg.OCRExtractions[1].DocType)
}

func TestPcmInboundMessage_PythonOCRDelegationPayload(t *testing.T) {
	inbound := PcmInboundMessage{
		SessionID:  "sess-999",
		FromHandle: "test@example.com",
		ToHandle:   "rap_client@a.usetoro.io",
		Attachments: []PcmAttachment{
			{
				Name:        "releve.pdf",
				DocumentURL: "https://voxprofit.s3.us-east-1.amazonaws.com/docs/releve.pdf",
			},
			{
				Name:        "invoice.pdf",
				DocumentURL: "https://voxprofit.s3.us-east-1.amazonaws.com/docs/invoice.pdf",
			},
		},
	}

	payload := map[string]interface{}{
		"session_id":                inbound.SessionID,
		"from_handle":               inbound.FromHandle,
		"to_handle":                 inbound.ToHandle,
		"attachments":               inbound.Attachments,
		"total_attachments":         len(inbound.Attachments),
		"final_destination_subject": "worker.inbox.pcm",
	}

	bytes, err := json.Marshal(payload)
	require.NoError(t, err)

	var unmarshaled map[string]interface{}
	err = json.Unmarshal(bytes, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, "sess-999", unmarshaled["session_id"])
	assert.Equal(t, "worker.inbox.pcm", unmarshaled["final_destination_subject"])
	atts, ok := unmarshaled["attachments"].([]interface{})
	require.True(t, ok)
	assert.Len(t, atts, 2)
}

func TestExtractOCRDocuments_DifferentiatesInvoiceAndBankStatement(t *testing.T) {
	w := &PcmWorker{}
	inbound := PcmInboundMessage{
		OCRExtractions: []PcmOCRExtraction{
			{
				DocType:  "invoice",
				FileName: "invoice1.pdf",
				Data:     map[string]interface{}{"total": 1500.0},
			},
			{
				DocType:  "bank_statement",
				FileName: "statement1.pdf",
				Data:     map[string]interface{}{"transactions": []interface{}{map[string]interface{}{"amount": 1500.0, "description": "Payment"}}},
			},
		},
	}

	docs := w.extractOCRDocuments(&inbound)
	require.Len(t, docs, 2)
	assert.Equal(t, "invoice", docs[0].Type)
	assert.Equal(t, "bank_statement", docs[1].Type)

	// Verify filtering for bank statement documents
	hasBankStatement := false
	for _, doc := range docs {
		if doc.Type == "bank_statement" {
			hasBankStatement = true
		}
	}
	assert.True(t, hasBankStatement)
}




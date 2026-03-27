package quickbooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateCompanyInfoContext(t *testing.T) {
	client, _ := NewClient("testClientId", "testClientSecret", "testRealm", false, "65", nil, nil)

	fetchCallCount := 0
	
	client.Client = &http.Client{
		Transport: &mockTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				fetchCallCount++
				
				// 1st request is the preflight FindCompanyInfoContext
				if fetchCallCount == 1 {
					assert.Equal(t, "GET", req.Method)
					assert.Contains(t, req.URL.Path, "/companyinfo/testRealm")
					
					respBody := `{
						"CompanyInfo": {
							"Id": "1",
							"SyncToken": "0",
							"CompanyName": "Sandbox Company_US_1"
						},
						"time": "2026-03-26T00:00:00.000-07:00"
					}`
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewBufferString(respBody)),
					}, nil
				}
				
				// 2nd request is the POST update
				assert.Equal(t, "POST", req.Method)
				assert.Contains(t, req.URL.Path, "/companyInfo")
				
				// Verify the payload contains the ID and sparse=true
				bodyBytes, _ := io.ReadAll(req.Body)
				req.Body.Close()
				
				var payload map[string]interface{}
				err := json.Unmarshal(bodyBytes, &payload)
				require.NoError(t, err)
				
				assert.Equal(t, "1", payload["Id"])
				assert.Equal(t, "0", payload["SyncToken"])
				assert.Equal(t, "Updated Company", payload["CompanyName"])
				assert.Equal(t, true, payload["sparse"])
				
				// Mock the response
				respBody := `{
					"CompanyInfo": {
						"Id": "1",
						"SyncToken": "1",
						"CompanyName": "Updated Company"
					},
					"time": "2026-03-26T00:01:00.000-07:00"
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(respBody)),
				}, nil
			},
		},
	}

	info := &CompanyInfo{
		CompanyName: "Updated Company",
	}

	updatedInfo, err := client.UpdateCompanyInfoContext(context.Background(), info)
	require.NoError(t, err)

	assert.Equal(t, 2, fetchCallCount)
	assert.Equal(t, "1", updatedInfo.Id)
	assert.Equal(t, "1", updatedInfo.SyncToken)
	assert.Equal(t, "Updated Company", updatedInfo.CompanyName)
}

package quickbooks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchItemRequest_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		request  BatchItemRequest
		expected string
	}{
		{
			name: "Vendor create operation",
			request: BatchItemRequest{
				BId:       "bid-1",
				Operation: "create",
				Entity:    "Vendor",
				Payload: map[string]string{
					"DisplayName": "Test Vendor",
				},
			},
			expected: `{"Vendor":{"DisplayName":"Test Vendor"},"bId":"bid-1","operation":"create"}`,
		},
		{
			name: "Bill update operation",
			request: BatchItemRequest{
				BId:       "bid-2",
				Operation: "update",
				Entity:    "Bill",
				Payload: map[string]interface{}{
					"Id":          "123",
					"TotalAmount": 100.50,
				},
			},
			expected: `{"Bill":{"Id":"123","TotalAmount":100.5},"bId":"bid-2","operation":"update"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.request)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))
		})
	}
}

func TestBatchBuilder(t *testing.T) {
	t.Run("creates sequential bIds", func(t *testing.T) {
		builder := NewBatchBuilder()
		builder.AddCreate("Vendor", map[string]string{"name": "v1"})
		builder.AddUpdate("Bill", map[string]string{"id": "b1"})
		builder.AddDelete("Invoice", map[string]string{"id": "i1"})

		items := builder.Build()
		require.Len(t, items, 3)
		assert.Equal(t, "bid-1", items[0].BId)
		assert.Equal(t, "bid-2", items[1].BId)
		assert.Equal(t, "bid-3", items[2].BId)
	})

	t.Run("tracks count correctly", func(t *testing.T) {
		builder := NewBatchBuilder()
		assert.Equal(t, 0, builder.Count())

		builder.AddCreate("Vendor", nil)
		assert.Equal(t, 1, builder.Count())

		builder.AddCreate("Vendor", nil)
		assert.Equal(t, 2, builder.Count())
	})

	t.Run("IsFull returns true at 30 items", func(t *testing.T) {
		builder := NewBatchBuilder()
		for i := 0; i < 29; i++ {
			builder.AddCreate("Vendor", nil)
		}
		assert.False(t, builder.IsFull())

		builder.AddCreate("Vendor", nil)
		assert.True(t, builder.IsFull())
	})

	t.Run("Reset clears builder", func(t *testing.T) {
		builder := NewBatchBuilder()
		builder.AddCreate("Vendor", nil)
		builder.AddCreate("Bill", nil)

		builder.Reset()
		assert.Equal(t, 0, builder.Count())
		assert.Len(t, builder.Build(), 0)

		// Should start bId counter from 1 again
		builder.AddCreate("Vendor", nil)
		items := builder.Build()
		assert.Equal(t, "bid-1", items[0].BId)
	})
}

func TestBatchItemResponse_HasError(t *testing.T) {
	t.Run("returns true when fault exists", func(t *testing.T) {
		response := &BatchItemResponse{
			BId: "bid-1",
			Fault: &Fault{
				Type: "ValidationFault",
				Error: []ErrorDetail{
					{Message: "Invalid field", Code: "6000"},
				},
			},
		}
		assert.True(t, response.HasError())
	})

	t.Run("returns false when no fault", func(t *testing.T) {
		response := &BatchItemResponse{
			BId:    "bid-1",
			Vendor: &Vendor{Id: "123"},
		}
		assert.False(t, response.HasError())
	})
}

func TestBatchItemResponse_GetError(t *testing.T) {
	t.Run("returns error message when fault exists", func(t *testing.T) {
		response := &BatchItemResponse{
			BId: "bid-1",
			Fault: &Fault{
				Error: []ErrorDetail{
					{Message: "Duplicate name exists", Code: "6240"},
				},
			},
		}
		assert.Equal(t, "Duplicate name exists", response.GetError())
	})

	t.Run("returns empty string when no fault", func(t *testing.T) {
		response := &BatchItemResponse{
			BId:    "bid-1",
			Vendor: &Vendor{Id: "123"},
		}
		assert.Equal(t, "", response.GetError())
	})
}

func TestBatchRequest_Structure(t *testing.T) {
	// Test that BatchRequest can be properly marshaled
	builder := NewBatchBuilder()
	builder.AddCreate("Vendor", map[string]string{
		"DisplayName": "Test Vendor",
	})
	builder.AddCreate("Bill", map[string]interface{}{
		"VendorRef": map[string]string{"value": "123"},
	})

	req := BatchRequest{
		BatchItemRequest: builder.Build(),
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(data), "BatchItemRequest")
	assert.Contains(t, string(data), "Vendor")
	assert.Contains(t, string(data), "Bill")
}

func TestBatchResponse_Unmarshal(t *testing.T) {
	// Test unmarshaling a sample QBO batch response
	responseJSON := `{
		"BatchItemResponse": [
			{
				"bId": "bid-1",
				"Vendor": {
					"Id": "123",
					"DisplayName": "Test Vendor"
				}
			},
			{
				"bId": "bid-2",
				"Fault": {
					"Error": [
						{
							"Message": "Duplicate Display Name Exists Error",
							"Detail": "The name supplied already exists",
							"code": "6240"
						}
					],
					"type": "ValidationFault"
				}
			}
		],
		"time": "2024-01-15T12:34:56.789-08:00"
	}`

	var response BatchResponse
	err := json.Unmarshal([]byte(responseJSON), &response)
	require.NoError(t, err)

	assert.Len(t, response.BatchItemResponse, 2)
	assert.Equal(t, "bid-1", response.BatchItemResponse[0].BId)
	assert.NotNil(t, response.BatchItemResponse[0].Vendor)
	assert.Equal(t, "123", response.BatchItemResponse[0].Vendor.Id)

	assert.Equal(t, "bid-2", response.BatchItemResponse[1].BId)
	assert.True(t, response.BatchItemResponse[1].HasError())
	assert.Contains(t, response.BatchItemResponse[1].GetError(), "Duplicate")
}

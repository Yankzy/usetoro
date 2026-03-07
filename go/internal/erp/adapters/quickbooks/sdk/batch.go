package quickbooks

import "encoding/json"

// BatchRequest represents a batch of QBO API operations
type BatchRequest struct {
	BatchItemRequest []BatchItemRequest `json:"BatchItemRequest"`
}

// BatchItemRequest represents a single operation within a batch
type BatchItemRequest struct {
	BId       string      `json:"bId"`       // Unique identifier for request matching
	Operation string      `json:"operation"` // "create", "update", "delete"
	Entity    string      `json:"-"`         // Internal: entity type (Bill, Vendor, etc.)
	Payload   interface{} `json:"-"`         // The actual entity payload
}

// MarshalJSON implements custom JSON marshaling for BatchItemRequest
// to dynamically set the JSON key based on Entity type
func (b BatchItemRequest) MarshalJSON() ([]byte, error) {
	// Create a map with the standard fields
	m := map[string]interface{}{
		"bId":       b.BId,
		"operation": b.Operation,
	}

	// Add the entity payload with the entity type as the key
	if b.Entity != "" && b.Payload != nil {
		m[b.Entity] = b.Payload
	}

	return json.Marshal(m)
}

// BatchResponse represents the API response
type BatchResponse struct {
	BatchItemResponse []BatchItemResponse `json:"BatchItemResponse"`
	Time              string              `json:"time"`
}

// BatchItemResponse represents the result of a single operation
type BatchItemResponse struct {
	BId      string    `json:"bId"`
	Fault    *Fault    `json:"Fault,omitempty"`
	Account  *Account  `json:"Account,omitempty"`
	Vendor   *Vendor   `json:"Vendor,omitempty"`
	Customer *Customer `json:"Customer,omitempty"`
	Invoice  *Invoice  `json:"Invoice,omitempty"`
	Bill     *Bill     `json:"Bill,omitempty"`
	Purchase *Purchase `json:"Purchase,omitempty"`
}

// Fault represents an error for a specific batch item
type Fault struct {
	Error []ErrorDetail `json:"Error"`
	Type  string        `json:"type"`
}

// ErrorDetail provides specifics about what went wrong
type ErrorDetail struct {
	Message string `json:"Message"`
	Detail  string `json:"Detail"`
	Code    string `json:"code"`
}

// HasError checks if this batch item response contains an error
func (b *BatchItemResponse) HasError() bool {
	return b.Fault != nil && len(b.Fault.Error) > 0
}

// GetError returns the first error message if present
func (b *BatchItemResponse) GetError() string {
	if b.HasError() {
		return b.Fault.Error[0].Message
	}
	return ""
}

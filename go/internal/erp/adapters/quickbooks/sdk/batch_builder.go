package quickbooks

import "fmt"

// BatchMaxSize is the maximum number of operations allowed per QBO batch request.
const BatchMaxSize = 30

// BatchBuilder helps construct batch requests with automatic bId generation
type BatchBuilder struct {
	items   []BatchItemRequest
	counter int
}

// NewBatchBuilder creates a new batch builder
func NewBatchBuilder() *BatchBuilder {
	return &BatchBuilder{
		items:   make([]BatchItemRequest, 0, 30),
		counter: 0,
	}
}

// AddCreate adds a create operation to the batch
func (b *BatchBuilder) AddCreate(entityType string, entity interface{}) *BatchBuilder {
	b.counter++
	b.items = append(b.items, BatchItemRequest{
		BId:       fmt.Sprintf("bid-%d", b.counter),
		Operation: "create",
		Entity:    entityType,
		Payload:   entity,
	})
	return b
}

// AddUpdate adds an update operation
func (b *BatchBuilder) AddUpdate(entityType string, entity interface{}) *BatchBuilder {
	b.counter++
	b.items = append(b.items, BatchItemRequest{
		BId:       fmt.Sprintf("bid-%d", b.counter),
		Operation: "update",
		Entity:    entityType,
		Payload:   entity,
	})
	return b
}

// AddCreateWithID adds a create operation with a specific bId for result mapping.
func (b *BatchBuilder) AddCreateWithID(entityType string, entity interface{}, bId string) *BatchBuilder {
	b.items = append(b.items, BatchItemRequest{
		BId:       bId,
		Operation: "create",
		Entity:    entityType,
		Payload:   entity,
	})
	return b
}

// AddDelete adds a delete operation
func (b *BatchBuilder) AddDelete(entityType string, entity interface{}) *BatchBuilder {
	b.counter++
	b.items = append(b.items, BatchItemRequest{
		BId:       fmt.Sprintf("bid-%d", b.counter),
		Operation: "delete",
		Entity:    entityType,
		Payload:   entity,
	})
	return b
}

// Build returns the final batch request slice
func (b *BatchBuilder) Build() []BatchItemRequest {
	return b.items
}

// Count returns the current number of operations in the batch
func (b *BatchBuilder) Count() int {
	return len(b.items)
}

// IsFull checks if the batch has reached the recommended maximum of 30 operations
func (b *BatchBuilder) IsFull() bool {
	return len(b.items) >= 30
}

// Reset clears the builder for reuse
func (b *BatchBuilder) Reset() {
	b.items = make([]BatchItemRequest, 0, 30)
	b.counter = 0
}

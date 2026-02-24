package store

import (
	"context"
	"errors"
	"sync"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

var ErrNotFound = errors.New("contract not found")

// Repository defines the interface for state persistence.
type Repository interface {
	SaveContract(ctx context.Context, c *core.Contract) error
	GetContract(ctx context.Context, id string) (*core.Contract, error)
	UpdateContractStatus(ctx context.Context, id string, status core.ContractStatus) error
}

type MemoryStore struct {
	contracts map[string]*core.Contract
	mu        sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		contracts: make(map[string]*core.Contract),
	}
}

func (m *MemoryStore) SaveContract(_ context.Context, c *core.Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contracts[c.ID] = c
	return nil
}

func (m *MemoryStore) GetContract(_ context.Context, id string) (*core.Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.contracts[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) UpdateContractStatus(_ context.Context, id string, status core.ContractStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contracts[id]
	if !ok {
		return ErrNotFound
	}
	c.Status = status
	return nil
}

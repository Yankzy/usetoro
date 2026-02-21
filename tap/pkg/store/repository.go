package store

import (
	"errors"
	"sync"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

var ErrNotFound = errors.New("contract not found")

// Repository defines the interface for state persistence.
type Repository interface {
	SaveContract(c *core.Contract) error
	GetContract(id string) (*core.Contract, error)
	UpdateStatus(id string, status core.ContractStatus) error
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

func (m *MemoryStore) SaveContract(c *core.Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deep copy could be done here for safety
	m.contracts[c.ID] = c
	return nil
}

func (m *MemoryStore) GetContract(id string) (*core.Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.contracts[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) UpdateStatus(id string, status core.ContractStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.contracts[id]
	if !ok {
		return ErrNotFound
	}

	c.Status = status
	// In a real DB, updated_at would be handled automatically or explicitly here
	return nil
}

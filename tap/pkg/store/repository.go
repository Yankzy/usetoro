package store

import (
	"errors"
	"sync"

	"github.com/Yankzy/usetoro/tap/pkg/tap"
)

var ErrNotFound = errors.New("contract not found")

// Repository defines the interface for state persistence.
type Repository interface {
	SaveContract(c *tap.Contract) error
	GetContract(id string) (*tap.Contract, error)
	UpdateStatus(id string, status tap.ContractStatus) error
}

type MemoryStore struct {
	contracts map[string]*tap.Contract
	mu        sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		contracts: make(map[string]*tap.Contract),
	}
}

func (m *MemoryStore) SaveContract(c *tap.Contract) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deep copy could be done here for safety
	m.contracts[c.ID] = c
	return nil
}

func (m *MemoryStore) GetContract(id string) (*tap.Contract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.contracts[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) UpdateStatus(id string, status tap.ContractStatus) error {
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

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
	GetIdentity(ctx context.Context, did string) (*core.Identity, error)
	SaveIdentity(ctx context.Context, identity *core.Identity) error
}

type MemoryStore struct {
	contracts  map[string]*core.Contract
	identities map[string]*core.Identity
	mu         sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		contracts:  make(map[string]*core.Contract),
		identities: make(map[string]*core.Identity),
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

func (m *MemoryStore) GetIdentity(_ context.Context, did string) (*core.Identity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	identity, ok := m.identities[did]
	if !ok {
		return nil, ErrNotFound
	}
	return identity, nil
}

func (m *MemoryStore) SaveIdentity(_ context.Context, identity *core.Identity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identities[identity.ID] = identity
	return nil
}

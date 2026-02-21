package memory

import (
	"github.com/Yankzy/usetoro/tap/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Public wrapper for internal memory manager
// This allows the main 'go' module to use memory functionality
// while keeping the implementation internal to 'tap'

type Manager = memory.Manager

func NewManager(db *pgxpool.Pool) *Manager {
	return memory.NewManager(db)
}

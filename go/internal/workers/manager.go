package workers

import (
	"context"
	"log/slog"
)

// Worker defines the interface for all background workers
type Worker interface {
	Start(ctx context.Context) error
}

// Manager orchestrates the lifecycle of multiple background workers
type Manager struct {
	logger  *slog.Logger
	workers []Worker
}

// NewManager creates a new worker manager
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		logger: logger,
	}
}

// Register adds a worker to the manager
func (m *Manager) Register(w Worker) {
	m.workers = append(m.workers, w)
}

// StartAll starts all registered workers concurrently and blocks until ctx finishes or an error occurs.
func (m *Manager) StartAll(ctx context.Context) error {
	m.logger.Info("Starting all background workers", "count", len(m.workers))

	if len(m.workers) == 0 {
		<-ctx.Done()
		return nil
	}

	errCh := make(chan error, len(m.workers))

	for _, w := range m.workers {
		worker := w // Capture for goroutine
		go func() {
			if err := worker.Start(ctx); err != nil {
				errCh <- err
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

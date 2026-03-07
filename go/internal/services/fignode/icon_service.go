package fignode

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
)

// IconService maintains an in-memory map of industry icons synced from the database.
type IconService struct {
	db     *database.Queries
	logger *slog.Logger
	mu     sync.RWMutex
	icons  map[string]string // industry name -> icon emoji
}

func NewIconService(db *database.Queries, logger *slog.Logger) *IconService {
	s := &IconService{
		db:     db,
		logger: logger,
		icons:  make(map[string]string),
	}
	s.syncIcons()       // Initial load
	s.startBackground() // Keep them fresh
	return s
}

// GetIcon safely gets the icon for an industry from the memory map,
// falling back to a default building icon if not found.
func (s *IconService) GetIcon(industry string, defaultIcon string) string {
	s.mu.RLock()
	icon, ok := s.icons[industry]
	s.mu.RUnlock()

	if ok {
		return icon
	}
	if defaultIcon != "" {
		return defaultIcon
	}
	return "🏢" // ultimate fallback
}

func (s *IconService) startBackground() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.syncIcons()
		}
	}()
}

func (s *IconService) syncIcons() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.GetAllIndustries(ctx)
	if err != nil {
		s.logger.Error("failed to sync industry icons", "error", err)
		return
	}

	newIcons := make(map[string]string, len(rows))
	for _, row := range rows {
		newIcons[row.Name] = row.IconEmoji
	}

	s.mu.Lock()
	s.icons = newIcons
	s.mu.Unlock()

	s.logger.Debug("industry icons synced successfully", "count", len(newIcons))
}

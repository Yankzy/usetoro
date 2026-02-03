package wshandler

import (
	"context"
	"log/slog"
)

// Service provides an interface for external packages to interact with the WebSocket hub
type Service struct {
	hub    *Hub
	logger *slog.Logger
}

// NewService creates a new WebSocket service
func NewService(hub *Hub, logger *slog.Logger) *Service {
	return &Service{
		hub:    hub,
		logger: logger,
	}
}

// BroadcastAuthURL broadcasts an authentication URL to all connected clients
func (s *Service) BroadcastAuthURL(ctx context.Context, url string) error {
	msg, err := NewAuthURLMessage(url)
	if err != nil {
		s.logger.Error("Failed to create auth URL message", "error", err)
		return err
	}

	s.hub.Broadcast(msg)
	s.logger.Info("Broadcast auth URL", "url", url, "clients", s.hub.ClientCount())
	return nil
}

// BroadcastQBOCredentials broadcasts QBO credentials to all connected clients
func (s *Service) BroadcastQBOCredentials(ctx context.Context, clientID, clientSecret, realmID string) error {
	msg, err := NewQBOCredentialsMessage(clientID, clientSecret, realmID)
	if err != nil {
		s.logger.Error("Failed to create QBO credentials message", "error", err)
		return err
	}

	s.hub.Broadcast(msg)
	s.logger.Info("Broadcast QBO credentials", "realm_id", realmID, "clients", s.hub.ClientCount())
	return nil
}

// BroadcastQBOConnected broadcasts a successful QBO connection notification
func (s *Service) BroadcastQBOConnected(ctx context.Context, realmID string) error {
	msg, err := NewQBOConnectedMessage(realmID)
	if err != nil {
		s.logger.Error("Failed to create QBO connected message", "error", err)
		return err
	}

	s.hub.Broadcast(msg)
	s.logger.Info("Broadcast QBO connected", "realm_id", realmID, "clients", s.hub.ClientCount())
	return nil
}

// BroadcastError broadcasts an error message to all connected clients
func (s *Service) BroadcastError(ctx context.Context, errorMsg string) error {
	msg, err := NewErrorMessage(errorMsg)
	if err != nil {
		s.logger.Error("Failed to create error message", "error", err)
		return err
	}

	s.hub.Broadcast(msg)
	s.logger.Warn("Broadcast error", "error", errorMsg, "clients", s.hub.ClientCount())
	return nil
}

// GetConnectedClients returns the number of currently connected clients
func (s *Service) GetConnectedClients() int {
	return s.hub.ClientCount()
}

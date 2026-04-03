package wshandler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"

	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/gorilla/websocket"
)

// MessageHandler handles incoming WebSocket messages
type MessageHandler struct {
	logger *slog.Logger
	config *Config
}

// Config holds configuration for QBO credentials
type Config struct {
	QBOClientID     string
	QBOClientSecret string
	QBORedirectURIs []string
	QBOIsProduction bool
}

// NewMessageHandler creates a new message handler
func NewMessageHandler(logger *slog.Logger, config *Config) *MessageHandler {
	return &MessageHandler{
		logger: logger,
		config: config,
	}
}

// HandleMessage processes incoming messages from clients and returns a response
func (h *MessageHandler) HandleMessage(ctx context.Context, data []byte) ([]byte, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		h.logger.Error("Failed to unmarshal message", "error", err)
		return NewErrorMessage("Invalid message format")
	}

	h.logger.Info("Processing client request", "type", msg.Type)

	switch msg.Type {
	case MessageTypeRequestQBOCredentials:
		return h.handleRequestQBOCredentials(ctx, msg)

	case MessageTypeRequestAuthURL:
		return h.handleRequestAuthURL(ctx, msg)

	case MessageTypeSubscribeCards, MessageType("request_cards"), MessageTypeSwipeResult:
		// These are intercepted by the WebSocket readPump core safely,
		// or they just don't have HTTP endpoints built for them currently.
		return nil, nil

	default:
		h.logger.Warn("Unknown message type", "type", msg.Type)
		return NewErrorMessage("Unknown message type")
	}
}

// handleRequestQBOCredentials responds with QBO credentials
func (h *MessageHandler) handleRequestQBOCredentials(ctx context.Context, msg Message) ([]byte, error) {
	h.logger.Info("Client requested QBO credentials")

	if h.config == nil {
		h.logger.Error("QBO config not initialized")
		return NewErrorMessage("QBO credentials not configured")
	}

	// Return credentials to the client
	return NewQBOCredentialsMessage(
		h.config.QBOClientID,
		h.config.QBOClientSecret,
		"", // realmID is typically obtained after auth, leave empty for now
	)
}

// handleRequestAuthURL generates and returns a QBO auth URL
func (h *MessageHandler) handleRequestAuthURL(ctx context.Context, msg Message) ([]byte, error) {
	h.logger.Info("Client requested auth URL")

	if h.config == nil {
		h.logger.Error("QBO config not initialized")
		return NewErrorMessage("QBO configuration not available")
	}

	// Extract state parameter if provided
	state := "security_token" // default
	if stateVal, ok := msg.Data["state"].(string); ok && stateVal != "" {
		state = stateVal
	}

	// Build QBO OAuth URL
	baseURL := quickbooks.DefaultAuthProductionEndpoint
	if !h.config.QBOIsProduction {
		baseURL = quickbooks.DefaultAuthSandboxEndpoint
	}

	// Determine valid RedirectURI based on Host from context
	host, _ := ctx.Value("host").(string)

	chosenURI := ""
	if host != "" {
		for _, uri := range h.config.QBORedirectURIs {
			u, err := url.Parse(uri)
			if err == nil && u.Host == host {
				chosenURI = uri
				break
			}
		}
	}

	// Fallback
	if chosenURI == "" && len(h.config.QBORedirectURIs) > 0 {
		chosenURI = h.config.QBORedirectURIs[0]
	}

	// Construct full auth URL using SDK helper
	authURL, err := quickbooks.GetAuthURL(
		h.config.QBOClientID,
		"com.intuit.quickbooks.accounting",
		state,
		chosenURI,
		baseURL,
	)
	if err != nil {
		h.logger.Error("Failed to generate QBO auth URL", "error", err)
		return NewErrorMessage("Failed to generate authorization URL")
	}

	h.logger.Info("Generated QBO auth URL", "url", authURL)
	return NewAuthURLMessage(authURL)
}

// SendResponse sends a response message back to the client
func SendResponse(conn *websocket.Conn, response []byte) error {
	return conn.WriteMessage(websocket.TextMessage, response)
}

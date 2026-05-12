package wshandler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go"
)

// MessageHandler handles incoming WebSocket messages
type MessageHandler struct {
	logger *slog.Logger
	config *Config
	queue  *queue.Client
}

// Config holds configuration for QBO credentials
type Config struct {
	QBOClientID     string
	QBOClientSecret string
	QBORedirectURIs []string
	QBOIsProduction bool
}

// NewMessageHandler creates a new message handler
func NewMessageHandler(logger *slog.Logger, config *Config, queue *queue.Client) *MessageHandler {
	return &MessageHandler{
		logger: logger,
		config: config,
		queue:  queue,
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

	case MessageTypeRequestRunSQL:
		return h.handleRequestRunSQL(ctx, msg)

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

	// Determine dynamic RedirectURI from context
	host, _ := ctx.Value("host").(string)
	scheme, _ := ctx.Value("scheme").(string)
	pathPrefix, _ := ctx.Value("path_prefix").(string)

	if scheme == "" {
		scheme = "https"
	}

	chosenURI := fmt.Sprintf("%s://%s%s/auth/qbo/callback", scheme, host, pathPrefix)
	h.logger.Debug("Dynamically determined RedirectURI for WebSocket", "uri", chosenURI)

	// Optional: Whitelist check
	if h.config != nil && len(h.config.QBORedirectURIs) > 0 {
		matched := false
		for _, configured := range h.config.QBORedirectURIs {
			if configured == chosenURI {
				matched = true
				break
			}
		}
		if !matched {
			h.logger.Warn("Dynamic WebSocket RedirectURI not in configured whitelist", "uri", chosenURI)
		}
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

// handleRequestRunSQL executes a generic SQL query via the SQL worker
func (h *MessageHandler) handleRequestRunSQL(ctx context.Context, msg Message) ([]byte, error) {
	h.logger.Info("Client requested SQL execution")

	if h.queue == nil {
		h.logger.Error("Queue client not initialized")
		return NewErrorMessage("Internal server error: Queue unavailable")
	}

	queryID, ok := msg.Data["query_id"].(string)
	if !ok || queryID == "" {
		return NewErrorMessage("Missing or invalid 'query_id' field")
	}

	args, _ := msg.Data["args"].([]any)

	// Package the SQL request using the struct that matches SQLWorkerPayload in workers/sql_worker.go
	returnSubj := nats.NewInbox()
	payload := struct {
		QueryID       string `json:"query_id"`
		Args          []any  `json:"args,omitempty"`
		ReturnSubject string `json:"return_subject"`
	}{
		QueryID:       queryID,
		Args:          args,
		ReturnSubject: returnSubj,
	}
	// Execute via NATS Request (Standard NATS Req/Rep)
	// The SQL worker will receive this and publish to the Reply subject.
	// We use the subject derived from the activity type "workers.sql.execute"
	subject := "worker.inbox.sql.execute"
	if derived, err := core.BuildWorkerInboxFromActivity("workers.sql.execute"); err == nil {
		subject = derived
	}

	h.logger.Debug("Publishing SQL request to NATS", "subject", subject, "query_id", queryID)

	// Wrap in a TAP Envelope for standardized worker routing and proof tracking.
	// We use the REQUEST performative for direct worker execution.
	convID := uuid.New().String()
	env, err := core.NewEnvelope(uuid.New().String(), "did:toro:ws-sidecar", "did:toro:sql-worker", convID, core.REQUEST, payload)
	if err != nil {
		h.logger.Error("Failed to create SQL request envelope", "error", err)
		return NewErrorMessage("Internal protocol error")
	}

	envBytes, err := json.Marshal(env)
	if err != nil {
		h.logger.Error("Failed to marshal SQL request envelope", "error", err)
		return NewErrorMessage("Internal protocol error")
	}

	// We cannot use h.queue.Request() here because the target subject is captured by JetStream.
	// If we use Request(), JetStream will intercept the request and send a PubAck to the _INBOX reply subject,
	// which will be interpreted as an empty response by our unmarshaler.
	// Instead, we subscribe to the explicit ReturnSubject and publish the envelope.
	sub, err := h.queue.Conn().SubscribeSync(returnSubj)
	if err != nil {
		h.logger.Error("Failed to subscribe to return subject", "error", err)
		return NewErrorMessage("Internal protocol error")
	}
	defer sub.Unsubscribe()

	if err := h.queue.Publish(subject, envBytes); err != nil {
		h.logger.Error("SQL NATS publish failed", "error", err, "subject", subject)
		return NewErrorMessage("SQL execution publish failed")
	}

	replyMsg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		h.logger.Error("SQL worker response timed out", "error", err, "subject", subject)
		return NewErrorMessage("SQL execution timed out")
	}
	respBytes := replyMsg.Data
	h.logger.Info("Received SQL worker response", "raw", string(respBytes))

	// The response from SQL worker is a SQLWorkerResult
	var result struct {
		Success bool            `json:"success"`
		Error   string          `json:"error,omitempty"`
		Data    json.RawMessage `json:"data,omitempty"`
	}

	if err := json.Unmarshal(respBytes, &result); err != nil {
		h.logger.Error("Failed to unmarshal SQL worker result", "error", err)
		return NewErrorMessage("Failed to parse worker response")
	}

	return NewSQLResultMessage(queryID, result.Success, result.Data, result.Error)
}

// SendResponse sends a response message back to the client
func SendResponse(conn *websocket.Conn, response []byte) error {
	return conn.WriteMessage(websocket.TextMessage, response)
}

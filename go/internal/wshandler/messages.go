package wshandler

import (
	"encoding/json"
	"fmt"
)

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// MessageTypeAuthURL is sent when QBO auth URL is generated
	MessageTypeAuthURL MessageType = "auth_url"

	// MessageTypeQBOCredentials is sent when QBO credentials need to be communicated
	MessageTypeQBOCredentials MessageType = "qbo_credentials"

	// MessageTypeQBOConnected is sent when QBO is successfully connected
	MessageTypeQBOConnected MessageType = "qbo_connected"

	// MessageTypeWorkflowStatus is sent for real-time visualizer updates
	MessageTypeWorkflowStatus MessageType = "workflow_status"

	// MessageTypeError is sent when an error occurs
	MessageTypeError MessageType = "error"

	// MessageTypeStripeFCConnected is sent when a Stripe Financial Connections flow completes
	MessageTypeStripeFCConnected MessageType = "stripe_fc_connected"

	// REQUEST TYPES (from client to server)

	// MessageTypeRequestQBOCredentials is sent by client to request QBO credentials
	MessageTypeRequestQBOCredentials MessageType = "request_qbo_credentials"

	// MessageTypeRequestAuthURL is sent by client to request auth URL generation
	MessageTypeRequestAuthURL MessageType = "request_auth_url"

	// MessageTypeSubscribeCards is sent by client to opt-in to retrieving JetStream cards
	MessageTypeSubscribeCards MessageType = "subscribe_cards"

	// MessageTypeSwipeResult is sent by client upon categorizing a transaction
	MessageTypeSwipeResult MessageType = "swipe_result"

	// MessageTypeRequestRunSQL is sent by client to execute a generic SQL query
	MessageTypeRequestRunSQL MessageType = "request_run_sql"

	// MessageTypeSQLResult is sent when a generic SQL query result is available
	MessageTypeSQLResult MessageType = "sql_result"
)

// Message represents a WebSocket message structure
type Message struct {
	Type MessageType            `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
}

// NewAuthURLMessage creates a message containing an auth URL
func NewAuthURLMessage(url string) ([]byte, error) {
	msg := Message{
		Type: MessageTypeAuthURL,
		Data: map[string]interface{}{
			"url": url,
		},
	}
	return json.Marshal(msg)
}

// NewQBOCredentialsMessage creates a message containing QBO credentials
func NewQBOCredentialsMessage(clientID, clientSecret, realmID string) ([]byte, error) {
	msg := Message{
		Type: MessageTypeQBOCredentials,
		Data: map[string]interface{}{
			"client_id":     clientID,
			"client_secret": clientSecret,
			"realm_id":      realmID,
		},
	}
	return json.Marshal(msg)
}

// NewQBOConnectedMessage creates a message indicating successful QBO connection
func NewQBOConnectedMessage(realmID string) ([]byte, error) {
	msg := Message{
		Type: MessageTypeQBOConnected,
		Data: map[string]interface{}{
			"realm_id": realmID,
			"status":   "connected",
		},
	}
	return json.Marshal(msg)
}

// NewWorkflowStatusMessage creates a message for real-time workflow visualization
func NewWorkflowStatusMessage(msgType MessageType, data map[string]interface{}) ([]byte, error) {
	msg := Message{
		Type: msgType,
		Data: data,
	}
	return json.Marshal(msg)
}

// NewSQLResultMessage creates a message containing the results of a SQL query.
// It uses a rich message type in the format "sql_result.{query_id}"
func NewSQLResultMessage(queryID string, success bool, data json.RawMessage, errorMsg string) ([]byte, error) {
	msgType := MessageTypeSQLResult
	if queryID != "" {
		msgType = MessageType(fmt.Sprintf("%s.%s", MessageTypeSQLResult, queryID))
	}

	msg := Message{
		Type: msgType,
		Data: map[string]interface{}{
			"success": success,
			"data":    data,
			"error":   errorMsg,
		},
	}
	return json.Marshal(msg)
}

// NewErrorMessage creates an error message
func NewErrorMessage(errorMsg string) ([]byte, error) {
	msg := Message{
		Type: MessageTypeError,
		Data: map[string]interface{}{
			"error": errorMsg,
		},
	}
	return json.Marshal(msg)
}

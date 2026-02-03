package wshandler

import "encoding/json"

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// MessageTypeAuthURL is sent when QBO auth URL is generated
	MessageTypeAuthURL MessageType = "auth_url"

	// MessageTypeQBOCredentials is sent when QBO credentials need to be communicated
	MessageTypeQBOCredentials MessageType = "qbo_credentials"

	// MessageTypeQBOConnected is sent when QBO is successfully connected
	MessageTypeQBOConnected MessageType = "qbo_connected"

	// MessageTypeError is sent when an error occurs
	MessageTypeError MessageType = "error"

	// REQUEST TYPES (from client to server)

	// MessageTypeRequestQBOCredentials is sent by client to request QBO credentials
	MessageTypeRequestQBOCredentials MessageType = "request_qbo_credentials"

	// MessageTypeRequestAuthURL is sent by client to request auth URL generation
	MessageTypeRequestAuthURL MessageType = "request_auth_url"
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

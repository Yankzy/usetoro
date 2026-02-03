# WebSocket Server - QBO Request/Response Protocol

## Client Requests

Your Wails desktop app can now send requests to the WebSocket server to get QBO credentials.

### Request QBO Credentials

**Client sends:**
```json
{
  "type": "request_qbo_credentials"
}
```

**Server responds:**
```json
{
  "type": "qbo_credentials",
  "data": {
    "client_id": "your-qbo-client-id",
    "client_secret": "your-qbo-client-secret",
    "realm_id": ""
  }
}
```

### Request Auth URL

**Client sends:**
```json
{
  "type": "request_auth_url",
  "data": {
    "state": "optional-state-token" 
  }
}
```

**Server responds:**
```json
{
  "type": "auth_url",
  "data": {
    "url": "https://appcenter.intuit.com/connect/oauth2?..."
  }
}
```

## Environment Variables

Add these to your `.env` file:

```env
# QuickBooks Online Configuration
QBO_CLIENT_ID=your_qbo_client_id
QBO_CLIENT_SECRET=your_qbo_client_secret
QBO_REDIRECT_URI=http://localhost/api/auth/qbo/callback
QBO_IS_PRODUCTION=false  # set to true for production
```

## Wails Integration Example

```go
type QBORequest struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data,omitempty"`
}

type QBOResponse struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data"`
}

// Request QBO credentials from server
func (a *App) RequestQBOCredentials() error {
    request := QBORequest{
        Type: "request_qbo_credentials",
    }
    
    data, _ := json.Marshal(request)
    
    a.mu.Lock()
    defer a.mu.Unlock()
    
    if a.wsConn != nil {
        return a.wsConn.WriteMessage(websocket.TextMessage, data)
    }
    return fmt.Errorf("not connected")
}

// In your message receiver loop
var response QBOResponse
if err := json.Unmarshal(message, &response); err == nil {
    switch response.Type {
    case "qbo_credentials":
        clientID := response.Data["client_id"].(string)
        clientSecret := response.Data["client_secret"].(string)
        // Use credentials
        
    case "auth_url":
        url := response.Data["url"].(string)
        runtime.BrowserOpenURL(a.ctx, url)
    }
}
```

## Error Handling

If an error occurs, the server will respond with:

```json
{
  "type": "error",
  "data": {
    "error": "Error message here"
  }
}
```

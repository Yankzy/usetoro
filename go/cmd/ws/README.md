# WebSocket Server for QBO Authentication

This WebSocket server provides real-time communication between the backend and Wails desktop app for QuickBooks Online (QBO) authentication.

## Architecture

The WebSocket server is located in `cmd/ws/` and uses the following components:

### Components

1. **Hub** (`internal/wshandler/hub.go`)
   - Manages all active WebSocket connections
   - Handles client registration/unregistration
   - Broadcasts messages to all connected clients

2. **Client** (`internal/wshandler/client.go`)
   - Represents individual WebSocket connections
   - Implements read/write pumps for bi-directional communication
   - Handles ping/pong for connection health checks

3. **Handler** (`internal/wshandler/handler.go`)
   - HTTP handler for WebSocket upgrade requests
   - Entry point for new connections

4. **Messages** (`internal/wshandler/messages.go`)
   - Defines message types for QBO authentication
   - Provides message constructors for type safety

5. **Service** (`internal/wshandler/service.go`)
   - Public API for broadcasting messages
   - Used by other services to send QBO-related updates

## Message Types

### `auth_url`
Sent when a QBO authentication URL is generated:
```json
{
  "type": "auth_url",
  "data": {
    "url": "https://..."
  }
}
```

### `qbo_credentials`
Sent when QBO credentials need to be communicated:
```json
{
  "type": "qbo_credentials",
  "data": {
    "client_id": "...",
    "client_secret": "...",
    "realm_id": "..."
  }
}
```

### `qbo_connected`
Sent when QBO is successfully connected:
```json
{
  "type": "qbo_connected",
  "data": {
    "realm_id": "...",
    "status": "connected"
  }
}
```

### `error`
Sent when an error occurs:
```json
{
  "type": "error",
  "data": {
    "error": "error message"
  }
}
```

## Running the Server

### Docker Compose (Recommended)

```bash
cd container
docker-compose up ws
```

**WebSocket Access:**
- **Through nginx (production)**: `ws://localhost/ws`
- **Direct access (development)**: `ws://localhost:8081/ws`

### Standalone
```bash
cd go
WS_ADDR=:8080 go run cmd/ws/main.go
```

### Docker
```bash
docker build -f container/ws/Dockerfile -t usetoro-ws .
docker run -p 8080:8080 usetoro-ws
```

### Environment Variables
- `WS_ADDR` - WebSocket server address (default: `:8080`)

## Endpoints

- `GET /ws` - WebSocket upgrade endpoint
- `GET /health` - Health check endpoint

## Integration Example

To integrate the WebSocket service into your existing handlers:

```go
// In your main.go or initialization code
hub := wshandler.NewHub(logger)
go hub.Run()

wsService := wshandler.NewService(hub, logger)

// In your QBO handler
func (h *Handler) HandleQBOCallback(w http.ResponseWriter, r *http.Request) {
    // ... existing code ...
    
    // After successfully saving tokens, broadcast to connected clients
    if err := wsService.BroadcastQBOConnected(ctx, realmID); err != nil {
        logger.Error("Failed to broadcast QBO connection", "error", err)
    }
}
```

## Client Connection (Wails App)

Your Wails desktop app should connect to `ws://localhost:8080/ws` and handle the incoming messages based on their type.

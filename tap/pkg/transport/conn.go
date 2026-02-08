package transport

import (
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect establishes a connection to the NATS server.
// It sets up automatic reconnection logic.
func Connect(url string) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name("TAP-Node"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(50),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("🔌 NATS Disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("⚡ NATS Reconnected to %s", nc.ConnectedUrl())
		}),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}

	return nc, nil
}

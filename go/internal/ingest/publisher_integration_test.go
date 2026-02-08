package ingest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
	"github.com/stripe/stripe-go/v76"
	"github.com/testcontainers/testcontainers-go"
	nats_module "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPublisher_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Start NATS Container
	natsContainer, err := nats_module.Run(ctx, "nats:2.9-alpine", testcontainers.WithWaitStrategy(wait.ForLog(".*Server is ready.*").AsRegexp()))
	if err != nil {
		t.Fatalf("failed to start nats container: %s", err)
	}
	defer func() {
		if err := natsContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	}()

	uri, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get nats connection string: %s", err)
	}

	// 2. Connect to NATS via Queue Client
	q, err := queue.NewClient(uri)
	if err != nil {
		t.Fatalf("failed to create queue client: %s", err)
	}
	defer q.Close()

	js := q.JetStream()

	// 3. Setup Stream
	streamName := "STRIPE_INGEST"
	subject := "raw.ingest.stripe"
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject},
	})
	if err != nil {
		t.Fatalf("failed to add stream: %s", err)
	}

	// 4. Initialize Publisher
	publisher := ingest.NewPublisher(q)

	// 5. Test Publish
	connID := "conn_123"
	toroEventID := "toro_evt_456"
	stripeEvent := stripe.Event{ID: "evt_789", Type: "payment_intent.succeeded"}
	body := []byte(`{"id": "evt_789", "type": "payment_intent.succeeded"}`)

	reqCtx := context.WithValue(ctx, "request_id", "req_abc")
	err = publisher.PublishStripeEvent(reqCtx, connID, toroEventID, stripeEvent.ID, string(stripeEvent.Type), body)
	if err != nil {
		t.Fatalf("failed to publish stripe event: %s", err)
	}

	// 6. Verify with a synchronous pull (subscribe and check)
	sub, err := js.SubscribeSync(subject)
	if err != nil {
		t.Fatalf("failed to subscribe: %s", err)
	}

	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive message: %s", err)
	}

	// 7. Assertions
	if string(msg.Data) != string(body) {
		t.Errorf("expected body %s, got %s", string(body), string(msg.Data))
	}

	if msg.Header.Get("Toro-Conn-ID") != connID {
		t.Errorf("expected Toro-Conn-ID %s, got %s", connID, msg.Header.Get("Toro-Conn-ID"))
	}

	if msg.Header.Get("Toro-Event-ID") != toroEventID {
		t.Errorf("expected Toro-Event-ID %s, got %s", toroEventID, msg.Header.Get("Toro-Event-ID"))
	}

	if msg.Header.Get("Provider-Event-ID") != stripeEvent.ID {
		t.Errorf("expected Provider-Event-ID %s, got %s", stripeEvent.ID, msg.Header.Get("Provider-Event-ID"))
	}

	if msg.Header.Get("Request-ID") != "req_abc" {
		t.Errorf("expected Request-ID req_abc, got %s", msg.Header.Get("Request-ID"))
	}

	fmt.Println("Integration test passed successfully!")
}

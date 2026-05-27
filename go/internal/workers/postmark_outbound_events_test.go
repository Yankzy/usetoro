package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go"
)

// mockDBTX implements database.DBTX for testing queries
type mockDBTX struct {
	lastQuery string
	lastArgs  []interface{}
	execErr   error
}

func (m *mockDBTX) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	m.lastQuery = query
	m.lastArgs = args
	return pgconn.NewCommandTag("UPDATE 1"), m.execErr
}

func (m *mockDBTX) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockDBTX) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	return nil
}

func TestPostmarkOutboundEventsWorker_Handle_Malformed(t *testing.T) {
	w := &PostmarkOutboundEventsWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "worker.inbox.email.postmark_outbound_events",
		Data:    []byte(`{ "invalid": "json"`),
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected nil error for malformed payload (poison pill), got %v", err)
	}
}

func TestPostmarkOutboundEventsWorker_Handle_MissingMessageID(t *testing.T) {
	w := &PostmarkOutboundEventsWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "worker.inbox.email.postmark_outbound_events",
		Data:    []byte(`{"RecordType":"Delivery"}`), // missing MessageID
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected nil error for payload with missing MessageID, got %v", err)
	}
}

func TestPostmarkOutboundEventsWorker_Handle_Success(t *testing.T) {
	cases := []struct {
		name       string
		recordType string
		jsonStr    string
		argIndex   int // index in lastArgs matching the JSONB field (delivered=1, bounced=2, opened=3, clicked=4, complained=5)
	}{
		{
			name:       "Delivery",
			recordType: "Delivery",
			jsonStr:    `{"RecordType":"Delivery","MessageID":"msg-123","DeliveredAt":"2026-05-26T20:06:35Z"}`,
			argIndex:   1,
		},
		{
			name:       "Bounce",
			recordType: "Bounce",
			jsonStr:    `{"RecordType":"Bounce","MessageID":"msg-123","BouncedAt":"2026-05-26T20:06:35Z"}`,
			argIndex:   2,
		},
		{
			name:       "SpamComplaint",
			recordType: "SpamComplaint",
			jsonStr:    `{"RecordType":"SpamComplaint","MessageID":"msg-123","BouncedAt":"2026-05-26T20:06:35Z"}`,
			argIndex:   5,
		},
		{
			name:       "Open",
			recordType: "Open",
			jsonStr:    `{"RecordType":"Open","MessageID":"msg-123","ReceivedAt":"2026-05-26T20:06:35Z"}`,
			argIndex:   3,
		},
		{
			name:       "Click",
			recordType: "Click",
			jsonStr:    `{"RecordType":"Click","MessageID":"msg-123","ReceivedAt":"2026-05-26T20:06:35Z"}`,
			argIndex:   4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := &mockDBTX{}
			w := &PostmarkOutboundEventsWorker{
				logger: slog.Default(),
				db:     database.New(mockDB),
			}

			msg := &nats.Msg{
				Subject: "worker.inbox.email.postmark_outbound_events",
				Data:    []byte(tc.jsonStr),
			}

			err := w.Handle(context.Background(), msg)
			if err != nil {
				t.Fatalf("unexpected handle error: %v", err)
			}

			if mockDB.lastQuery == "" {
				t.Fatal("expected Exec to be called on database, but it was not")
			}

			// Args should be: [ExternalID, Delivered, Bounced, Opened, Clicked, Complained]
			// tc.argIndex defines the target field index
			if len(mockDB.lastArgs) != 6 {
				t.Fatalf("expected 6 arguments to Exec, got %d", len(mockDB.lastArgs))
			}

			if mockDB.lastArgs[0] != "msg-123" {
				t.Errorf("expected ExternalID arg 'msg-123', got '%v'", mockDB.lastArgs[0])
			}

			targetVal := mockDB.lastArgs[tc.argIndex]
			if targetVal == nil {
				t.Errorf("expected argument at index %d to be set, but it was nil", tc.argIndex)
			} else {
				targetBytes, ok := targetVal.([]byte)
				if !ok {
					t.Fatalf("expected []byte for JSONB field, got %T", targetVal)
				}
				var parsed map[string]interface{}
				if err := json.Unmarshal(targetBytes, &parsed); err != nil {
					t.Fatalf("failed to unmarshal saved JSONB content: %v", err)
				}
				if parsed["RecordType"] != tc.recordType {
					t.Errorf("expected saved RecordType '%s', got '%v'", tc.recordType, parsed["RecordType"])
				}
			}

			// All other status fields should be nil
			for i := 1; i <= 5; i++ {
				if i != tc.argIndex {
					val := mockDB.lastArgs[i]
					if val != nil {
						if bytesVal, ok := val.([]byte); !ok || bytesVal != nil {
							t.Errorf("expected argument at index %d to be nil, but got '%v'", i, val)
						}
					}
				}
			}
		})
	}
}

func TestPostmarkOutboundEventsWorker_Handle_DatabaseError(t *testing.T) {
	mockDB := &mockDBTX{
		execErr: errors.New("db disconnect"),
	}
	w := &PostmarkOutboundEventsWorker{
		logger: slog.Default(),
		db:     database.New(mockDB),
	}

	msg := &nats.Msg{
		Subject: "worker.inbox.email.postmark_outbound_events",
		Data:    []byte(`{"RecordType":"Delivery","MessageID":"msg-123"}`),
	}

	err := w.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected database error to propagate, but got nil")
	}
}

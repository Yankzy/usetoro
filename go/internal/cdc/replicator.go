package cdc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	slotName    = "toro_nats_slot"
	publication = "toro_ledger_pub"
	plugin      = "pgoutput"
)

// RunReplicator is the main entry point to tail the WAL and publish to NATS
func RunReplicator(ctx context.Context, dbURL string, natsURL string) error {
	// 1. Connect to NATS JetStream
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to nats: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to init jetstream: %w", err)
	}

	publisher := NewPublisher(js)

	// 2. Establish Replication Connection to Postgres
	// Parse config and append replication option
	cfg, err := pgconn.ParseConfig(dbURL)
	if err != nil {
		return fmt.Errorf("failed to parse db url: %w", err)
	}
	// Important: Instruct Postgres this is a replication connection
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = make(map[string]string)
	}
	cfg.RuntimeParams["replication"] = "database"

	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres for replication: %w", err)
	}
	defer conn.Close(context.Background())

	// 3. Identify System
	sysident, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		return fmt.Errorf("IdentifySystem failed: %w", err)
	}
	log.Printf("SystemID:%s Timeline:%d XLogPos:%s DBName:%s", sysident.SystemID, sysident.Timeline, sysident.XLogPos, sysident.DBName)

	// 4. Create or Ensure Replication Slot exists
	_, err = pglogrepl.CreateReplicationSlot(ctx, conn, slotName, plugin, pglogrepl.CreateReplicationSlotOptions{Mode: pglogrepl.LogicalReplication})
	if err != nil {
		// Log error but continue since the slot might already exist (Error: 42710)
		// E.g., pgconn.PgError.Code == "42710"
		log.Printf("CreateReplicationSlot result/warn (might already exist): %v", err)
	}

	// 5. Start Replication
	err = pglogrepl.StartReplication(ctx, conn, slotName, sysident.XLogPos, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", publication),
		},
	})
	if err != nil {
		return fmt.Errorf("StartReplication failed: %w", err)
	}
	log.Printf("Logical replication started on slot %s", slotName)

	// 6. Enter Checkpoint Loop
	clientXLogPos := sysident.XLogPos
	standbyMessageTimeout := time.Second * 10
	nextStandbyMessageDeadline := time.Now().Add(standbyMessageTimeout)

	decoder := NewDecoder()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Calculate how long to block waiting for a message
		now := time.Now()
		deadline := nextStandbyMessageDeadline
		if now.After(deadline) {
			deadline = now
		}

		ctxTimeout, cancelTimeout := context.WithDeadline(ctx, deadline)
		msg, err := conn.ReceiveMessage(ctxTimeout)
		cancelTimeout()

		if err != nil {
			if pgconn.Timeout(err) {
				// Time to send Standby Status Update to keep connection alive
				err = pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{
					WALWritePosition: clientXLogPos,
					WALFlushPosition: clientXLogPos,
					WALApplyPosition: clientXLogPos,
					ClientTime:       time.Now(),
				})
				if err != nil {
					return fmt.Errorf("SendStandbyStatusUpdate failed: %w", err)
				}
				nextStandbyMessageDeadline = time.Now().Add(standbyMessageTimeout)
				continue
			}
			return fmt.Errorf("ReceiveMessage failed: %w", err)
		}

		switch msg := msg.(type) {
		case *pgproto3.CopyData:
			switch msg.Data[0] {
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
				if err != nil {
					log.Printf("ParsePrimaryKeepaliveMessage failed: %v", err)
					continue
				}
				if pkm.ReplyRequested {
					nextStandbyMessageDeadline = time.Time{}
				}

			case pglogrepl.XLogDataByteID:
				xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					log.Printf("ParseXLogData failed: %v", err)
					continue
				}

				// Decode the payload (`pgoutput` data)
				eventWrapped, err := decoder.Decode(xld.WALData, xld.WALStart)
				if err == nil && eventWrapped != nil {
					// It's a valid data event (Insert/Update/Delete). Publish to NATS context.
					err = publisher.PublishSync(ctx, eventWrapped)
					if err != nil {
						// NATS is unreachable or JetStream failed to acknowledge.
						// Critical error. We must crash and not update our local clientXLogPos.
						// Postgres will retain the WAL and resend it when we reconnect.
						return fmt.Errorf("failed to publish LSN %s to NATS: %w", xld.WALStart, err)
					}
				}

				// If NATS ack was successful (or it was a non-data message like BEGIN/COMMIT),
				// we advance our local XLog position.
				clientXLogPos = xld.WALStart + pglogrepl.LSN(len(xld.WALData))
			}
		}
	}
}

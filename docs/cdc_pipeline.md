# Level 20: The Enterprise CDC Pipeline

**Objective:** Architect a zero-data-loss Change Data Capture (CDC) service in Go to power the "Executable Ledger."
**Pattern:** Write-Ahead Log (WAL) Tailing via Logical Replication to NATS JetStream.

## 1. Database Configuration (The Source)

Before writing any Go code, the database must be configured to emit logical decoding streams.

### A. postgresql.conf

You must enable logical replication. This requires a database restart.

```ini
wal_level = logical
max_replication_slots = 5
max_wal_senders = 5
```

### B. The Publication (What to stream)

You don't want to stream every single table (like your sessions or cache tables). You only stream the Ledger tables. This is now managed by a Goose migration that automatically adds all tables from the `qbo` schema.

```sql
-- This runs automatically via a Goose migration (e.g. 014_add_logical_publication.sql)
-- Drop publication if it exists to recreate it cleanly
DROP PUBLICATION IF EXISTS toro_ledger_pub;

-- Create publication for users table and all tables in the qbo schema
CREATE PUBLICATION toro_ledger_pub FOR TABLE users;

DO $$
DECLARE
    tbl record;
BEGIN
    FOR tbl IN
        SELECT table_name
        FROM information_schema.tables
        WHERE table_schema = 'qbo' AND table_type = 'BASE TABLE'
    LOOP
        EXECUTE format('ALTER PUBLICATION toro_ledger_pub ADD TABLE qbo.%I', tbl.table_name);
    END LOOP;
END;
$$;
```

## 2. The Go Service Architecture (The "Nervous System")

The Go service is a standalone binary. It has no HTTP API. It is purely a background worker. We will use the standard `github.com/jackc/pglogrepl` package (part of the `pgx` ecosystem).

### Component 1: The Replication Connection

The service establishes a special replication connection to Postgres (different from a standard query connection). It creates a Logical Replication Slot (e.g., `toro_nats_slot`) attached to the `toro_ledger_pub` publication.

**Enterprise Feature:** The slot is persistent. If the Go app disconnects, Postgres holds the WAL files for it until it reconnects.

### Component 2: The pgoutput Decoder

Postgres sends raw binary streams using a plugin called `pgoutput`. The Go service decodes this binary into struct events:

- **Relation Message:** Tells you the schema of the table.
- **Insert Message:** Contains the new row data.
- **Update Message:** Contains the old row and new row data.
- **Delete Message:** Contains the primary key of the deleted row.

### Component 3: The NATS Publisher

It converts the decoded row into a standardized JSON envelope.

```json
{
  "event_id": "lsn-1A-2B3C", 
  "table": "invoices",
  "action": "INSERT",
  "timestamp": "2026-02-19T11:00:00Z",
  "data": { "id": 123, "amount": 5000, "status": "pending" }
}
```

It publishes to NATS JetStream subject: `ledger.invoices.insert`.

## 3. The Checkpoint Loop (Critical Path)

This is how you prevent data loss.

```go
// PSEUDO-CODE FOR ENTERPRISE CHECKPOINTING
for {
    // 1. Receive binary message from Postgres WAL
    msg := ReceiveFromPostgres() 
    lsn := msg.WALStart
    
    // 2. Decode the message into a Toro Event
    event := DecodePgOutput(msg)
    
    // 3. Publish to NATS and wait for acknowledgment
    // WARNING: Do not use async publish here. Use synchronous publish 
    // to ensure NATS JetStream has persisted it to disk.
    ack, err := js.Publish("ledger."+event.Table, event.JSON())
    if err != nil {
        // NATS is down. DO NOT acknowledge to Postgres. 
        // Panic/Restart the service. Postgres will resend this LSN later.
        log.Fatalf("Failed to publish to NATS: %v", err)
    }

    // 4. Acknowledge back to Postgres
    // Now that NATS has it, we tell Postgres to advance our cursor.
    SendStandbyStatusUpdateToPostgres(lsn)
}
```

## 4. Handling the "Two Generals Problem" (Idempotency)

**The Risk:** What if NATS saves the message to disk, but the network drops before NATS can send the PubAck back to Go?

1. Go thinks it failed. It crashes.
2. Go restarts, reads the same LSN from Postgres, and sends it to NATS again.
3. Now you have duplicate events in NATS.

**The Enterprise Solution: NATS MsgID Deduplication.**
When publishing to NATS JetStream, you must provide a unique Message ID. The perfect Message ID is the Postgres LSN because it is mathematically unique and monotonic.

```go
// NATS JetStream automatically ignores duplicates if MsgId is provided
msgId := fmt.Sprintf("%s", lsn) // e.g., "1A/2B3C"
js.PublishMsg(&nats.Msg{
    Subject: "ledger.invoices.insert",
    Data:    eventJSON,
    Header:  nats.Header{"Nats-Msg-Id": []string{msgId}},
})
```

**Result:** Even if the Go service loops and sends the same LSN three times, NATS will discard the duplicates. You achieve Exactly-Once Processing semantics.

## 5. Security & Operations

- **User Permissions:** The Postgres user running this service must have the `REPLICATION` role.
- **Monitoring:** You must monitor the `pg_stat_replication` view in Postgres. If the `replay_lag` grows, it means your Go service is falling behind or crashed, and Postgres WAL disks will eventually fill up.
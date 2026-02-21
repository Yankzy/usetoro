package cdc

import (
	"fmt"
	"time"

	"github.com/jackc/pglogrepl"
)

// Event is the standardized JSON payload sent to NATS JetStream
type Event struct {
	EventID   string         `json:"event_id"` // Matches the Postgres LSN (1A/2B3C)
	Table     string         `json:"table"`
	Action    string         `json:"action"` // INSERT, UPDATE, DELETE
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// Decoder holds the replication stream state, mapping RelationIDs to schema metadata
type Decoder struct {
	relations map[uint32]*pglogrepl.RelationMessage
}

func NewDecoder() *Decoder {
	return &Decoder{
		relations: make(map[uint32]*pglogrepl.RelationMessage),
	}
}

// Decode transforms raw pgoutput binary messages into our Toro Event structs.
// For schema tracking, it internally caches RelationMessages.
// If it returns a non-nil Event, that event represents row data to be published.
func (d *Decoder) Decode(walData []byte, lsn pglogrepl.LSN) (*Event, error) {
	// Parse the logical replication payload
	logicalMsg, err := pglogrepl.Parse(walData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse logical replication message: %w", err)
	}

	switch msg := logicalMsg.(type) {
	case *pglogrepl.RelationMessage:
		// Postgres tells us the schema of a table. We cache it.
		d.relations[msg.RelationID] = msg
		return nil, nil

	case *pglogrepl.InsertMessage:
		rel, ok := d.relations[msg.RelationID]
		if !ok {
			return nil, fmt.Errorf("unknown relation ID: %d", msg.RelationID)
		}
		data := d.decodeTuples(rel, msg.Tuple)
		return &Event{
			EventID:   lsn.String(),
			Table:     rel.RelationName,
			Action:    "INSERT",
			Timestamp: time.Now(),
			Data:      data,
		}, nil

	case *pglogrepl.UpdateMessage:
		rel, ok := d.relations[msg.RelationID]
		if !ok {
			return nil, fmt.Errorf("unknown relation ID: %d", msg.RelationID)
		}
		data := d.decodeTuples(rel, msg.NewTuple)
		return &Event{
			EventID:   lsn.String(),
			Table:     rel.RelationName,
			Action:    "UPDATE",
			Timestamp: time.Now(),
			Data:      data,
		}, nil

	case *pglogrepl.DeleteMessage:
		rel, ok := d.relations[msg.RelationID]
		if !ok {
			return nil, fmt.Errorf("unknown relation ID: %d", msg.RelationID)
		}
		data := d.decodeTuples(rel, msg.OldTuple)
		return &Event{
			EventID:   lsn.String(),
			Table:     rel.RelationName,
			Action:    "DELETE",
			Timestamp: time.Now(),
			Data:      data,
		}, nil
	}

	// For other messages like Begin, Commit, or Origin, we do nothing.
	return nil, nil
}

// decodeTuples dynamically converts pgoutput Tuples into heavily typed Go maps based on the cached relation metadata
func (d *Decoder) decodeTuples(rel *pglogrepl.RelationMessage, tuple *pglogrepl.TupleData) map[string]any {
	result := make(map[string]any)
	if tuple == nil {
		return result
	}

	for i, col := range tuple.Columns {
		if i >= len(rel.Columns) {
			break
		}
		colInfo := rel.Columns[i]

		// pglogrepl marks data Types (n=null, u=unchanged toast, t=text, b=binary format)
		switch col.DataType {
		case 'n': // null
			result[colInfo.Name] = nil
		case 'u': // unchanged toast
			result[colInfo.Name] = "UNCHANGED_TOAST"
		case 't': // text (default encoding for most pgoutput data)
			result[colInfo.Name] = string(col.Data)
		case 'b': // binary (rarely used unless specified during start replication)
			result[colInfo.Name] = col.Data
		}
	}
	return result
}

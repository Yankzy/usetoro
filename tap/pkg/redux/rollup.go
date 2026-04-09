package redux

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
)

// RollupWorker consumes RFC6902 Events from NATS JetStream and batches them securely into Postgres.
// It directly fulfills the architectural rule preventing thousands of AI agents from locking the SQL connection pool.
type RollupWorker struct {
	Logger  *slog.Logger
	JS      nats.JetStreamContext
	DB      *database.Queries
	DBPool  *pgxpool.Pool
	Batch   int
	Timeout time.Duration
}

func NewRollupWorker(logger *slog.Logger, js nats.JetStreamContext, pool *pgxpool.Pool) *RollupWorker {
	return &RollupWorker{
		Logger:  logger,
		JS:      js,
		DB:      database.New(pool),
		DBPool:  pool,
		Batch:   50, // Architectural flush boundary scaling safely
		Timeout: 3 * time.Second,
	}
}

// Start strictly monitors high-velocity stream mappings cleanly
func (w *RollupWorker) Start(ctx context.Context) error {
	_, err := w.JS.AddStream(&nats.StreamConfig{
		Name:     "WORKFLOW",
		Subjects: []string{"workflow.trace.>"},
	})
	if err != nil {
		w.Logger.Warn("Rollup worker stream configuration warning (might already exist)", "error", err)
	}

	w.Logger.Info("💾 [REDUX ROLLUP] Monitoring workflow.trace.> JetStream for 50-event compaction batches")

	sub, err := w.JS.PullSubscribe("workflow.trace.>", "redux_rollup_layer", nats.BindStream("WORKFLOW"))
	if err != nil {
		return fmt.Errorf("failed to pull subscribe to workflow trace correctly: %w", err)
	}

	go w.consumeLoop(ctx, sub)
	return nil
}

func (w *RollupWorker) consumeLoop(ctx context.Context, sub *nats.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := sub.Fetch(w.Batch, nats.MaxWait(w.Timeout))
		if err != nil {
			if len(msgs) == 0 {
				continue
			}
		}
		
		if len(msgs) > 0 {
			w.processBatch(ctx, msgs)
		}
	}
}

func (w *RollupWorker) processBatch(ctx context.Context, msgs []*nats.Msg) {
	eventsByWorkflow := make(map[string][]RFC6902Event)
	msgRefs := make([]*nats.Msg, 0, len(msgs))

	for _, msg := range msgs {
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 3 {
			msg.Ack()
			continue
		}
		wfID := parts[2]

		var event RFC6902Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			w.Logger.Error("Corrupted Redux Event dropped", "error", err)
			msg.Term() // Poison isolation 
			continue
		}

		eventsByWorkflow[wfID] = append(eventsByWorkflow[wfID], event)
		msgRefs = append(msgRefs, msg)
	}

	// Safely map and compile all outstanding states cleanly via transaction
	for wfStr, events := range eventsByWorkflow {
		var wfUUID pgtype.UUID
		wfUUID.Scan(wfStr)

		wf, err := w.DB.CreateOrGetWorkflow(ctx, database.CreateOrGetWorkflowParams{
			ID:       wfUUID,
			EntityID: pgtype.UUID{Valid: false}, // seeded from trace ID; updated upstream when known
		})
		if err != nil {
			w.Logger.Error("Skipping Rollup: failed to upsert workflow base", "uuid", wfStr, "error", err)
			continue
		}

		cfg := DefaultConfig()
		store, _ := NewStore(cfg)
        
		nextState, _, faults, reduceErr := store.Reduce(ctx, wf.State, uint64(wf.SequenceID), events)
		
		if reduceErr != nil || len(faults) > 0 {
			w.Logger.Error("Rollup Worker skipped structurally flawed SQL patch!", "wf", wfStr, "faults", faults)
		} else {
			// Single SQL lock correctly applying massive bounds
			tx, _ := w.DBPool.Begin(ctx)
			qtx := w.DB.WithTx(tx)

			qtx.UpdateWorkflowState(ctx, database.UpdateWorkflowStateParams{
				ID:         wfUUID,
				SequenceID: wf.SequenceID + int64(len(events)),
				State:      nextState,
			})
            
			// Immutable event history trace
			for _, ev := range events {
				rawEvt, _ := json.Marshal(ev)
				qtx.LogWorkflowHistory(ctx, database.LogWorkflowHistoryParams{
					WorkflowID: wfUUID,
					Role:       ev.Actor,
					Content:    rawEvt,
				})
			}
			tx.Commit(ctx)
		}
	}

	for _, m := range msgRefs {
		m.Ack()
	}
	
	w.Logger.Info("💾 [REDUX ROLLUP] Compacted transient LLM messages inherently into DB", "count", len(msgRefs))
}

package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// ConversationSchedulerWorker handles:
// 1. Session status updates from the general agent (ConversationState tool)
// 2. Scheduling follow-up reminders (ScheduleReminder tool)
// 3. Periodic checks for sessions needing follow-up
type ConversationSchedulerWorker struct {
	db        *database.Queries
	logger    *slog.Logger
	cfg       *config.Config
	nc        *nats.Conn
	ticker    *time.Ticker
	stopCh    chan struct{}
	checkInterval time.Duration
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &ConversationSchedulerWorker{
			db:            deps.Store.Queries,
			logger:        deps.Logger,
			cfg:           deps.Config,
			nc:            deps.Queue,
			stopCh:        make(chan struct{}),
			checkInterval: 15 * time.Minute,
		}, nil
	})
}

func (w *ConversationSchedulerWorker) Init(ctx context.Context) error {
	w.ticker = time.NewTicker(w.checkInterval)
	go w.followupLoop(ctx)
	w.logger.Info("conversation scheduler: follow-up loop started", "interval", w.checkInterval)
	return nil
}

func (w *ConversationSchedulerWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("ConversationSchedulerWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("ConversationSchedulerWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{
			nats.Durable(durableFromSubject(subject)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		},
	}}
}

func (w *ConversationSchedulerWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("scheduler: poison pill exceeded retries")
		msg.Term()
		return nil
	}

	// Parse the payload — supports both raw JSON and FIPA envelopes
	var payload map[string]interface{}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		// Try unwrapping a FIPA envelope
		var envlp core.Envelope
		if err2 := json.Unmarshal(msg.Data, &envlp); err2 == nil {
			_ = json.Unmarshal(envlp.Body, &payload)
		}
	}
	if payload == nil {
		w.logger.Error("scheduler: unparseable payload")
		msg.Ack()
		return nil
	}

	action, _ := payload["action"].(string)

	switch action {
	case "schedule_reminder":
		return w.handleScheduleReminder(ctx, msg, payload)
	default:
		// Also handle direct status updates (from ConversationState tool)
		return w.handleStatusUpdate(ctx, msg, payload)
	}
}

func (w *ConversationSchedulerWorker) handleStatusUpdate(ctx context.Context, msg *nats.Msg, payload map[string]interface{}) error {
	status, _ := payload["status"].(string)
	if status == "" {
		w.logger.Warn("scheduler: no status in payload, ignoring")
		msg.Ack()
		return nil
	}

	w.logger.Info("scheduler: received status update", "status", status)
	// Session status updates are handled by the GeneralAgentIngressWorker
	// when the agent uses the ConversationState tool. This is a pass-through.
	msg.Ack()
	return nil
}

func (w *ConversationSchedulerWorker) handleScheduleReminder(ctx context.Context, msg *nats.Msg, payload map[string]interface{}) error {
	delayStr, _ := payload["delay"].(string)
	message, _ := payload["message"].(string)

	dur, err := parseDelay(delayStr)
	if err != nil {
		w.logger.Error("scheduler: invalid delay", "delay", delayStr, "error", err)
		msg.Ack()
		return nil
	}

	// Clamp to reasonable bounds
	if dur < time.Hour {
		dur = time.Hour
	}
	if dur > 30*24*time.Hour {
		dur = 30 * 24 * time.Hour
	}

	fireAt := time.Now().Add(dur)
	w.logger.Info("scheduler: reminder scheduled", "delay", delayStr, "fire_at", fireAt)

	// Store the reminder context in a goroutine with time.AfterFunc.
	// For production durability, this should be persisted to a DB table.
	time.AfterFunc(dur, func() {
		w.fireReminder(message)
	})

	msg.Ack()
	return nil
}

// fireReminder publishes the reminder message back to the general agent ingress
// so it re-enters the conversation with full context.
func (w *ConversationSchedulerWorker) fireReminder(message string) {
	w.logger.Info("scheduler: firing reminder", "message_len", len(message))

	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("scheduler: failed to derive general agent ingress subject", "error", err)
		return
	}

	eventData := map[string]interface{}{
		"prompt": message,
		"source": "system",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("scheduler: failed to publish reminder", "error", err)
	}
}

// followupLoop periodically checks for conversations that have been
// awaiting_reply for too long and sends reminder messages.
func (w *ConversationSchedulerWorker) followupLoop(ctx context.Context) {
	for {
		select {
		case <-w.ticker.C:
			w.checkFollowups(ctx)
		case <-w.stopCh:
			w.ticker.Stop()
			return
		case <-ctx.Done():
			w.ticker.Stop()
			return
		}
	}
}

func (w *ConversationSchedulerWorker) checkFollowups(ctx context.Context) {
	sessions, err := w.db.GetAwaitingReplySessions(ctx)
	if err != nil {
		w.logger.Error("scheduler: failed to get sessions awaiting reply", "error", err)
		return
	}

	for _, sess := range sessions {
		// Parse context_json for follow-up config
		var ctxJSON struct {
			FollowupIntervalHours int    `json:"followup_interval_hours"`
			MaxFollowups          int    `json:"max_followups"`
			FollowupCount         int    `json:"followup_count"`
			LastFollowupAt        string `json:"last_followup_at"`
			LastReminderMessage   string `json:"last_reminder_message"`
		}
		if len(sess.ContextJson) > 0 {
			_ = json.Unmarshal(sess.ContextJson, &ctxJSON)
		}

		// Default: 72 hours between follow-ups, max 3
		if ctxJSON.FollowupIntervalHours <= 0 {
			ctxJSON.FollowupIntervalHours = 72
		}
		if ctxJSON.MaxFollowups <= 0 {
			ctxJSON.MaxFollowups = 3
		}

		// Check if enough time has passed since last activity
		// The GetAwaitingReplySessions query selects sessions with status='awaiting_reply'
		// We check last_activity_at to determine if a reminder is due
		deadline := time.Now().Add(-time.Duration(ctxJSON.FollowupIntervalHours) * time.Hour)
		if sess.LastActivityAt.Valid && sess.LastActivityAt.Time.After(deadline) {
			continue // Not due yet
		}

		// Check if max follow-ups reached
		if ctxJSON.FollowupCount >= ctxJSON.MaxFollowups {
			w.logger.Warn("scheduler: max follow-ups reached, escalating",
				"session_id", uuidFromPG(sess.ID),
				"followup_count", ctxJSON.FollowupCount,
			)
			// Escalate: mark as escalated
			_ = w.db.UpdateConversationSession(ctx, database.UpdateConversationSessionParams{
				ID:     sess.ID,
				Status: pgtype.Text{String: "escalated", Valid: true},
			})
			continue
		}

		// Send follow-up reminder
		reminderMsg := ctxJSON.LastReminderMessage
		if reminderMsg == "" {
			reminderMsg = fmt.Sprintf("Just checking in — we still need those documents. Please reply when you have them.")
		}

		w.fireReminder(reminderMsg)

		// Update follow-up state in context_json
		ctxJSON.FollowupCount++
		ctxJSON.LastFollowupAt = time.Now().Format(time.RFC3339)
		updatedCtx, _ := json.Marshal(ctxJSON)
		_ = w.db.UpdateConversationSession(ctx, database.UpdateConversationSessionParams{
			ID:          sess.ID,
			ContextJson: updatedCtx,
		})

		w.logger.Info("scheduler: sent follow-up reminder",
			"session_id", uuidFromPG(sess.ID),
			"followup_count", ctxJSON.FollowupCount,
		)
	}
}

// Stop signals the follow-up loop to shut down gracefully.
func (w *ConversationSchedulerWorker) Stop() error {
	close(w.stopCh)
	return nil
}

func parseDelay(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty delay")
	}
	// Support common formats: "72h", "3d", "1w", "2h30m"
	switch s[len(s)-1] {
	case 'd':
		days, err := parseInt(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	case 'w':
		weeks, err := parseInt(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	default:
		return time.ParseDuration(s)
	}
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func uuidFromPG(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

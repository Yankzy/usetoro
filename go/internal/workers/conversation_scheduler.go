package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
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
// 4. Distributed internal cron via toro_core.scheduled_jobs
type ConversationSchedulerWorker struct {
	db            *database.Queries
	logger        *slog.Logger
	cfg           *config.Config
	nc            *nats.Conn
	ticker        *time.Ticker
	hourlyTicker  *time.Ticker
	secondlyTicker *time.Ticker
	stopCh        chan struct{}
	checkInterval time.Duration

	// In-memory buffer for scheduled jobs to execute within the current hour
	jobBufferMu sync.Mutex
	jobBuffer   map[string]database.ToroCoreScheduledJob
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
			jobBuffer:     make(map[string]database.ToroCoreScheduledJob),
		}, nil
	})
}

func (w *ConversationSchedulerWorker) Init(ctx context.Context) error {
	w.ticker = time.NewTicker(w.checkInterval)
	w.hourlyTicker = time.NewTicker(time.Hour)
	w.secondlyTicker = time.NewTicker(time.Second)

	// Pre-load the job buffer immediately on startup
	w.pollHourlyJobs(ctx)

	go w.followupLoop(ctx)
	go w.hourlyLoop(ctx)
	go w.secondlyLoop(ctx)

	w.logger.Info("conversation scheduler: loops started", "interval", w.checkInterval)
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

	// Build the event payload that we will eventually publish
	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("scheduler: failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}
	eventData := map[string]interface{}{
		"prompt": message,
		"source": "system",
	}
	eventBytes, _ := json.Marshal(eventData)

	// Insert into DB
	jobID, err := w.db.InsertScheduledJob(ctx, database.InsertScheduledJobParams{
		QueueSubject: subject,
		PayloadJson:  eventBytes,
		FireAt:       pgtype.Timestamptz{Time: fireAt, Valid: true},
	})
	if err != nil {
		w.logger.Error("scheduler: failed to insert scheduled job", "error", err)
		// NAK so it retries
		msg.Nak()
		return nil
	}

	// If it's firing within the current hour, push to memory buffer immediately
	// so the secondly ticker picks it up without waiting for the hourly poll.
	if time.Until(fireAt) <= time.Hour {
		w.jobBufferMu.Lock()
		w.jobBuffer[uuidFromPG(jobID)] = database.ToroCoreScheduledJob{
			ID:           jobID,
			QueueSubject: subject,
			PayloadJson:  eventBytes,
			FireAt:       pgtype.Timestamptz{Time: fireAt, Valid: true},
		}
		w.jobBufferMu.Unlock()
	}

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

func (w *ConversationSchedulerWorker) hourlyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.hourlyTicker.Stop()
			return
		case <-w.stopCh:
			w.hourlyTicker.Stop()
			return
		case <-w.hourlyTicker.C:
			w.pollHourlyJobs(ctx)
		}
	}
}

func (w *ConversationSchedulerWorker) pollHourlyJobs(ctx context.Context) {
	now := time.Now()
	oneHourLater := now.Add(time.Hour)

	jobs, err := w.db.GetPendingJobsWindow(ctx, database.GetPendingJobsWindowParams{
		FireAt:   pgtype.Timestamptz{Time: now, Valid: true},
		FireAt_2: pgtype.Timestamptz{Time: oneHourLater, Valid: true},
	})
	if err != nil {
		w.logger.Error("scheduler: failed to poll hourly jobs", "error", err)
		return
	}

	w.jobBufferMu.Lock()
	defer w.jobBufferMu.Unlock()

	// Replace the memory buffer with the newly fetched jobs
	// Note: since we run this every hour, some jobs from the previous hour might still be in here if they were missed,
	// but the query bounds are strictly [now, +1h]. It's fine to overwrite the buffer if we assume it's cleanly processed.
	// A safer way is to just merge or clear.
	w.jobBuffer = make(map[string]database.ToroCoreScheduledJob)
	for _, job := range jobs {
		w.jobBuffer[uuidFromPG(job.ID)] = job
	}

	w.logger.Info("scheduler: loaded scheduled jobs for next hour", "count", len(jobs))
}

func (w *ConversationSchedulerWorker) secondlyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.secondlyTicker.Stop()
			return
		case <-w.stopCh:
			w.secondlyTicker.Stop()
			return
		case <-w.secondlyTicker.C:
			w.fireReadyJobs(ctx)
		}
	}
}

func (w *ConversationSchedulerWorker) fireReadyJobs(ctx context.Context) {
	w.jobBufferMu.Lock()
	defer w.jobBufferMu.Unlock()

	now := time.Now()

	for id, job := range w.jobBuffer {
		if !job.FireAt.Valid || job.FireAt.Time.After(now) {
			continue // Not ready yet
		}

		// Fire it!
		w.logger.Info("scheduler: firing scheduled job", "job_id", id, "subject", job.QueueSubject)
		if err := w.nc.Publish(job.QueueSubject, job.PayloadJson); err != nil {
			w.logger.Error("scheduler: failed to publish scheduled job", "job_id", id, "error", err)
			continue
		}

		// Mark as fired in the database
		_ = w.db.MarkJobFired(ctx, job.ID)

		// Remove from memory buffer
		delete(w.jobBuffer, id)
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

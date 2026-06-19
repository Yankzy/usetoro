package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/vcoo"
	"github.com/nats-io/nats.go"
)

type VirtualCOOWorker struct {
	db      *database.Queries
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	vcooSvc *vcoo.Service
	client  *http.Client
	stopCh  chan struct{}
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &VirtualCOOWorker{
			db:      deps.Store.Queries,
			logger:  deps.Logger,
			cfg:     deps.Config,
			nc:      deps.Queue,
			vcooSvc: vcoo.NewService(deps.Store.Queries, deps.LLMClient),
			client:  &http.Client{},
			stopCh:  make(chan struct{}),
		}, nil
	})
}

func (w *VirtualCOOWorker) Init(ctx context.Context) error {
	go w.cronLoop(ctx)
	w.logger.Info("VirtualCOOWorker: cron loop started")
	return nil
}

func (w *VirtualCOOWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject = "worker.inbox.vcoo_ingress"
	}

	group := workerCfg.Group
	if group == "" {
		group = "vcoo-worker-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *VirtualCOOWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("VirtualCOOWorker: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload PostmarkInboundEmail
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("VirtualCOOWorker: failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	// Source verification: check if sender exists in user database
	_, err := w.db.GetUserVCOODataByEmail(ctx, payload.From)
	if err != nil {
		w.logger.Warn("VirtualCOOWorker: skipping ingestion, email source not found in user database",
			"sender", payload.From,
		)
		msg.Ack()
		return nil
	}

	w.logger.Info("VirtualCOOWorker: processing nightly status report", "sender", payload.From)
	err = w.vcooSvc.ProcessNightlyReport(ctx, payload.From, payload.TextBody)
	if err != nil {
		w.logger.Error("VirtualCOOWorker: failed to process nightly report", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("VirtualCOOWorker: nightly status report recorded successfully")
	msg.Ack()
	return nil
}

func (w *VirtualCOOWorker) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkCronEvents(ctx)
		}
	}
}

func (w *VirtualCOOWorker) checkCronEvents(ctx context.Context) {
	loc := getCasablancaLocation()
	now := time.Now().In(loc)
	todayStr := now.Format("2006-01-02")

	// Fetch all users with initialized VCOO states
	users, err := w.db.ListUsersWithVCOO(ctx)
	if err != nil {
		w.logger.Error("VirtualCOOWorker: failed to list VCOO users from database", "error", err)
		return
	}

	for _, userData := range users {
		var vState tools.VCOOStateStruct
		if len(userData.VcooState) > 0 && string(userData.VcooState) != "{}" {
			_ = json.Unmarshal(userData.VcooState, &vState)
		}

		// 1. Check Daily Brief Egress (Every morning at 08:00 Casablanca time)
		if now.Hour() == 8 && now.Minute() == 0 {
			if vState.LastBriefDate != todayStr {
				w.logger.Info("VirtualCOOWorker: executing Daily Command Brief cron run", "email", userData.Email)
				briefText, err := w.vcooSvc.GenerateAndLogDailyBrief(ctx, userData.Email)
				if err != nil {
					w.logger.Error("VirtualCOOWorker: failed to generate command brief", "email", userData.Email, "error", err)
					continue
				}

				// Send brief via Postmark HTTP API
				w.sendEmail(ctx, userData.Email, briefText, vState.RestrictionActive)
			}
		}

		// 2. Check Weekly Velocity calculation (Every Sunday night at 23:59 Casablanca time)
		if now.Weekday() == time.Sunday && now.Hour() == 23 && now.Minute() == 59 {
			if vState.LastVelocityUpdate != todayStr {
				w.logger.Info("VirtualCOOWorker: executing Weekly Velocity calculation cron run", "email", userData.Email)
				outputMsg, err := w.vcooSvc.ExecuteWeeklyVelocityCheck(ctx, userData.Email)
				if err != nil {
					w.logger.Error("VirtualCOOWorker: failed to execute weekly velocity check", "email", userData.Email, "error", err)
					continue
				}
				w.logger.Info("VirtualCOOWorker: weekly velocity check complete", "email", userData.Email, "result", outputMsg)
			}
		}
	}
}

func (w *VirtualCOOWorker) sendEmail(ctx context.Context, to string, body string, restrictionActive bool) {
	if w.cfg.PostmarkServerToken == "" {
		w.logger.Warn("VirtualCOOWorker: postmark token not configured, skipping brief dispatch")
		return
	}

	from := w.cfg.PostmarkSenderSignature
	if from == "" {
		from = "coo@inbound.yourplatform.com"
	}

	subjectStatus := "NORMAL"
	if restrictionActive {
		subjectStatus = "ACTIVE"
	}
	subject := fmt.Sprintf("[COMMAND BRIEF] SYSTEM DIRECTIVE - VELOCITY TRACKING %s", subjectStatus)

	payload := map[string]interface{}{
		"From":          from,
		"To":            to,
		"Subject":       subject,
		"TextBody":      body,
		"MessageStream": "outbound",
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(jsonPayload))
	if err != nil {
		w.logger.Error("VirtualCOOWorker: failed to create postmark request", "error", err)
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", w.cfg.PostmarkServerToken)

	resp, err := w.client.Do(req)
	if err != nil {
		w.logger.Error("VirtualCOOWorker: failed to send brief email", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		w.logger.Error("VirtualCOOWorker: postmark API error sending brief",
			"status", resp.StatusCode,
			"error", errResp,
		)
		return
	}

	w.logger.Info("VirtualCOOWorker: daily brief dispatched successfully", "to", to)
}

func getCasablancaLocation() *time.Location {
	loc, err := time.LoadLocation("Africa/Casablanca")
	if err != nil {
		loc = time.FixedZone("Casablanca", 3600)
	}
	return loc
}

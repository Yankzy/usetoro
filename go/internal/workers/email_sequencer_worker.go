package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// CampaignTriggerEvent represents a request to execute a specific step of a campaign for a prospect.
// Subject: ase.events.email.campaign.triggered
type CampaignTriggerEvent struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	ProspectID uuid.UUID `json:"prospect_id"`
	CampaignID uuid.UUID `json:"campaign_id"`
	StepIndex  int       `json:"step_index"`
}

// EmailDispatchEvent represents a fully rendered email payload ready to be sent via SMTP.
// Subject: ase.events.email.pipeline.dispatch
type EmailDispatchEvent struct {
	TenantID        uuid.UUID `json:"tenant_id"`
	ProspectID      uuid.UUID `json:"prospect_id"`
	CampaignID      uuid.UUID `json:"campaign_id"`
	StepIndex       int       `json:"step_index"`
	AccountID       uuid.UUID `json:"account_id"`
	ToEmail         string    `json:"to_email"`
	Subject         string    `json:"subject"`
	BodyHTML        string    `json:"body_html"`
	BodyText        string    `json:"body_text"`                   // Fallback text version
	ThreadMessageID string    `json:"thread_message_id,omitempty"` // For replying in thread
}

type EmailSequencerWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewEmailSequencerWorker(deps.Store.Queries, deps.Queue, deps.Logger), nil
	})
}

func NewEmailSequencerWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger) *EmailSequencerWorker {
	return &EmailSequencerWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "email_sequencer"),
	}
}

func (w *EmailSequencerWorker) Init(ctx context.Context) error {
	return nil
}

func (w *EmailSequencerWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "ase.events.email.campaign.triggered",
			Group:   "email-sequencer-worker",
		},
	}
}

func (w *EmailSequencerWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event CampaignTriggerEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("failed to unmarshal campaign trigger event", "error", err)
		return nil // Drop invalid payload
	}

	w.logger.Info("processing campaign step", "prospect_id", event.ProspectID, "step_index", event.StepIndex)

	// 1. Validate prospect status
	prospect, err := w.db.GetProspectByID(ctx, pgtype.UUID{Bytes: event.ProspectID, Valid: true})
	if err != nil {
		w.logger.Error("failed to get prospect", "prospect_id", event.ProspectID, "error", err)
		return err // Retry on DB error
	}

	if prospect.Status != "active" {
		w.logger.Info("dropping event for inactive prospect", "prospect_id", event.ProspectID, "status", prospect.Status)
		return nil // Drop event
	}

	// 2. Get the current campaign step
	step, err := w.db.GetCampaignStep(ctx, database.GetCampaignStepParams{
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
		StepNumber: int32(event.StepIndex),
	})
	if err != nil {
		w.logger.Error("failed to get campaign step", "campaign_id", event.CampaignID, "step", event.StepIndex, "error", err)
		return nil // Missing step, drop
	}

	// 3. Render Templates
	var prospectMeta map[string]any
	if len(prospect.Metadata) > 0 {
		_ = json.Unmarshal(prospect.Metadata, &prospectMeta)
	} else {
		prospectMeta = make(map[string]any)
	}

	// Inject root fields into meta for easy templating
	prospectMeta["email"] = prospect.Email
	if prospect.FirstName.Valid {
		prospectMeta["first_name"] = prospect.FirstName.String
	} else {
		prospectMeta["first_name"] = "there"
	}
	if prospect.LastName.Valid {
		prospectMeta["last_name"] = prospect.LastName.String
	}

	subjTmpl, err := template.New("subject").Option("missingkey=default").Parse(step.SubjectTemplate)
	if err != nil {
		w.logger.Error("failed to parse subject template", "error", err)
		return nil
	}
	var subjBuf bytes.Buffer
	if err := subjTmpl.Execute(&subjBuf, prospectMeta); err != nil {
		w.logger.Error("failed to execute subject template", "error", err)
		return nil
	}

	bodyTmpl, err := template.New("body").Option("missingkey=default").Parse(step.BodyTemplate)
	if err != nil {
		w.logger.Error("failed to parse body template", "error", err)
		return nil
	}
	var bodyBuf bytes.Buffer
	if err := bodyTmpl.Execute(&bodyBuf, prospectMeta); err != nil {
		w.logger.Error("failed to execute body template", "error", err)
		return nil
	}

	// 4. Fetch an active email account for dispatch
	account, err := w.db.GetNextAvailableEmailAccount(ctx, prospect.TenantID)
	if err != nil {
		w.logger.Error("failed to find available email account", "error", err)
		return err // Retry later when accounts free up
	}

	// 5. Schedule the Send (Publish to Dispatcher)
	dispatchEvt := EmailDispatchEvent{
		TenantID:   event.TenantID,
		ProspectID: prospect.ID.Bytes,
		CampaignID: event.CampaignID,
		StepIndex:  event.StepIndex,
		AccountID:  account.ID.Bytes,
		ToEmail:    prospect.Email,
		Subject:    subjBuf.String(),
		BodyHTML:   bodyBuf.String(),
		BodyText:   bodyBuf.String(), // simple fallback
	}

	dispatchData, _ := json.Marshal(dispatchEvt)
	if err := w.nc.Publish("ase.events.email.pipeline.dispatch", dispatchData); err != nil {
		w.logger.Error("failed to publish to dispatch queue", "error", err)
		return err
	}

	// 6. Schedule the Follow-up Step
	nextStepIndex := event.StepIndex + 1
	nextStep, err := w.db.GetCampaignStep(ctx, database.GetCampaignStepParams{
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
		StepNumber: int32(nextStepIndex),
	})

	if err == nil { // Next step exists!
		delay := nextStep.DelayDuration.Microseconds * 1000 // Convert microseconds to nanoseconds
		fireAt := time.Now().Add(time.Duration(delay))

		nextEvent := CampaignTriggerEvent{
			TenantID:   event.TenantID,
			ProspectID: event.ProspectID,
			CampaignID: event.CampaignID,
			StepIndex:  nextStepIndex,
		}
		nextData, _ := json.Marshal(nextEvent)

		// Insert into scheduled_jobs
		_, err = w.db.InsertScheduledJob(ctx, database.InsertScheduledJobParams{
			QueueSubject: "ase.events.email.campaign.triggered",
			PayloadJson:  nextData,
			FireAt:       pgtype.Timestamptz{Time: fireAt, Valid: true},
		})
		if err != nil {
			w.logger.Error("failed to schedule next step", "error", err)
			return err
		}
		w.logger.Info("scheduled next campaign step", "step", nextStepIndex, "fire_at", fireAt)
	}

	return nil
}

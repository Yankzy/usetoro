package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
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
	TenantID        uuid.UUID  `json:"tenant_id"`
	ProspectID      uuid.UUID  `json:"prospect_id"`
	CampaignID      uuid.UUID  `json:"campaign_id"`
	StepIndex       int        `json:"step_index"`
	AccountID       uuid.UUID  `json:"account_id"`
	ToEmail         string     `json:"to_email"`
	Subject         string     `json:"subject"`
	BodyHTML        string     `json:"body_html"`
	BodyText        string     `json:"body_text"`                   // Fallback text version
	ThreadMessageID string     `json:"thread_message_id,omitempty"` // For replying in thread
	LandingPageID   *uuid.UUID `json:"landing_page_id,omitempty"`
	EmailFormID     *uuid.UUID `json:"email_form_id,omitempty"`
}

type EmailSequencerStore interface {
	GetProspectByID(context.Context, pgtype.UUID) (database.GetProspectByIDRow, error)
	GetCampaignStep(context.Context, database.GetCampaignStepParams) (database.GetCampaignStepRow, error)
	GetNextAvailableEmailAccount(context.Context, pgtype.UUID) (database.GetNextAvailableEmailAccountRow, error)
	GetEmailLogsForProspect(context.Context, database.GetEmailLogsForProspectParams) ([]database.GetEmailLogsForProspectRow, error)
	HasProspectInteracted(context.Context, database.HasProspectInteractedParams) (bool, error)
	InsertScheduledJob(context.Context, database.InsertScheduledJobParams) (pgtype.UUID, error)
}

type EmailSequencerWorker struct {
	db      EmailSequencerStore
	nc      *nats.Conn
	logger  *slog.Logger
	runtime *agent.Runtime
	cfg     *config.Config
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewEmailSequencerWorker(deps.Store.Queries, deps.Queue, deps.Logger, deps.Runtime, deps.Config), nil
	})
}

func NewEmailSequencerWorker(db EmailSequencerStore, nc *nats.Conn, logger *slog.Logger, runtime *agent.Runtime, cfg *config.Config) *EmailSequencerWorker {
	return &EmailSequencerWorker{
		db:      db,
		nc:      nc,
		logger:  logger.With("worker", "email_sequencer"),
		runtime: runtime,
		cfg:     cfg,
	}
}

func (w *EmailSequencerWorker) Init(ctx context.Context) error {
	return nil
}

func (w *EmailSequencerWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)

	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("email_sequencer: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("email_sequencer: failed to derive inbox", "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "email-sequencer-worker"
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

	// 2. Check if prospect has interacted (click or reply)
	interacted, err := w.db.HasProspectInteracted(ctx, database.HasProspectInteractedParams{
		ProspectID: pgtype.UUID{Bytes: event.ProspectID, Valid: true},
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
	})
	if err != nil {
		w.logger.Error("failed to check prospect interactions", "prospect_id", event.ProspectID, "error", err)
		return err
	}

	actualStepIndex := event.StepIndex
	if interacted {
		// Needle moved forward! Go vertical: advance to the next step
		actualStepIndex = event.StepIndex + 1
		w.logger.Info("prospect interacted, advancing campaign vertically", "prospect_id", event.ProspectID, "next_step", actualStepIndex)
	} else {
		w.logger.Info("prospect silent, staying at current step index horizontally", "prospect_id", event.ProspectID, "step", actualStepIndex)
	}

	// 3. Get the campaign step to execute
	step, err := w.db.GetCampaignStep(ctx, database.GetCampaignStepParams{
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
		StepNumber: int32(actualStepIndex),
	})
	if err != nil {
		w.logger.Info("no active campaign step found for execution index, campaign completes", "campaign_id", event.CampaignID, "step", actualStepIndex)
		return nil // No step, drop event
	}

	// 4. Retrieve logs to see previous sends for horizontal variance
	logs, err := w.db.GetEmailLogsForProspect(ctx, database.GetEmailLogsForProspectParams{
		ProspectID: pgtype.UUID{Bytes: event.ProspectID, Valid: true},
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
	})
	if err != nil {
		w.logger.Error("failed to retrieve email logs", "prospect_id", event.ProspectID, "error", err)
		return err
	}

	sentCount := 0
	var sentHistory []string
	for _, logEntry := range logs {
		if logEntry.EventType == "sent" {
			sentCount++
			var meta map[string]string
			if len(logEntry.Metadata) > 0 {
				_ = json.Unmarshal(logEntry.Metadata, &meta)
			}
			if meta != nil && meta["subject"] != "" {
				sentHistory = append(sentHistory, fmt.Sprintf("Subject: %s\nBody: %s", meta["subject"], meta["body_html"]))
			}
		}
	}

	// 5. Generate Subject and Body (Original rendering vs LLM Variation)
	var finalSubject, finalBody string
	if sentCount == 0 || interacted {
		// First send of the step (or transition to a new vertical step): use default template rendering
		finalSubject, finalBody, err = w.renderTemplate(step, prospect)
		if err != nil {
			w.logger.Error("failed to render templates", "error", err)
			return nil
		}
	} else {
		// Non-response follow-up (silence): stay horizontal, generate a new variation using LLM
		w.logger.Info("prospect non-response: generating horizontal variation", "prospect_id", event.ProspectID, "attempt", sentCount)
		finalSubject, finalBody, err = w.generateLLMVariation(ctx, step, prospect, sentHistory)
		if err != nil {
			w.logger.Error("failed to generate LLM variation, falling back to original templates", "error", err)
			finalSubject, finalBody, _ = w.renderTemplate(step, prospect)
		}
	}

	// 6. Fetch an active email account for dispatch
	account, err := w.db.GetNextAvailableEmailAccount(ctx, prospect.TenantID)
	if err != nil {
		w.logger.Error("failed to find available email account", "error", err)
		return err // Retry later when accounts free up
	}

	var lpID *uuid.UUID
	if step.LandingPageID.Valid {
		id := uuid.UUID(step.LandingPageID.Bytes)
		lpID = &id
	}
	var efID *uuid.UUID
	if step.EmailFormID.Valid {
		id := uuid.UUID(step.EmailFormID.Bytes)
		efID = &id
	}

	// 7. Schedule the Send (Publish to Dispatcher)
	dispatchEvt := EmailDispatchEvent{
		TenantID:      event.TenantID,
		ProspectID:    prospect.ID.Bytes,
		CampaignID:    event.CampaignID,
		StepIndex:     actualStepIndex,
		AccountID:     account.ID.Bytes,
		ToEmail:       prospect.Email,
		Subject:       finalSubject,
		BodyHTML:      finalBody,
		BodyText:      finalBody, // simple fallback
		LandingPageID: lpID,
		EmailFormID:   efID,
	}

	dispatchData, _ := json.Marshal(dispatchEvt)
	if err := w.nc.Publish("ase.events.email.pipeline.dispatch", dispatchData); err != nil {
		w.logger.Error("failed to publish to dispatch queue", "error", err)
		return err
	}

	// 8. Schedule the Follow-up Step
	// In the asymmetric model:
	// - If they interacted: we schedule a check for StepIndex + 1 (the next vertical step)
	// - If they did not interact: we schedule a check for the same StepIndex (horizontal follow-up)
	nextStepIndex := actualStepIndex
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
			w.logger.Error("failed to schedule follow-up step", "error", err)
			return err
		}
		w.logger.Info("scheduled follow-up campaign step", "step", nextStepIndex, "fire_at", fireAt)
	}

	return nil
}

func (w *EmailSequencerWorker) renderTemplate(step database.GetCampaignStepRow, prospect database.GetProspectByIDRow) (string, string, error) {
	var prospectMeta map[string]any
	if len(prospect.Metadata) > 0 {
		_ = json.Unmarshal(prospect.Metadata, &prospectMeta)
	} else {
		prospectMeta = make(map[string]any)
	}

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
		return "", "", err
	}
	var subjBuf bytes.Buffer
	if err := subjTmpl.Execute(&subjBuf, prospectMeta); err != nil {
		return "", "", err
	}

	bodyTmpl, err := template.New("body").Option("missingkey=default").Parse(step.BodyTemplate)
	if err != nil {
		return "", "", err
	}
	var bodyBuf bytes.Buffer
	if err := bodyTmpl.Execute(&bodyBuf, prospectMeta); err != nil {
		return "", "", err
	}

	return subjBuf.String(), bodyBuf.String(), nil
}

func (w *EmailSequencerWorker) generateLLMVariation(ctx context.Context, step database.GetCampaignStepRow, prospect database.GetProspectByIDRow, history []string) (string, string, error) {
	if w.runtime == nil {
		return "", "", fmt.Errorf("agent runtime not initialized")
	}

	var prospectMeta map[string]any
	if len(prospect.Metadata) > 0 {
		_ = json.Unmarshal(prospect.Metadata, &prospectMeta)
	} else {
		prospectMeta = make(map[string]any)
	}
	firstName := "there"
	if prospect.FirstName.Valid {
		firstName = prospect.FirstName.String
	}

	originalSubject, originalBody, err := w.renderTemplate(step, prospect)
	if err != nil {
		return "", "", err
	}

	historyStr := "No previous history."
	if len(history) > 0 {
		historyStr = ""
		for i, h := range history {
			historyStr += fmt.Sprintf("=== SENT EMAIL %d ===\n%s\n\n", i+1, h)
		}
	}

	systemPrompt := `You are an elite growth copywriter and outreach specialist. 
Your goal is to perform dynamic horizontal A/B testing variations for a prospect who has not responded to our previous emails.
You must rewrite the original subject line and body text to use a completely fresh semantic hook, angle, or value proposition.
DO NOT repeat the exact same messaging, phrasing, subject line, or structure as the emails in the history.
Keep it extremely personalized, natural, and brief. No placeholder brackets.
Your response MUST be in JSON format matching this schema:
{
  "subject": "Variation subject line",
  "body": "Variation body text (HTML formatted)"
}`

	userPrompt := fmt.Sprintf(`Prospect Info:
- First Name: %s
- Last Name: %s
- Email: %s
- Extra Metadata: %v

Original Subject Template: %s
Original Body Template: %s

Previous Emails Sent (History):
%s

Please output the fresh variation JSON now.`, firstName, prospect.LastName.String, prospect.Email, prospectMeta, originalSubject, originalBody, historyStr)

	respStr, err := w.runtime.Exec(ctx, userPrompt, systemPrompt)
	if err != nil {
		return "", "", err
	}

	// Parse JSON output
	cleanResp := strings.TrimSpace(respStr)
	if strings.HasPrefix(cleanResp, "```json") {
		cleanResp = strings.TrimPrefix(cleanResp, "```json")
		cleanResp = strings.TrimSuffix(cleanResp, "```")
	} else if strings.HasPrefix(cleanResp, "```") {
		cleanResp = strings.TrimPrefix(cleanResp, "```")
		cleanResp = strings.TrimSuffix(cleanResp, "```")
	}
	cleanResp = strings.TrimSpace(cleanResp)

	var res struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(cleanResp), &res); err != nil {
		return "", "", fmt.Errorf("failed to parse LLM json response: %w (raw response: %s)", err, respStr)
	}

	if res.Subject == "" || res.Body == "" {
		return "", "", fmt.Errorf("empty values returned from LLM: subject=%q, body=%q", res.Subject, res.Body)
	}

	return res.Subject, res.Body, nil
}

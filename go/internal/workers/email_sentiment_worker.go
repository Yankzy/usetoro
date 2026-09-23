package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type EmailSentimentWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
	llm    *agent.Runtime
}

type InboundReplyEvent struct {
	ProspectID string `json:"prospect_id"`
	From       string `json:"from"`
	Body       string `json:"body"`
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		// Initialize the runtime for LLM sentiment analysis
		// Using an agent.Runtime allows us to leverage the existing LLM infrastructure
		llmRuntime := agent.NewRuntime(deps.Logger, nil, core.AgentConfig{
			DID:   "email_sentiment_analyzer",
			Model: "gpt-5.4-mini", // Using a fast/cheap model for sentiment
			SystemPrompt: `You are an expert email sentiment analyzer for a B2B sales sequence.
Read the prospect's reply and classify their intent.
You must return ONLY a JSON object with a single key "intent" and one of these exact values:
- "positive_interest": the prospect wants to talk, asked a question, or showed interest.
- "out_of_office": it's an auto-responder indicating they are away. Extract "return_date" (YYYY-MM-DD) if present.
- "not_interested": the prospect said no, asked to be unsubscribed, or was hostile.

Example output:
{"intent": "not_interested"}
OR
{"intent": "out_of_office", "return_date": "2026-08-01"}
`,
		})

		return NewEmailSentimentWorker(deps.Store.Queries, deps.Queue, deps.Logger, llmRuntime), nil
	})
}

func NewEmailSentimentWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger, llm *agent.Runtime) *EmailSentimentWorker {
	return &EmailSentimentWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "email_sentiment"),
		llm:    llm,
	}
}

func (w *EmailSentimentWorker) Init(ctx context.Context) error {
	return nil
}

func (w *EmailSentimentWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "email.inbound.reply.parsed",
			Group:   "email-sentiment-worker-v2",
		},
	}
}

func (w *EmailSentimentWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event InboundReplyEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("failed to unmarshal reply event", "error", err)
		return nil // Drop invalid payload
	}

	w.logger.Info("analyzing reply sentiment", "prospect_id", event.ProspectID)

	// Call the LLM using runtime.go
	llmResp, err := w.llm.ExecWithPaging(ctx, event.Body, "", nil, nil)
	if err != nil {
		w.logger.Error("failed to execute LLM sentiment analysis", "error", err)
		return err // Retry via NATS
	}

	// Parse JSON output from LLM
	var result struct {
		Intent     string `json:"intent"`
		ReturnDate string `json:"return_date"`
	}

	// Clean up potential markdown formatting from LLM response
	cleanResp := strings.TrimSpace(llmResp)
	cleanResp = strings.TrimPrefix(cleanResp, "```json")
	cleanResp = strings.TrimPrefix(cleanResp, "```")
	cleanResp = strings.TrimSuffix(cleanResp, "```")
	cleanResp = strings.TrimSpace(cleanResp)

	if err := json.Unmarshal([]byte(cleanResp), &result); err != nil {
		w.logger.Error("failed to parse LLM JSON output", "error", err, "raw_response", llmResp)
		// If it's a transient failure, we can return err. But often it's just a bad format, so we might want to log and ignore or retry.
		return err
	}

	// Find the prospect UUID
	var uuidBytes pgtype.UUID
	if err := uuidBytes.Scan(event.ProspectID); err != nil {
		w.logger.Error("invalid prospect UUID", "prospect_id", event.ProspectID)
		return nil
	}

	switch result.Intent {
	case "positive_interest":
		w.logger.Info("prospect showed positive interest!", "prospect_id", event.ProspectID)
		// Update status to paused so they don't get automated followups while we talk to them
		err = w.db.UpdateProspectStatus(ctx, database.UpdateProspectStatusParams{
			Status: "paused",
			ID:     uuidBytes,
		})

		// TODO: Log an opportunity or alert via Slack

	case "out_of_office":
		w.logger.Info("prospect is OOO", "prospect_id", event.ProspectID, "return_date", result.ReturnDate)
		// Update status to paused
		err = w.db.UpdateProspectStatus(ctx, database.UpdateProspectStatusParams{
			Status: "paused",
			ID:     uuidBytes,
		})

		// Log OOO event to database
		meta, _ := json.Marshal(map[string]string{"return_date": result.ReturnDate})
		_ = w.db.LogEmailEvent(ctx, database.LogEmailEventParams{
			ProspectID: uuidBytes,
			CampaignID: pgtype.UUID{Valid: false},
			ListID:     pgtype.UUID{Valid: false},
			NatsMsgID:  pgtype.Text{Valid: false},
			EventType:  "out_of_office",
			Metadata:   meta,
			UserAgent:  pgtype.Text{Valid: false},
			IpAddress:  pgtype.Text{Valid: false},
			IsHuman:    pgtype.Bool{Bool: true, Valid: true},
		})

	case "not_interested":
		w.logger.Info("prospect is not interested", "prospect_id", event.ProspectID)
		// Update status to opted_out
		err = w.db.UpdateProspectStatus(ctx, database.UpdateProspectStatusParams{
			Status: "opted_out",
			ID:     uuidBytes,
		})
	default:
		w.logger.Warn("unknown intent from LLM", "intent", result.Intent)
	}

	if err != nil {
		w.logger.Error("failed to update prospect status", "error", err)
		return err
	}

	return nil
}

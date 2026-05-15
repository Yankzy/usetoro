package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// PostmarkInboundEmail represents the JSON payload received from Postmark
type PostmarkInboundEmail struct {
	FromName      string `json:"FromName"`
	MessageStream string `json:"MessageStream"`
	From          string `json:"From"`
	FromFull      struct {
		Email       string `json:"Email"`
		Name        string `json:"Name"`
		MailboxHash string `json:"MailboxHash"`
	} `json:"FromFull"`
	To     string `json:"To"`
	ToFull []struct {
		Email       string `json:"Email"`
		Name        string `json:"Name"`
		MailboxHash string `json:"MailboxHash"`
	} `json:"ToFull"`
	Cc                string `json:"Cc"`
	Bcc               string `json:"Bcc"`
	OriginalRecipient string `json:"OriginalRecipient"`
	Subject           string `json:"Subject"`
	MessageID         string `json:"MessageID"`
	ReplyTo           string `json:"ReplyTo"`
	MailboxHash       string `json:"MailboxHash"`
	Date              string `json:"Date"`
	TextBody          string `json:"TextBody"`
	HtmlBody          string `json:"HtmlBody"`
	StrippedTextReply string `json:"StrippedTextReply"`
	Headers           []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"Headers"`
	Attachments []struct {
		Name          string `json:"Name"`
		ContentType   string `json:"ContentType"`
		ContentLength int    `json:"ContentLength"`
		Content       string `json:"Content"`
	} `json:"Attachments"`
}

type PostmarkInboundEmailWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PostmarkInboundEmailWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
		}, nil
	})
}

func (w *PostmarkInboundEmailWorker) Init(ctx context.Context) error {
	return nil
}

func (w *PostmarkInboundEmailWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("PostmarkInboundEmailWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("PostmarkInboundEmailWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
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

func (w *PostmarkInboundEmailWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload PostmarkInboundEmail
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("failed to unmarshal postmark email payload", "error", err)
		msg.Term()
		return nil
	}

	// 1. Resolve Entity ID
	lookupEmail := payload.OriginalRecipient
	if lookupEmail == "" {
		lookupEmail = payload.To
	}

	var entityID pgtype.UUID
	if lookupEmail != "" {
		var err error
		entityID, err = w.db.GetEntityIDByEmail(ctx, lookupEmail)
		if err != nil {
			w.logger.Warn("could not resolve entity_id for inbound email", "email", lookupEmail, "error", err)
		}
	}

	// 2. Extract In-Reply-To from headers
	var inReplyTo string
	for _, header := range payload.Headers {
		if strings.EqualFold(header.Name, "In-Reply-To") {
			inReplyTo = header.Value
			break
		}
	}

	// 3. Prepare metadata
	metadata := make(map[string]interface{})
	metadata["headers"] = payload.Headers
	metadata["attachments"] = payload.Attachments
	metadata["from_name"] = payload.FromName
	metadata["date"] = payload.Date

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		w.logger.Error("failed to marshal metadata", "error", err)
		metadataJSON = []byte("{}")
	}

	// 4. Save to Conversations table
	err = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:     entityID,
		Source:       "email",
		ExternalID:   payload.MessageID,
		FromHandle:   payload.From,
		ToHandle:     payload.To,
		ReplyTo:      pgtype.Text{String: payload.ReplyTo, Valid: payload.ReplyTo != ""},
		InReplyTo:    pgtype.Text{String: inReplyTo, Valid: inReplyTo != ""},
		Subject:      pgtype.Text{String: payload.Subject, Valid: true},
		BodyText:     pgtype.Text{String: payload.TextBody, Valid: true},
		BodyHtml:     pgtype.Text{String: payload.HtmlBody, Valid: true},
		StrippedText: pgtype.Text{String: payload.StrippedTextReply, Valid: payload.StrippedTextReply != ""},
		Metadata:     metadataJSON,
	})

	if err != nil {
		w.logger.Error("failed to save inbound email to conversations table", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("successfully saved inbound email", "message_id", payload.MessageID, "from", payload.From, "entity_id", entityID)
	msg.Ack()
	return nil
}

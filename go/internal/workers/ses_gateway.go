package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/ses"
	"github.com/nats-io/nats.go"
)

type SESGatewayWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	sesSvc ses.SESService
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		sesService, err := ses.NewSESService(deps.Config)
		if err != nil {
			deps.Logger.Warn("SESGatewayWorker: SES client could not be initialized", "error", err)
			// We won't strictly fail initialization here so the rest of the app can boot
			// but this worker won't function without SES credentials.
		}

		return &SESGatewayWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			sesSvc: sesService,
		}, nil
	})
}

func (w *SESGatewayWorker) Init(ctx context.Context) error {
	return nil
}

func (w *SESGatewayWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			// Listen for AWS SES SNS Webhooks (Inbound Emails)
			Subject: "worker.inbox.email.ses_inbound",
			Group:   "ses_inbound_group",
			Options: []nats.SubOpt{nats.AckExplicit()},
		},
		{
			// Listen for internal agent requests to send an outbound email via SES
			Subject: "worker.outbox.email.ses_outbound",
			Group:   "ses_outbound_group",
			Options: []nats.SubOpt{nats.AckExplicit()},
		},
	}
}

func (w *SESGatewayWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	if w.sesSvc == nil {
		w.logger.Error("SESGatewayWorker: Cannot handle message without SES configured")
		msg.Nak()
		return fmt.Errorf("SES service not initialized")
	}

	switch msg.Subject {
	case "worker.inbox.email.ses_inbound":
		return w.handleInbound(ctx, msg)
	case "worker.outbox.email.ses_outbound":
		return w.handleOutbound(ctx, msg)
	default:
		w.logger.Warn("SESGatewayWorker received unknown subject", "subject", msg.Subject)
		msg.Ack()
		return nil
	}
}

func (w *SESGatewayWorker) handleInbound(ctx context.Context, msg *nats.Msg) error {
	// 1. Parse the SNS/SES JSON payload
	inboundEmail, err := w.sesSvc.ParseInboundPayload(msg.Data)
	if err != nil {
		w.logger.Error("failed to parse SES inbound payload", "error", err)
		msg.Term() // Bad payload, don't retry
		return nil
	}

	// 2. Extract Alias and Domain from the "To" address
	alias, domain := ses.ExtractAliasAndDomain(inboundEmail.To)
	if alias == "" || domain == "" {
		w.logger.Warn("invalid recipient format", "to", inboundEmail.To)
		msg.Ack()
		return nil
	}

	// 3. Resolve the Enterprise Tenant and Agent DID
	res, err := w.db.ResolveAgentByAliasAndDomain(ctx, database.ResolveAgentByAliasAndDomainParams{
		DomainName: domain,
		AgentAlias: alias,
	})
	if err != nil {
		w.logger.Warn("could not resolve alias/domain to an agent", "alias", alias, "domain", domain, "error", err)
		// Bounce handling logic could go here
		msg.Ack()
		return nil
	}

	// 4. Construct internal Toro event payload
	eventData := map[string]interface{}{
		"entity_id":   res.EntityID,
		"target_did":  res.TargetDid,
		"source":      "email_ses",
		"subject":     inboundEmail.Subject,
		"from_handle": inboundEmail.From,
		"to_handle":   inboundEmail.To,
		"message_id":  inboundEmail.MessageID,
		"date":        inboundEmail.Date,
	}

	eventBytes, _ := json.Marshal(eventData)

	// 5. Route to the General Agent Ingress (or directly to TargetDID)
	// For now, we publish to general_agent_ingress to handle persistence and unified routing
	if err := w.nc.Publish("worker.inbox.general_agent_ingress", eventBytes); err != nil {
		w.logger.Error("failed to publish SES inbound to general ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("routed SES inbound email to agent", "entity_id", res.EntityID, "target_did", res.TargetDid)
	msg.Ack()
	return nil
}

func (w *SESGatewayWorker) handleOutbound(ctx context.Context, msg *nats.Msg) error {
	var req ses.SendAgentReplyRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		w.logger.Error("failed to unmarshal outbound SES request", "error", err)
		msg.Term()
		return nil
	}

	if err := w.sesSvc.SendAgentReply(ctx, req); err != nil {
		w.logger.Error("failed to send SES reply", "error", err)
		msg.Nak() // Retryable
		return err
	}

	w.logger.Info("sent agent reply via SES", "from", fmt.Sprintf("%s@%s", req.FromAlias, req.DomainName), "to", req.To)
	msg.Ack()
	return nil
}

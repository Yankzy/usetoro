package workers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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
	db      *database.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	client  *http.Client
	storage infra.S3Service
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		storageSvc, err := infra.NewS3Service(deps.Config)
		if err != nil {
			deps.Logger.Warn("PostmarkInboundEmailWorker: S3 storage not configured", "error", err)
			// We can proceed without it, but attachments won't upload to S3
		}

		return &PostmarkInboundEmailWorker{
			db:      deps.Store.Queries,
			pool:    deps.Store.Pool,
			logger:  deps.Logger,
			cfg:     deps.Config,
			nc:      deps.Queue,
			client:  &http.Client{},
			storage: storageSvc,
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

	// 1. Parse the agent alias from the recipient.
	// Emails go to mark@usetoro.io (not subdomain-routed). The alias
	// determines which agent handles the message.
	recipient := payload.OriginalRecipient
	if recipient == "" {
		recipient = payload.To
	}
	agentAlias, _ := parseAgentEmail(recipient)
	if agentAlias == "coo" {
		w.logger.Info("routing inbound email directly to VCOO ingress", "from", payload.From, "to", recipient)
		if w.nc != nil {
			if err := w.nc.Publish("worker.inbox.vcoo_ingress", msg.Data); err != nil {
				w.logger.Error("failed to publish VCOO ingress message", "error", err)
				msg.Nak()
				return err
			}
		}
		msg.Ack()
		return nil
	}

	// 2. Extract In-Reply-To and Message-ID from headers.
	// We parse the Message-ID (with angle brackets) from the HTTP API Headers
	// array, not the top-level MessageID which is Postmark's internal ID.
	// Email clients use the Message-ID for threading via In-Reply-To
	// and References headers.
	var inReplyTo string
	var smtpMessageID string
	for _, header := range payload.Headers {
		if strings.EqualFold(header.Name, "In-Reply-To") {
			inReplyTo = header.Value
			break
		}
		if strings.EqualFold(header.Name, "Message-ID") {
			smtpMessageID = header.Value
			break
		}
	}

	// log the session ID
	w.logger.Info("inbound email session", "postmark_message_id", payload.MessageID, "from", payload.From, "to", recipient, "agent_alias", agentAlias, "in_reply_to", inReplyTo, "smtp_message_id", smtpMessageID)

	if smtpMessageID == "" {
		w.logger.Warn("Message-ID header not found in inbound API payload, falling back to Postmark internal MessageID which will not support email threading",
			"postmark_message_id", payload.MessageID,
			"from", payload.From,
			"subject", payload.Subject,
		)
		smtpMessageID = payload.MessageID
	}

	// 3. Resolve Entity ID.
	// Priority 1: In-Reply-To references an existing conversation.
	// Priority 2: Sender's email matches a recent conversation (from_handle or to_handle).
	// Priority 3: Sender is a registered user (e.g., CPA emailing their agent).
	var entityID pgtype.UUID

	// Priority 1: In-Reply-To
	if inReplyTo != "" {
		// Try to find the entity from the referenced conversation's session
		cleanID := cleanMessageID(inReplyTo)
		refSessionID, err := w.db.GetConversationByExternalID(ctx, cleanID)

		if err != nil || !refSessionID.Valid {
			refSessionID, err = w.db.GetConversationByExternalID(ctx, inReplyTo)
		}

		if err == nil && refSessionID.Valid {
			sess, err := w.db.GetConversationSession(ctx, refSessionID)
			if err == nil {
				entityID = sess.EntityID
			}
		}

		// ASE DAG Lookup
		if !entityID.Valid && strings.Contains(inReplyTo, "<ase_") {
			var aseNodeID string
			for _, idStr := range strings.Split(inReplyTo, " ") {
				if strings.HasPrefix(idStr, "<ase_") {
					clean := strings.Trim(idStr, "<>")
					if idx := strings.Index(clean, "@"); idx != -1 {
						clean = clean[:idx]
					}
					if idx := strings.Index(clean, "__"); idx != -1 {
						clean = clean[:idx]
					}
					parts := strings.Split(clean, "_")
					if len(parts) >= 4 && parts[0] == "ase" {
						aseNodeID = parts[1]
						break
					}
				}
			}
			
			if aseNodeID != "" && w.pool != nil {
				var createdBy pgtype.UUID
				err := w.pool.QueryRow(ctx, `
					SELECT s.created_by 
					FROM fignode.staging_transactions t 
					JOIN fignode.staging_sessions s ON t.session_id = s.id 
					WHERE t.id = $1
				`, aseNodeID).Scan(&createdBy)
				if err == nil {
					entityID = createdBy
					w.logger.Info("resolved entity_id from ASE transaction", "entity_id", entityID, "ase_node_id", aseNodeID)
				} else {
					w.logger.Warn("failed to resolve entity_id from ASE transaction", "error", err, "ase_node_id", aseNodeID)
				}
			}
		}
	}

	// log the session
	w.logger.Info("inbound email session", "postmark_message_id", payload.MessageID, "from", payload.From, "to", recipient, "agent_alias", agentAlias, "in_reply_to", inReplyTo, "smtp_message_id", smtpMessageID, "entity_id", entityID.String())

	// Priority 2: Recent conversations
	if !entityID.Valid && payload.From != "" {
		recentConvs, err := w.db.GetRecentConversationsByHandle(ctx, database.GetRecentConversationsByHandleParams{
			FromHandle: payload.From,
			Limit:      10, // Fetch up to 10 recent conversations
		})
		if err == nil && len(recentConvs) > 0 {
			// Find unique entity IDs
			entityMap := make(map[string][]database.ToroCoreConversation)
			var uniqueEntities []string
			for _, conv := range recentConvs {
				if !conv.EntityID.Valid {
					continue
				}
				eID := fmt.Sprintf("%x-%x-%x-%x-%x", conv.EntityID.Bytes[0:4], conv.EntityID.Bytes[4:6], conv.EntityID.Bytes[6:8], conv.EntityID.Bytes[8:10], conv.EntityID.Bytes[10:16])

				if _, exists := entityMap[eID]; !exists {
					uniqueEntities = append(uniqueEntities, eID)
				}
				entityMap[eID] = append(entityMap[eID], conv)
			}

			if len(uniqueEntities) == 1 {
				// No ambiguity
				_ = entityID.Scan(uniqueEntities[0])
			}
		}
	}

	// Priority 3: Registered users
	if !entityID.Valid && payload.From != "" {
		id, err := w.db.GetEntityIDByEmail(ctx, payload.From)
		if err == nil {
			entityID = id
		}
	}

	if !entityID.Valid {
		w.logger.Warn("bouncing email: entity not found",
			"from", payload.From,
			"recipient", recipient,
			"agent_alias", agentAlias,
		)
		w.sendBounceReply(ctx, payload.From, payload.TextBody, payload.Subject)
		msg.Ack()
		return nil
	}

	// 3. Prepare metadata and upload attachments to S3
	metadata := make(map[string]interface{})
	metadata["headers"] = payload.Headers

	processedAttachments := make([]map[string]interface{}, 0, len(payload.Attachments))
	for i, att := range payload.Attachments {
		attMeta := map[string]interface{}{
			"Name":          att.Name,
			"ContentType":   att.ContentType,
			"ContentLength": att.ContentLength,
		}

		if w.storage != nil && att.Content != "" {
			// Decode base64
			decodedBytes, err := base64.StdEncoding.DecodeString(att.Content)
			if err != nil {
				w.logger.Error("failed to decode attachment base64", "error", err, "name", att.Name)
				attMeta["Content"] = att.Content
			} else {
				// Upload to S3 directly, no folders
				s3Key := fmt.Sprintf("%s-%s", uuid.New().String(), att.Name)

				err = w.storage.UploadFileToS3(ctx, s3Key, bytes.NewReader(decodedBytes), att.ContentType)
				if err != nil {
					w.logger.Error("failed to upload attachment to S3", "error", err, "name", att.Name)
					attMeta["Content"] = att.Content
				} else {
					attMeta["S3Key"] = s3Key
				}
			}
		} else {
			// Fallback if S3 is not configured
			attMeta["Content"] = att.Content
		}

		// Ensure we don't save the raw base64 content back into the database if we uploaded it
		payload.Attachments[i].Content = ""

		processedAttachments = append(processedAttachments, attMeta)
	}

	metadata["attachments"] = processedAttachments
	metadata["from_name"] = payload.FromName
	metadata["date"] = payload.Date

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		w.logger.Error("failed to marshal metadata", "error", err)
		metadataJSON = []byte("{}")
	}

	promptText := payload.StrippedTextReply
	if promptText == "" {
		promptText = payload.TextBody
	}

	if len(processedAttachments) > 0 {
		var attNames []string
		for _, attMeta := range processedAttachments {
			if name, ok := attMeta["Name"].(string); ok && name != "" {
				attNames = append(attNames, name)
			}
		}
		if len(attNames) > 0 {
			promptText += fmt.Sprintf("\n\n[SYSTEM: The user attached %d file(s): %s]", len(attNames), strings.Join(attNames, ", "))
		} else {
			promptText += fmt.Sprintf("\n\n[SYSTEM: The user attached %d file(s)]", len(processedAttachments))
		}
	}

	externalID := smtpMessageID

	err = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:     entityID,
		Source:       "email",
		ExternalID:   externalID,
		FromHandle:   payload.From,
		ToHandle:     payload.To,
		ReplyTo:      pgtype.Text{String: payload.ReplyTo, Valid: payload.ReplyTo != ""},
		InReplyTo:    pgtype.Text{String: inReplyTo, Valid: inReplyTo != ""},
		Subject:      pgtype.Text{String: payload.Subject, Valid: true},
		BodyText:     pgtype.Text{String: promptText, Valid: true},
		BodyHtml:     pgtype.Text{String: payload.HtmlBody, Valid: true},
		StrippedText: pgtype.Text{String: promptText, Valid: promptText != ""},
		Metadata:     metadataJSON,
	})

	if err != nil {
		w.logger.Error("failed to save inbound email to conversations table", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("successfully saved inbound email", "message_id", smtpMessageID, "from", payload.From, "entity_id", entityID)

	// 5. Check whether this email is a reply to a bridged Slack thread.
	// If the In-Reply-To header matches a known email_latest_message_id in
	// toro_threads_mappings, this is Flow 3: email follow-up → append to
	// existing Slack thread.
	if inReplyTo != "" {
		cleanID := cleanMessageID(inReplyTo)
		mapping, mappingErr := w.db.GetThreadMappingByEmailMessageID(ctx, cleanID)

		if mappingErr != nil || mapping.SlackChannelID == "" {
			mapping, mappingErr = w.db.GetThreadMappingByEmailMessageID(ctx, inReplyTo)
		}

		if mappingErr == nil && mapping.SlackChannelID != "" {
			w.logger.Info("email follow-up to bridged slack thread",
				"slack_channel", mapping.SlackChannelID,
				"slack_thread_ts", mapping.SlackParentTs,
				"in_reply_to", inReplyTo,
			)

			// Route to OmniChatWorker to post as a threaded Slack reply.
			outProof := core.Proof{
				Type:      core.ProofAPI,
				Timestamp: time.Now().Unix(),
				Data: mustMarshalRaw(map[string]interface{}{
					"body_text":        promptText,
					"source":           "slack",
					"from_handle":      payload.From,
					"to_handle":        mapping.SlackChannelID,
					"slack_channel_id": mapping.SlackChannelID,
					"slack_thread_ts":  mapping.SlackParentTs,
					"entity_id":        entityID,
				}),
			}
			proofBytes, _ := json.Marshal(outProof)

			outEnv := core.Envelope{
				ID:           uuid.New().String(),
				Timestamp:    time.Now(),
				SenderDID:    "did:toro:worker:postmark_inbound_email",
				ReceiverDID:  "did:toro:worker:omni_chat",
				Performative: core.INFORM,
				Body:         proofBytes,
			}
			outgoingBytes, _ := json.Marshal(outEnv)
			if err := w.nc.Publish("proof.outgoing.chat", outgoingBytes); err != nil {
				w.logger.Error("failed to publish bridged email to slack", "error", err)
				msg.Nak()
				return err
			}

			// Advance the email pointer so the next reply cycle can find the mapping.
			_ = w.db.UpdateThreadMappingEmailMessageID(ctx, database.UpdateThreadMappingEmailMessageIDParams{
				EmailLatestMessageID: smtpMessageID,
				SlackChannelID:       mapping.SlackChannelID,
				SlackParentTs:        mapping.SlackParentTs,
			})

			w.logger.Info("routed email follow-up to slack thread", "thread_ts", mapping.SlackParentTs)
			msg.Ack()
			return nil
		}
	}

	// 6. Route to the General Agent ingress worker (same path as HTTP /ingress?domain=general)
	// The GeneralAgentIngressWorker handles conversation persistence, agent dispatch, and
	// publishing to proof.outgoing.chat for OmniChatWorker channel delivery.
	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":          promptText,
		"entity_id":       entityID,
		"from_handle":     payload.From,
		"to_handle":       payload.To,
		"source":          "email",
		"subject":         payload.Subject,
		"agent_alias":     agentAlias,
		"in_reply_to":     inReplyTo,
		"message_id":      smtpMessageID,
		"has_attachments": len(processedAttachments) > 0,
	}

	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("routed email to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}

// sendBounceReply sends a direct reply via Postmark when the inbound message
// cannot be matched to a known entity. This avoids wasting LLM tokens on
// unresolvable messages.
func (w *PostmarkInboundEmailWorker) sendBounceReply(ctx context.Context, to, originalBody, originalSubject string) {
	// We wont reply to spam emails
	w.logger.Warn("Unknown user emailed us, No replies will be sent")

	// 	if w.cfg.PostmarkServerToken == "" {
	// 		w.logger.Warn("cannot send bounce reply: postmark token not configured")
	// 		return
	// 	}

	// 	subject := "Unable to process your message"
	// 	if originalSubject != "" {
	// 		subject = fmt.Sprintf("Re: %s", originalSubject)
	// 	}

	// 	body := fmt.Sprintf(`The recipient to your message below could not be resolved. Please double check.

	// ---
	// %s
	// ---

	// Do not reply to this email.`, originalBody)

	// 	payload := map[string]interface{}{
	// 		"From":          "do-not-reply@usetoro.io",
	// 		"To":            to,
	// 		"Subject":       subject,
	// 		"TextBody":      body,
	// 		"MessageStream": "outbound",
	// 	}

	// 	jsonPayload, _ := json.Marshal(payload)
	// 	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(jsonPayload))
	// 	if err != nil {
	// 		w.logger.Error("bounce reply: failed to create request", "error", err)
	// 		return
	// 	}
	// 	req.Header.Set("Accept", "application/json")
	// 	req.Header.Set("Content-Type", "application/json")
	// 	req.Header.Set("X-Postmark-Server-Token", w.cfg.PostmarkServerToken)

	// 	resp, err := w.client.Do(req)
	// 	if err != nil {
	// 		w.logger.Error("bounce reply: failed to send", "error", err)
	// 		return
	// 	}
	// 	defer resp.Body.Close()

	// 	if resp.StatusCode != http.StatusOK {
	// 		var errResp map[string]interface{}
	// 		_ = json.NewDecoder(resp.Body).Decode(&errResp)
	// 		w.logger.Error("bounce reply: postmark API error",
	// 			"status", resp.StatusCode,
	// 			"error", errResp,
	// 		)
	// 		return
	// 	}

	// w.logger.Info("bounce reply sent", "to", to)
}

// parseAgentEmail splits an agent email address into its routing components.
// "mark@cpa2.usetoro.io" → alias="mark", subdomain="cpa2"
func parseAgentEmail(email string) (alias, subdomain string) {
	// Strip name prefix if present: "Mark Smith <mark@cpa2.usetoro.io>"
	email = strings.TrimSpace(email)
	if idx := strings.LastIndex(email, "<"); idx >= 0 {
		email = strings.TrimSuffix(strings.TrimSpace(email[idx+1:]), ">")
	}

	parts := strings.SplitN(email, "@", 2)
	// Fallback: If the string isn't a fully qualified email address (no '@' symbol),
	// check if it's just the raw name of a configured virtual employee.
	// This acts as a lenient parser for edge cases (e.g. legacy payloads or test data).
	if len(parts) != 2 {
		cleaned := strings.ToLower(strings.TrimSpace(email))
		cfg := config.GetGlobal()
		if cfg != nil {
			if _, ok := cfg.VirtualEmployees[cleaned]; ok {
				return cleaned, ""
			}
		}

		if idx := strings.Index(cleaned, " "); idx >= 0 {
			firstWord := cleaned[:idx]
			if cfg != nil {
				if _, ok := cfg.VirtualEmployees[firstWord]; ok {
					return firstWord, ""
				}
			}
		}
		return "", ""
	}
	alias = strings.ToLower(strings.TrimSpace(parts[0]))

	domainParts := strings.SplitN(parts[1], ".", 3)
	if len(domainParts) >= 2 {
		subdomain = strings.ToLower(strings.TrimSpace(domainParts[0]))
	}

	return alias, subdomain
}

// cleanMessageID strips brackets and domain from an SMTP Message-ID
// so it can be matched against the Postmark MessageID stored in the DB.
func cleanMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	if idx := strings.Index(id, "@"); idx >= 0 {
		id = id[:idx]
	}
	return id
}

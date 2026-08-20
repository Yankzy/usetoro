package workers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	know "github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
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
	Cc                string           `json:"Cc"`
	Bcc               string           `json:"Bcc"`
	OriginalRecipient string           `json:"OriginalRecipient"`
	Subject           string           `json:"Subject"`
	MessageID         string           `json:"MessageID"`
	ReplyTo           string           `json:"ReplyTo"`
	MailboxHash       string           `json:"MailboxHash"`
	Date              string           `json:"Date"`
	TextBody          string           `json:"TextBody"`
	HtmlBody          string           `json:"HtmlBody"`
	StrippedTextReply string           `json:"StrippedTextReply"`
	Headers           []PostmarkHeader `json:"Headers"`
	Attachments       []struct {
		Name          string `json:"Name"`
		ContentType   string `json:"ContentType"`
		ContentLength int    `json:"ContentLength"`
		Content       string `json:"Content"`
		S3Key         string `json:"S3Key,omitempty"`
		SHA256        string `json:"SHA256,omitempty"`
	} `json:"Attachments"`
}

type PostmarkInboundEmailWorker struct {
	db      *database.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	storage infra.S3Service
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		storageSvc, err := infra.NewS3Service(deps.Config)
		if err != nil {
			deps.Logger.Warn("PostmarkInboundEmailWorker: S3 storage not configured", "error", err)
		}

		return &PostmarkInboundEmailWorker{
			db:      deps.Store.Queries,
			pool:    deps.Store.Pool,
			logger:  deps.Logger,
			cfg:     deps.Config,
			nc:      deps.Queue,
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
	// w.logger.Info("PostmarkInboundEmailWorker:", "MSG", msg)
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload PostmarkInboundEmail
	if errUnmarshal := json.Unmarshal(msg.Data, &payload); errUnmarshal != nil {
		w.logger.Error("PostmarkInboundEmailWorker: failed to unmarshal postmark email payload", "error", errUnmarshal)
		return nil
	}

	// 1. Parse the recipient and determine canonical addresses
	recipient := payload.OriginalRecipient
	if recipient == "" {
		recipient = payload.To
	}

	// 2. Check if this is a bridged Slack thread (bypasses standard routing)
	payloadBytes, isBridged, slackErr := ProcessBridgedSlackThread(
		ctx, w.logger, w.db, w.pool, &payload,
	)
	if slackErr != nil {
		w.logger.Error("failed to process slack bridged thread", "error", slackErr)
	}
	if isBridged {
		if w.nc != nil {
			if pubErr := w.nc.Publish("proof.outgoing.chat", payloadBytes); pubErr != nil {
				return fmt.Errorf("failed to publish bridged email to slack: %w", pubErr)
			}
		}
		return nil
	}

	// 3. Resolve sender identity (entity_id, agent alias, session_id).
	// If unverified, bounce immediately — do not run any downstream task.
	inReplyToHeader, _ := ExtractMessageHeaders(payload.Headers)
	sender, resolveErr := ResolveSender(ctx, w.logger, w.db, w.pool, payload.From, recipient, inReplyToHeader)
	if resolveErr != nil {
		Bounce(ctx, w.logger, w.cfg, payload.From, payload.TextBody, payload.Subject)
		w.logger.Error("failed to resolve sender", "error", resolveErr)
		return nil
	}

	// 4. Process attachments: Support pre-existing S3Key, or upload raw base64 content to S3
	var attachmentMetadata []map[string]interface{}

	var docStore *know.DocumentStore
	if w.pool != nil {
		docStore = know.NewDocumentStore(w.pool, w.logger)
	}
	var documentIDs []string

	for idx, att := range payload.Attachments {
		// Attachment contains base64 content that needs to be uploaded to S3
		if att.Content != "" {
			if w.storage == nil {
				w.logger.Error("PostmarkInboundEmailWorker: S3 storage service not configured, cannot upload attachment", "name", att.Name)
				continue
			}

			rawContent := strings.TrimSpace(att.Content)
			rawContent = strings.ReplaceAll(rawContent, "\r", "")
			rawContent = strings.ReplaceAll(rawContent, "\n", "")

			decodedBytes, decodeErr := base64.StdEncoding.DecodeString(rawContent)
			if decodeErr != nil {
				// Try raw unpadded decoding as fallback
				if rawBytes, rawErr := base64.RawStdEncoding.DecodeString(rawContent); rawErr == nil {
					decodedBytes = rawBytes
					decodeErr = nil
				} else if urlBytes, urlErr := base64.URLEncoding.DecodeString(rawContent); urlErr == nil {
					decodedBytes = urlBytes
					decodeErr = nil
				}
			}
			if decodeErr != nil {
				w.logger.Error("failed to decode attachment base64", "error", decodeErr, "name", att.Name)
				continue
			}

			// Convert non-PDF image attachments to PDF format for standardized S3 storage
			contentType := att.ContentType
			if strings.HasPrefix(contentType, "image/") && !strings.Contains(contentType, "pdf") {
				pdfBytes, err := convertImageToPDF(decodedBytes, contentType)
				if err == nil {
					decodedBytes = pdfBytes
					contentType = "application/pdf"
					if !strings.HasSuffix(strings.ToLower(att.Name), ".pdf") {
						att.Name = att.Name + ".pdf"
					}
				} else {
					w.logger.Warn("failed to convert image attachment to PDF", "name", att.Name, "error", err)
				}
			}
			if contentType == "" {
				contentType = "application/octet-stream"
			}

			// Calculate sha256 hash
			hashSum := sha256.Sum256(decodedBytes)
			sha256Str := hex.EncodeToString(hashSum[:])

			s3Key := ""

			// Check if document already exists to avoid redundant S3 upload
			if docStore != nil {
				existingDoc, _ := docStore.GetDocumentBySHA256(ctx, sha256Str)
				if existingDoc != nil && existingDoc.S3URL != "" {
					s3Key = existingDoc.S3URL
					w.logger.Info("PostmarkInboundEmailWorker: document already exists, skipping S3 upload", "sha256", sha256Str, "file_name", att.Name)
				}
			}
			if s3Key == "" {
				s3Key = fmt.Sprintf("%s-%s", uuid.New().String(), att.Name)
				uploadErr := w.storage.UploadFileToS3(ctx, s3Key, bytes.NewReader(decodedBytes), contentType)
				if uploadErr != nil {
					w.logger.Error("failed to upload attachment to S3", "error", uploadErr, "name", att.Name)
					// TODO: Save fails in s3_upload_fails table
					continue
				}
			}

			payload.Attachments[idx].Content = ""
			payload.Attachments[idx].S3Key = s3Key
			payload.Attachments[idx].SHA256 = sha256Str
			payload.Attachments[idx].ContentType = contentType

			docMetadata := map[string]any{
				"session_id":     sender.SessionID,
				"entity_id":      sender.EntityIDStr,
				"from_handle":    sender.FromHandle,
				"to_handle":      sender.ToHandle,
				"reply_to":       payload.ReplyTo,
				"subject":        payload.Subject,
				"agent_alias":    sender.AgentAlias,
				"external_id":    payload.MessageID,
				"s3_key":         s3Key,
				"sha256":         sha256Str,
				"content_length": len(decodedBytes),
			}

			var docID string
			if docStore != nil {
				createdDoc, _, docErr := docStore.CreateOrGetDocument(
					ctx,
					sender.SessionID,
					know.DocTypeOther,
					att.Name,
					contentType,
					s3Key,
					sha256Str,
					"EMAIL",
					sender.FromHandle,
					docMetadata,
				)
				if docErr != nil {
					w.logger.Error("PostmarkInboundEmailWorker: failed to create document record in DB", "error", docErr, "file_name", att.Name)
				} else if createdDoc != nil {
					docID = createdDoc.ID.String()
					documentIDs = append(documentIDs, docID)
					w.logger.Info("PostmarkInboundEmailWorker: saved document in DB", "doc_id", docID, "file_name", att.Name)
				}
			}

			attMetadata := map[string]interface{}{
				"document_id":    docID,
				"name":           att.Name,
				"s3_key":         s3Key,
				"sha256":         sha256Str,
				"content_type":   contentType,
				"content_length": len(decodedBytes),
			}
			attachmentMetadata = append(attachmentMetadata, attMetadata)
		} else if att.S3Key != "" {
			s3Key := att.S3Key
			sha256Str := att.SHA256
			contentType := att.ContentType
			if contentType == "" {
				contentType = "application/octet-stream"
			}

			docMetadata := map[string]any{
				"session_id":     sender.SessionID,
				"entity_id":      sender.EntityIDStr,
				"from_handle":    sender.FromHandle,
				"to_handle":      sender.ToHandle,
				"reply_to":       payload.ReplyTo,
				"subject":        payload.Subject,
				"agent_alias":    sender.AgentAlias,
				"external_id":    payload.MessageID,
				"s3_key":         s3Key,
				"sha256":         sha256Str,
				"content_length": att.ContentLength,
			}

			var docID string
			if docStore != nil {
				createdDoc, _, docErr := docStore.CreateOrGetDocument(
					ctx,
					sender.SessionID,
					know.DocTypeOther,
					att.Name,
					contentType,
					s3Key,
					sha256Str,
					"EMAIL",
					sender.FromHandle,
					docMetadata,
				)
				if docErr != nil {
					w.logger.Error("PostmarkInboundEmailWorker: failed to create document record in DB", "error", docErr, "file_name", att.Name)
				} else if createdDoc != nil {
					docID = createdDoc.ID.String()
					documentIDs = append(documentIDs, docID)
					w.logger.Info("PostmarkInboundEmailWorker: saved document in DB", "doc_id", docID, "file_name", att.Name)
				}
			}

			attMetadata := map[string]interface{}{
				"document_id":    docID,
				"name":           att.Name,
				"s3_key":         s3Key,
				"sha256":         sha256Str,
				"content_type":   contentType,
				"content_length": att.ContentLength,
			}
			attachmentMetadata = append(attachmentMetadata, attMetadata)
		}
	}

	// 5. Save conversation session and conversation message in DB
	if w.db != nil {
		sessionManager := conversation.NewSessionManager(w.db, w.logger)
		session, _, err := sessionManager.FindOrCreateSession(ctx, conversation.FindOrCreateParams{
			EntityID:          sender.EntityID,
			ParticipantHandle: sender.FromHandle,
			ToroHandle:        sender.ToHandle,
			Source:            "email",
			Subject:           payload.Subject,
		})
		if err != nil {
			w.logger.Error("PostmarkInboundEmailWorker: failed to find or create conversation session", "error", err)
		} else {
			sender.SessionID = uuidFromPG(session.ID)
		}

		inReplyTo, _ := ExtractMessageHeaders(payload.Headers)

		metaBytes, _ := json.Marshal(map[string]interface{}{
			"attachments": attachmentMetadata,
			"headers":     payload.Headers,
			"agent_alias": sender.AgentAlias,
		})

		if err := w.db.SaveConversationSessionMessage(ctx, database.SaveConversationSessionMessageParams{
			EntityID:     sender.EntityID,
			Source:       "email",
			ExternalID:   payload.MessageID,
			FromHandle:   sender.FromHandle,
			ToHandle:     sender.ToHandle,
			ReplyTo:      pgtype.Text{String: payload.ReplyTo, Valid: payload.ReplyTo != ""},
			InReplyTo:    pgtype.Text{String: inReplyTo, Valid: inReplyTo != ""},
			Subject:      pgtype.Text{String: payload.Subject, Valid: payload.Subject != ""},
			BodyText:     pgtype.Text{String: payload.TextBody, Valid: payload.TextBody != ""},
			BodyHtml:     pgtype.Text{String: payload.HtmlBody, Valid: payload.HtmlBody != ""},
			StrippedText: pgtype.Text{String: payload.StrippedTextReply, Valid: payload.StrippedTextReply != ""},
			Metadata:     metaBytes,
			SessionID:    session.ID,
			Role:         "user",
		}); err != nil {
			w.logger.Warn("PostmarkInboundEmailWorker: failed to save conversation message", "error", err)
		}
	}

	// 6. Route all incoming emails to Dynamic Agent
	destSubject := core.BuildTaskSubject("dynamic", core.ComplexityEntry, "purpose")

	// 7. Publish ONE unified email task to Dynamic Agent in a FIPA REQUEST Envelope
	if w.nc != nil {
		taskPayload := map[string]any{
			"session_id":      sender.SessionID,
			"entity_id":       sender.EntityIDStr,
			"from_handle":     sender.FromHandle,
			"to_handle":       sender.ToHandle,
			"reply_to":        payload.ReplyTo,
			"subject":         payload.Subject,
			"body_text":       payload.TextBody,
			"agent_alias":     sender.AgentAlias,
			"requested_agent": "dynamic-agent",
			"external_id":     payload.MessageID,
			"document_ids":    documentIDs,
			"attachments":     attachmentMetadata,
		}

		payloadBytes, errMarshal := json.Marshal(taskPayload)
		if errMarshal != nil {
			return fmt.Errorf("failed to marshal task payload: %w", errMarshal)
		}

		taskDef := core.TaskDefinition{
			ID:         uuid.New().String(),
			Domain:     "dynamic.purpose",
			Complexity: core.ComplexityEntry,
			Payload:    payloadBytes,
		}
		taskDefBytes, _ := json.Marshal(taskDef)

		env := core.Envelope{
			ID:           uuid.New().String(),
			Timestamp:    time.Now(),
			SenderDID:    "did:toro:worker:postmark_inbound",
			ReceiverDID:  "did:toro:agent:dynamic_purpose_1",
			Performative: core.REQUEST,
			Body:         taskDefBytes,
		}

		if envBytes, err := json.Marshal(env); err == nil {
			if pubErr := w.nc.Publish(destSubject, envBytes); pubErr != nil {
				w.logger.Warn("PostmarkInboundEmailWorker: failed to publish email task to dynamic agent", "subject", destSubject, "error", pubErr)
			} else {
				w.logger.Info("PostmarkInboundEmailWorker: published unified email task to dynamic agent", "subject", destSubject, "document_count", len(documentIDs))
			}
		}
	}

	return nil
}

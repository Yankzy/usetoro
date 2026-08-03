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
	"github.com/Yankzy/usetoro/internal/infra"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func convertImageToPDF(imgBytes []byte, contentType string) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	var opts fpdf.ImageOptions
	switch {
	case strings.Contains(contentType, "png"):
		opts.ImageType = "png"
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		opts.ImageType = "jpg"
	default:
		opts.ImageType = "png"
	}
	pdf.RegisterImageOptionsReader("img", opts, bytes.NewReader(imgBytes))
	pdf.ImageOptions("img", 10, 10, 190, 0, false, opts, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

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

	// 3. Resolve sender identity (entity_id, agent alias, realm).
	// If unverified, bounce immediately — do not run OCR or any downstream task.
	inReplyToHeader, _ := ExtractMessageHeaders(payload.Headers)
	sender, resolveErr := ResolveSender(ctx, w.logger, w.db, w.pool, payload.From, recipient, inReplyToHeader)
	if resolveErr != nil {
		Bounce(ctx, w.logger, w.cfg, payload.From, payload.TextBody, payload.Subject)
		return nil
	}

	// 4. Process attachments: Convert images to PDF, calculate sha256, upload to S3
	var attachmentMetadata []map[string]interface{}
	for idx, att := range payload.Attachments {
		if w.storage != nil && att.Content != "" {
			decodedBytes, decodeErr := base64.StdEncoding.DecodeString(att.Content)
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

			s3Key := fmt.Sprintf("%s-%s", uuid.New().String(), att.Name)
			uploadErr := w.storage.UploadFileToS3(ctx, s3Key, bytes.NewReader(decodedBytes), contentType)
			if uploadErr != nil {
				w.logger.Error("failed to upload attachment to S3", "error", uploadErr, "name", att.Name)
			} else {
				payload.Attachments[idx].Content = ""
				payload.Attachments[idx].S3Key = s3Key
				payload.Attachments[idx].SHA256 = sha256Str
				payload.Attachments[idx].ContentType = contentType

				attMetadata := map[string]interface{}{
					"name":           att.Name,
					"s3_key":         s3Key,
					"sha256":         sha256Str,
					"content_type":   contentType,
					"content_length": len(decodedBytes),
				}
				attachmentMetadata = append(attachmentMetadata, attMetadata)
			}
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

	// 6. Determine final destination subject based on alias
	var destSubject string
	switch {
	case sender.AgentAlias == "coo":
		destSubject, _ = core.BuildWorkerInboxFromActivity("workers.vcoo_ingress")
	case strings.HasPrefix(sender.AgentAlias, "rap_"):
		destSubject = "events.accounting.1.pcm_bookkeeping"
	default:
		derived, deriveErr := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
		if deriveErr != nil {
			return fmt.Errorf("failed to derive general agent ingress subject: %w", deriveErr)
		}
		destSubject = derived
	}

	// 7. Route: Send all attachments in a single payload to the workflow ingress (destSubject).
	totalAttachments := len(attachmentMetadata)
	enrichedAttachments := make([]map[string]interface{}, 0, totalAttachments)

	for _, attMeta := range attachmentMetadata {
		docURL := ""
		if s3Key, ok := attMeta["s3_key"].(string); ok && s3Key != "" && w.storage != nil {
			if url, err := w.storage.GeneratePresignedURL(ctx, s3Key, 24*time.Hour); err == nil {
				w.logger.Info("S3 URL", "url", url, "s3_key", s3Key)
				docURL = url
			} else {
				w.logger.Warn("failed to generate presigned url for attachment", "error", err, "s3_key", s3Key)
			}
		}
		enriched := map[string]interface{}{
			"name":           attMeta["name"],
			"document_url":   docURL,
			"content_type":   attMeta["content_type"],
			"content_length": attMeta["content_length"],
		}
		enrichedAttachments = append(enrichedAttachments, enriched)
	}

	firstDocURL := ""
	if len(enrichedAttachments) > 0 {
		if u, ok := enrichedAttachments[0]["document_url"].(string); ok {
			firstDocURL = u
		}
	}

	taskPayload := map[string]interface{}{
		"entity_id":         sender.EntityIDStr,
		"session_id":        sender.SessionID,
		"external_id":       payload.MessageID,
		"from_handle":       sender.FromHandle,
		"to_handle":         sender.ToHandle,
		"reply_to":          payload.ReplyTo,
		"document_url":      firstDocURL,
		"attachments":       enrichedAttachments,
		"total_attachments": totalAttachments,
		"subject":           payload.Subject,
		"body_text":         payload.TextBody,
		"body_html":         payload.HtmlBody,
		"agent_alias":       sender.AgentAlias,
	}

	packagedPayload, packErr := json.Marshal(taskPayload)
	if packErr != nil {
		w.logger.Error("failed to marshal structured payload", "error", packErr)
		return packErr
	}

	if w.nc != nil {
		if err := w.nc.Publish(destSubject, packagedPayload); err != nil {
			w.logger.Error("failed to publish task to workflow", "target", destSubject, "error", err)
			return err
		}
		w.logger.Info("PostmarkInboundEmailWorker: successfully routed event with attachments", "target", destSubject, "session", sender.SessionID, "total_attachments", totalAttachments)
	}

	return nil
}

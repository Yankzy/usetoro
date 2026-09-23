package workers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolvedSender captures the verified identity of an inbound email sender.
// It is populated once at the ingress boundary so every downstream NATS
// publish carries entity_id without re-resolving.
type ResolvedSender struct {
	EntityID    pgtype.UUID // database primary key
	EntityIDStr string      // hex-formatted UUID string
	FromHandle  string      // canonical sender email
	ToHandle    string      // canonical recipient email (OriginalRecipient or To)
	AgentAlias  string      // parsed alias from the recipient address (e.g. "rap_morocco")
	Subdomain   string      // parsed subdomain from the recipient address
	SessionID   string      // conversation session UUID (populated after session resolution)
}

// ResolveSender wraps recipient-based entity resolution, sender authentication,
// and hierarchical authorization into a single call.
// It returns a populated ResolvedSender or an error when the sender cannot be
// matched or authorized for the target entity (the caller should then Bounce).
func ResolveSender(
	ctx context.Context,
	logger *slog.Logger,
	db *database.Queries,
	pool *pgxpool.Pool,
	from string,
	recipient string,
	inReplyTo string,
) (ResolvedSender, error) {
	agentAlias, subdomain := ParseAgentEmail(recipient)

	if db == nil {
		return ResolvedSender{
			FromHandle: from,
			ToHandle:   recipient,
			AgentAlias: agentAlias,
			Subdomain:  subdomain,
		}, fmt.Errorf("database not available")
	}

	// 1. Authenticate sender: sender must be a registered active user
	senderUserEntityID, err := db.GetEntityIDByEmail(ctx, from)
	if err != nil || !senderUserEntityID.Valid {
		return ResolvedSender{
			FromHandle: from,
			ToHandle:   recipient,
			AgentAlias: agentAlias,
			Subdomain:  subdomain,
		}, fmt.Errorf("unauthorized sender: %s is not a registered user", from)
	}

	// 2. Deterministically resolve target entity
	var targetEntityID pgtype.UUID

	// Precedence 1: In-Reply-To thread (reply-chain routing)
	if inReplyTo != "" {
		cleanID := CleanMessageID(inReplyTo)
		refSessionID, err := db.GetConversationByExternalID(ctx, cleanID)
		if err != nil || !refSessionID.Valid {
			refSessionID, err = db.GetConversationByExternalID(ctx, inReplyTo)
		}
		if err == nil && refSessionID.Valid {
			sess, err := db.GetConversationSession(ctx, refSessionID)
			if err == nil && sess.EntityID.Valid {
				targetEntityID = sess.EntityID
			}
		}

		// ASE DAG Lookup fallback
		if !targetEntityID.Valid && strings.Contains(inReplyTo, "<ase_") && pool != nil {
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
			if aseNodeID != "" {
				var createdBy pgtype.UUID
				err := pool.QueryRow(ctx, `
					SELECT s.created_by 
					FROM fignode.staging_transactions t 
					JOIN fignode.staging_sessions s ON t.session_id = s.id 
					WHERE t.id = $1
				`, aseNodeID).Scan(&createdBy)
				if err == nil && createdBy.Valid {
					targetEntityID = createdBy
				}
			}
		}
	}

	// Precedence 2: Deterministic Recipient-based routing (<entity>@<domain> or <alias>@<entity>.<domain>)
	if !targetEntityID.Valid {
		var candidateSubdomain pgtype.UUID
		var candidateAlias pgtype.UUID

		// Check subdomain (ignoring generic domains)
		sub := strings.ToLower(strings.TrimSpace(subdomain))
		if sub != "" && sub != "inbound" && sub != "mail" && sub != "api" && sub != "gateway" && sub != "usetoro" {
			if id, err := db.GetEntityBySubdomain(ctx, sub); err == nil && id.Valid {
				candidateSubdomain = id
			}
		}

		// Check agent alias
		alias := strings.ToLower(strings.TrimSpace(agentAlias))
		if alias != "" && alias != "inbox" && alias != "mail" && alias != "api" && alias != "support" {
			if id, err := db.GetEntityBySubdomain(ctx, alias); err == nil && id.Valid {
				candidateAlias = id
			}
		}

		if candidateSubdomain.Valid && candidateAlias.Valid {
			if candidateSubdomain != candidateAlias {
				return ResolvedSender{
					FromHandle: from,
					ToHandle:   recipient,
					AgentAlias: agentAlias,
					Subdomain:  subdomain,
				}, fmt.Errorf("ambiguous recipient: subdomain and alias resolve to different entities")
			}
			targetEntityID = candidateSubdomain
		} else if candidateSubdomain.Valid {
			targetEntityID = candidateSubdomain
		} else if candidateAlias.Valid {
			targetEntityID = candidateAlias
		} else {
			// Fallback to the sender's own entity if no specific entity was targeted
			targetEntityID = senderUserEntityID
		}
	}

	// 3. Sender Authorization check
	isAuthorized := false
	if senderUserEntityID == targetEntityID {
		isAuthorized = true
	} else {
		descendants, err := db.GetEntityDescendants(ctx, senderUserEntityID)
		if err == nil {
			for _, desc := range descendants {
				if desc.Valid && desc == targetEntityID {
					isAuthorized = true
					break
				}
			}
		}
	}

	if !isAuthorized {
		return ResolvedSender{
			FromHandle: from,
			ToHandle:   recipient,
			AgentAlias: agentAlias,
			Subdomain:  subdomain,
		}, fmt.Errorf("sender %s is not authorized for entity %s", from, uuidFromPG(targetEntityID))
	}

	return ResolvedSender{
		EntityID:    targetEntityID,
		EntityIDStr: uuidFromPG(targetEntityID),
		FromHandle:  from,
		ToHandle:    recipient,
		AgentAlias:  agentAlias,
		Subdomain:   subdomain,
	}, nil
}

// Bounce logs an unknown-sender event and delegates to SendBounceReply.
// It always returns nil so callers can write: return Bounce(...)
func Bounce(ctx context.Context, logger *slog.Logger, cfg *config.Config, from, body, subject string) error {
	logger.Warn("Bounce: unknown sender, dropping message", "from", from, "subject", subject)
	SendBounceReply(ctx, http.DefaultClient, cfg, logger, from, body, subject)
	return nil
}

// CleanMessageID strips brackets and domain from an SMTP Message-ID
// so it can be matched against the Postmark MessageID stored in the DB.
func CleanMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	if idx := strings.Index(id, "@"); idx >= 0 {
		id = id[:idx]
	}
	return id
}

// ResolveEntityID attempts to resolve the entity ID for an inbound email using a waterfall approach:
// 1. Checks if In-Reply-To references an existing conversation.
// 2. Checks if In-Reply-To references an ASE DAG node.
// 3. Checks recent conversations by the sender's handle.
// 4. Looks up the sender as a registered user.
func ResolveEntityID(ctx context.Context, logger *slog.Logger, db *database.Queries, pool *pgxpool.Pool, from string, inReplyTo string) pgtype.UUID {
	var entityID pgtype.UUID

	// Priority 1: In-Reply-To
	if inReplyTo != "" {
		// Try to find the entity from the referenced conversation's session
		cleanID := CleanMessageID(inReplyTo)
		refSessionID, err := db.GetConversationByExternalID(ctx, cleanID)

		if err != nil || !refSessionID.Valid {
			refSessionID, err = db.GetConversationByExternalID(ctx, inReplyTo)
		}

		if err == nil && refSessionID.Valid {
			sess, err := db.GetConversationSession(ctx, refSessionID)
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

			if aseNodeID != "" && pool != nil {
				var createdBy pgtype.UUID
				err := pool.QueryRow(ctx, `
					SELECT s.created_by 
					FROM fignode.staging_transactions t 
					JOIN fignode.staging_sessions s ON t.session_id = s.id 
					WHERE t.id = $1
				`, aseNodeID).Scan(&createdBy)
				if err == nil {
					entityID = createdBy
					logger.Info("resolved entity_id from ASE transaction", "entity_id", entityID, "ase_node_id", aseNodeID)
				} else {
					logger.Warn("failed to resolve entity_id from ASE transaction", "error", err, "ase_node_id", aseNodeID)
				}
			}
		}
	}

	// Priority 2: Recent conversations
	if !entityID.Valid && from != "" {
		recentConvs, err := db.GetRecentConversationsByHandle(ctx, database.GetRecentConversationsByHandleParams{
			FromHandle: from,
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
	if !entityID.Valid && from != "" {
		id, err := db.GetEntityIDByEmail(ctx, from)
		if err == nil {
			entityID = id
		}
	}

	return entityID
}

// CheckBridgedSlackThread checks if the email is a reply to a bridged Slack thread.
// It uses the In-Reply-To header to find a matching mapping in the database.
func CheckBridgedSlackThread(ctx context.Context, db *database.Queries, inReplyTo string) (database.ToroCoreToroThreadsMapping, bool) {
	if inReplyTo == "" {
		return database.ToroCoreToroThreadsMapping{}, false
	}

	cleanID := CleanMessageID(inReplyTo)
	mapping, mappingErr := db.GetThreadMappingByEmailMessageID(ctx, cleanID)

	if mappingErr != nil || mapping.SlackChannelID == "" {
		mapping, mappingErr = db.GetThreadMappingByEmailMessageID(ctx, inReplyTo)
	}

	if mappingErr == nil && mapping.SlackChannelID != "" {
		return mapping, true
	}

	return database.ToroCoreToroThreadsMapping{}, false
}

// UpdateSlackThreadEmailMessageID advances the email pointer for a bridged Slack thread
// so the next reply cycle can find the mapping.
func UpdateSlackThreadEmailMessageID(ctx context.Context, db *database.Queries, messageID string, channelID string, threadTs string) error {
	return db.UpdateThreadMappingEmailMessageID(ctx, database.UpdateThreadMappingEmailMessageIDParams{
		EmailLatestMessageID: messageID,
		SlackChannelID:       channelID,
		SlackParentTs:        threadTs,
	})
}

// PostmarkHeader represents a single header in the Postmark JSON payload.
type PostmarkHeader struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// ExtractMessageHeaders finds the In-Reply-To and Message-ID headers from a Postmark payload.
func ExtractMessageHeaders(headers []PostmarkHeader) (inReplyTo, messageID string) {
	for _, header := range headers {
		if strings.EqualFold(header.Name, "In-Reply-To") {
			inReplyTo = header.Value
		} else if strings.EqualFold(header.Name, "Message-ID") {
			messageID = header.Value
		}
	}
	return inReplyTo, messageID
}

// ProcessBridgedSlackThread processes an inbound email to see if it belongs to a bridged Slack thread.
// It returns the serialized Envelope to publish to `proof.outgoing.chat` if it is a bridged thread.
func ProcessBridgedSlackThread(
	ctx context.Context, logger *slog.Logger, db *database.Queries, pool *pgxpool.Pool, payload *PostmarkInboundEmail,
) ([]byte, bool, error) {
	inReplyTo, smtpMessageID := ExtractMessageHeaders(payload.Headers)
	if smtpMessageID == "" {
		logger.Warn("Message-ID header not found in inbound API payload, falling back to Postmark internal MessageID",
			"postmark_message_id", payload.MessageID,
			"from", payload.From,
		)
		smtpMessageID = payload.MessageID
	}

	mapping, isBridged := CheckBridgedSlackThread(ctx, db, inReplyTo)
	if !isBridged {
		return nil, false, nil
	}

	logger.Info("email follow-up to bridged slack thread",
		"slack_channel", mapping.SlackChannelID,
		"slack_thread_ts", mapping.SlackParentTs,
		"in_reply_to", inReplyTo,
	)

	// We need to resolve the entity ID to properly route the proof
	entityID := ResolveEntityID(ctx, logger, db, pool, payload.From, inReplyTo)

	promptText := payload.StrippedTextReply
	if promptText == "" {
		promptText = payload.TextBody
	}

	outProof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data: func(v interface{}) json.RawMessage {
			b, _ := json.Marshal(v)
			return b
		}(map[string]interface{}{
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

	// Advance the email pointer so the next reply cycle can find the mapping.
	_ = UpdateSlackThreadEmailMessageID(ctx, db, smtpMessageID, mapping.SlackChannelID, mapping.SlackParentTs)

	return outgoingBytes, true, nil
}

// SendBounceReply sends a direct reply via Postmark when the inbound message
// cannot be matched to a known entity. This avoids wasting LLM tokens on unresolvable messages.
func SendBounceReply(ctx context.Context, client *http.Client, cfg *config.Config, logger *slog.Logger, to, originalBody, originalSubject string) {
	// We wont reply to spam emails
	logger.Warn("Unknown user emailed us, No replies will be sent")

	// Uncomment to actually send bounce replies
	/*
		if cfg.PostmarkServerToken == "" {
			logger.Warn("cannot send bounce reply: postmark token not configured")
			return
		}

		subject := "Unable to process your message"
		if originalSubject != "" {
			subject = fmt.Sprintf("Re: %s", originalSubject)
		}

		body := fmt.Sprintf(`The recipient to your message below could not be resolved. Please double check.

		---
		%s
		---

		Do not reply to this email.`, originalBody)

		payload := map[string]interface{}{
			"From":          "do-not-reply@usetoro.io",
			"To":            to,
			"Subject":       subject,
			"TextBody":      body,
			"MessageStream": "outbound",
		}

		jsonPayload, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.postmarkapp.com/email", bytes.NewBuffer(jsonPayload))
		if err != nil {
			logger.Error("bounce reply: failed to create request", "error", err)
			return
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Postmark-Server-Token", cfg.PostmarkServerToken)

		resp, err := client.Do(req)
		if err != nil {
			logger.Error("bounce reply: failed to send", "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var errResp map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&errResp)
			logger.Error("bounce reply: postmark API error",
				"status", resp.StatusCode,
				"error", errResp,
			)
			return
		}

		logger.Info("bounce reply sent", "to", to)
	*/
}

// ParseAgentEmail splits an agent email address into its routing components.
// "mark@a.usetoro.io" → alias="mark", subdomain="a"
func ParseAgentEmail(email string) (alias, subdomain string) {
	// Strip name prefix if present: "Mark Smith <mark@a.usetoro.io>"
	email = strings.TrimSpace(email)
	if idx := strings.LastIndex(email, "<"); idx >= 0 {
		email = strings.TrimSuffix(strings.TrimSpace(email[idx+1:]), ">")
	}

	parts := strings.SplitN(email, "@", 2)
	// Fallback: If the string isn't a fully qualified email address (no '@' symbol),
	// check if it's just the raw name of a configured virtual employee.
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

// ExportToCSV generates a CSV byte slice from headers and rows.
func ExportToCSV(headers []string, rows [][]string, delimiter rune) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if delimiter != 0 {
		writer.Comma = delimiter
	}

	if len(headers) > 0 {
		if err := writer.Write(headers); err != nil {
			return nil, err
		}
	}

	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ExportToPNM generates a PNM byte slice (Sage format) from headers and rows.
// This delegates to ExportToCSV but can be extended for PNM-specific formatting.
func ExportToPNM(headers []string, rows [][]string, delimiter rune) ([]byte, error) {
	return ExportToCSV(headers, rows, delimiter)
}

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

package workers

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/smtp"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/infra/crypto"
	"github.com/Yankzy/usetoro/internal/database"
)

type EmailDispatchWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
	aesKey []byte
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewEmailDispatchWorker(deps.Store.Queries, deps.Queue, deps.Logger, []byte(deps.Config.EncryptionKey)), nil
	})
}

func NewEmailDispatchWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger, aesKey []byte) *EmailDispatchWorker {
	return &EmailDispatchWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "email_dispatch"),
		aesKey: aesKey,
	}
}

func (w *EmailDispatchWorker) Init(ctx context.Context) error {
	return nil
}

func (w *EmailDispatchWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "ase.events.email.pipeline.dispatch",
			Group:   "email-dispatch-worker",
		},
	}
}

func (w *EmailDispatchWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event EmailDispatchEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("failed to unmarshal dispatch event", "error", err)
		return nil // Drop invalid payload
	}

	w.logger.Info("dispatching email", "prospect_id", event.ProspectID, "account_id", event.AccountID)

	// Fetch Account details
	account, err := w.db.GetEmailAccountByID(ctx, pgtype.UUID{Bytes: event.AccountID, Valid: true})
	if err != nil {
		w.logger.Error("failed to fetch email account by ID", "account_id", event.AccountID, "error", err)
		return err // Retry later
	}

	// Decrypt password
	password, err := crypto.Decrypt(string(w.aesKey), account.EncryptedPassword)
	if err != nil {
		w.logger.Error("failed to decrypt email account password", "account", account.Email, "error", err)
		return nil
	}

	// Construct MIME payload with RFC 8058 One-Click Unsubscribe
	// Note: We use a generic List-Unsubscribe URL for now, which should ideally point to the tenant's domain
	unsubURL := fmt.Sprintf("https://api.usetoro.io/api/v1/marketing/unsubscribe?p=%s&c=%s", 
		fmt.Sprintf("%x", event.ProspectID), 
		fmt.Sprintf("%x", event.CampaignID),
	)
	
	var mime strings.Builder
	mime.WriteString(fmt.Sprintf("From: %s\r\n", account.Email))
	mime.WriteString(fmt.Sprintf("To: %s\r\n", event.ToEmail))
	mime.WriteString(fmt.Sprintf("Subject: %s\r\n", event.Subject))
	mime.WriteString("MIME-Version: 1.0\r\n")
	mime.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	mime.WriteString(fmt.Sprintf("List-Unsubscribe: <%s>\r\n", unsubURL))
	mime.WriteString("List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
	if event.ThreadMessageID != "" {
		mime.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", event.ThreadMessageID))
		mime.WriteString(fmt.Sprintf("References: %s\r\n", event.ThreadMessageID))
	}
	mime.WriteString("\r\n")

	proxyURL := config.GetGlobal().TrackingProxyURL
	if proxyURL == "" {
		proxyURL = "http://localhost:8080"
	}
	proxyURL = strings.TrimRight(proxyURL, "/")

	pStr := fmt.Sprintf("%x", event.ProspectID)
	cStr := fmt.Sprintf("%x", event.CampaignID)
	// We don't have list_id on the event yet, so we leave l empty or could add it to Event if we wanted

	// Open tracking pixel
	trackingPixel := fmt.Sprintf(`<img src="%s/api/track/open?p=%s&c=%s" width="1" height="1" alt="" />`, proxyURL, pStr, cStr)

	// Click tracking (link rewriting)
	linkRegex := regexp.MustCompile(`href=["'](http[^"']+)["']`)
	rewrittenHTML := linkRegex.ReplaceAllStringFunc(event.BodyHTML, func(match string) string {
		parts := strings.SplitN(match, "\"", 3)
		if len(parts) == 3 {
			targetURL := parts[1]
			b64URL := base64.URLEncoding.EncodeToString([]byte(targetURL))
			return fmt.Sprintf(`href="%s/api/track/click?p=%s&c=%s&url=%s"`, proxyURL, pStr, cStr, b64URL)
		} else {
			parts = strings.SplitN(match, "'", 3)
			if len(parts) == 3 {
				targetURL := parts[1]
				b64URL := base64.URLEncoding.EncodeToString([]byte(targetURL))
				return fmt.Sprintf(`href='%s/api/track/click?p=%s&c=%s&url=%s'`, proxyURL, pStr, cStr, b64URL)
			}
		}
		return match
	})

	mime.WriteString(rewrittenHTML)
	mime.WriteString(trackingPixel)

	// Dispatch via SMTP
	// Determine host dynamically (simplified for this exercise to use Gmail/Office365 defaults)
	smtpHost := "smtp.gmail.com"
	smtpPort := "587"
	if strings.Contains(account.Email, "outlook.com") || strings.Contains(account.Email, "hotmail.com") {
		smtpHost = "smtp.office365.com"
	}

	auth := smtp.PlainAuth("", account.Email, string(password), smtpHost)
	
	// Create a custom tls.Config to ensure STARTTLS upgrades correctly
	tlsConfig := &tls.Config{
		ServerName: smtpHost,
	}

	// Use standard net/smtp SendMail which handles EHLO, STARTTLS (if supported), AUTH, and DATA
	// SendMail automatically upgrades to TLS if STARTTLS is supported.
	err = sendMail(smtpHost+":"+smtpPort, auth, account.Email, []string{event.ToEmail}, []byte(mime.String()), tlsConfig)
	if err != nil {
		w.logger.Error("failed to dispatch email via SMTP", "host", smtpHost, "error", err)
		return err // NATS retry
	}

	// Cleanup & Metrics
	_ = w.db.IncrementEmailAccountSendCount(ctx, account.ID)
	
	meta, _ := json.Marshal(map[string]string{"smtp_host": smtpHost})
	_ = w.db.LogEmailEvent(ctx, database.LogEmailEventParams{
		ProspectID: pgtype.UUID{Bytes: event.ProspectID, Valid: true},
		CampaignID: pgtype.UUID{Bytes: event.CampaignID, Valid: true},
		ListID:     pgtype.UUID{Valid: false},
		NatsMsgID:  pgtype.Text{String: msg.Subject, Valid: true},
		EventType:  "sent",
		Metadata:   meta,
		UserAgent:  pgtype.Text{Valid: false},
		IpAddress:  pgtype.Text{Valid: false},
		IsHuman:    pgtype.Bool{Bool: true, Valid: true},
	})

	w.logger.Info("email dispatched successfully", "to", event.ToEmail)
	return nil
}

// sendMail is a custom implementation wrapper around smtp.SendMail that explicitly sets TLS config
func sendMail(addr string, a smtp.Auth, from string, to []string, msg []byte, tlsConfig *tls.Config) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err = c.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	
	if a != nil {
		if err = c.Auth(a); err != nil {
			return err
		}
	}
	
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return err
		}
	}
	
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	
	err = w.Close()
	if err != nil {
		return err
	}
	return c.Quit()
}

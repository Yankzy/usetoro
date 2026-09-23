package workers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/smtp"
	"strings"

	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/crypto"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
)

type WarmupContentWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
	llm    *agent.Runtime
	aesKey []byte
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		llmRuntime := agent.NewRuntime(deps.Logger, nil, core.AgentConfig{
			DID:   "warmup_content_generator",
			Model: "gpt-5.4-mini",
			SystemPrompt: `You are an AI warmup engine for email infrastructure.
Generate a casual, plain-text email with a max of 50 words.
Use spintax format {option1|option2|option3} for every 2-3 words to ensure uniqueness.
STRICT RULES:
- NO LINKS
- NO FINANCIAL LANGUAGE
- NO TRACKING PIXELS
- Output ONLY a JSON object with two keys: "subject" and "body"

Example:
{"subject": "{Hello|Hi|Greetings} {there|friend}", "body": "{Hope you are doing well|How are you|I hope this finds you well}. {Just wanted to|I am writing to} {check in|say hello}."}
`,
		})

		return NewWarmupContentWorker(deps.Store.Queries, deps.Queue, deps.Logger, llmRuntime, []byte(deps.Config.EncryptionKey)), nil
	})
}

func NewWarmupContentWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger, llm *agent.Runtime, aesKey []byte) *WarmupContentWorker {
	return &WarmupContentWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "warmup_content"),
		llm:    llm,
		aesKey: aesKey,
	}
}

func (w *WarmupContentWorker) Init(ctx context.Context) error {
	return nil
}

func (w *WarmupContentWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "email.warmup.generate",
			Group:   "warmup-content-group-v2",
		},
	}
}

type WarmupContent struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (w *WarmupContentWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event WarmupDispatchEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("failed to unmarshal warmup event", "error", err)
		return nil
	}

	account, err := w.db.GetEmailAccountByID(ctx, pgtype.UUID{Bytes: event.FromAccountID, Valid: true})
	if err != nil {
		w.logger.Error("failed to get account", "error", err)
		return nil
	}

	llmResp, err := w.llm.Exec(ctx, "Generate a warmup email.", "")
	if err != nil {
		w.logger.Error("llm execution failed", "error", err)
		return err // Retryable
	}

	var content WarmupContent
	if err := json.Unmarshal([]byte(llmResp), &content); err != nil {
		w.logger.Error("failed to parse llm json", "error", err, "raw", llmResp)
		return nil
	}

	password, err := crypto.Decrypt(account.EncryptedPassword, string(w.aesKey))
	if err != nil {
		w.logger.Error("failed to decrypt password", "error", err)
		return nil
	}

	var mime strings.Builder
	mime.WriteString(fmt.Sprintf("From: %s\r\n", account.Email))
	mime.WriteString(fmt.Sprintf("To: %s\r\n", event.ToEmail))
	mime.WriteString(fmt.Sprintf("Subject: %s\r\n", content.Subject))
	mime.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	mime.WriteString("\r\n")
	mime.WriteString(content.Body)

	smtpHost := "smtp.gmail.com"
	smtpPort := "587"
	if strings.Contains(account.Email, "outlook.com") || strings.Contains(account.Email, "hotmail.com") {
		smtpHost = "smtp.office365.com"
	}

	auth := smtp.PlainAuth("", account.Email, string(password), smtpHost)
	tlsConfig := &tls.Config{ServerName: smtpHost}

	err = sendSmtpMail(smtpHost+":"+smtpPort, auth, account.Email, []string{event.ToEmail}, []byte(mime.String()), tlsConfig)
	if err != nil {
		w.logger.Error("failed to dispatch warmup email via SMTP", "host", smtpHost, "error", err)
		return err // NATS retry
	}

	_ = w.db.IncrementEmailAccountSendCount(ctx, account.ID)
	w.logger.Info("warmup email dispatched successfully", "to", event.ToEmail)
	return nil
}

func sendSmtpMail(addr string, a smtp.Auth, from string, to []string, msg []byte, tlsConfig *tls.Config) error {
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

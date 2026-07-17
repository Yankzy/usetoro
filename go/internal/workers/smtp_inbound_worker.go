package workers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/mail"
	"strings"

	"github.com/nats-io/nats.go"
)

type SmtpInboundWorker struct {
	nc       *nats.Conn
	logger   *slog.Logger
	listener net.Listener
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewSmtpInboundWorker(deps.Queue, deps.Logger), nil
	})
}

func NewSmtpInboundWorker(nc *nats.Conn, logger *slog.Logger) *SmtpInboundWorker {
	return &SmtpInboundWorker{
		nc:     nc,
		logger: logger.With("worker", "smtp_inbound"),
	}
}

func (w *SmtpInboundWorker) Init(ctx context.Context) error {
	// Start a lightweight SMTP server on port 2525
	l, err := net.Listen("tcp", ":2525")
	if err != nil {
		w.logger.Error("failed to start SMTP server", "error", err)
		return err
	}
	w.listener = l
	w.logger.Info("SMTP inbound server listening on :2525")

	go w.serve()
	return nil
}

func (w *SmtpInboundWorker) Subscriptions() []SubscriptionConfig {
	return nil
}

func (w *SmtpInboundWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	return nil
}

func (w *SmtpInboundWorker) serve() {
	for {
		conn, err := w.listener.Accept()
		if err != nil {
			// Check if listener was closed
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			w.logger.Error("failed to accept SMTP connection", "error", err)
			continue
		}
		go w.handleConnection(conn)
	}
}

func (w *SmtpInboundWorker) handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	send := func(msg string) {
		fmt.Fprint(writer, msg+"\r\n")
		writer.Flush()
	}

	send("220 usetoro.io SMTP Server")

	var rawData strings.Builder
	readingData := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)

		if readingData {
			if line == "." {
				readingData = false
				send("250 OK: queued as 12345")
				w.processEmailData(rawData.String())
				continue
			}
			rawData.WriteString(line)
			rawData.WriteString("\n")
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])

		switch cmd {
		case "HELO", "EHLO":
			send("250 Hello")
		case "MAIL":
			send("250 OK")
		case "RCPT":
			send("250 OK")
		case "DATA":
			send("354 End data with <CR><LF>.<CR><LF>")
			readingData = true
			rawData.Reset()
		case "QUIT":
			send("221 Bye")
			return
		default:
			send("500 Command unrecognized")
		}
	}
}

func (w *SmtpInboundWorker) processEmailData(data string) {
	msg, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		w.logger.Error("failed to parse incoming email via SMTP", "error", err)
		return
	}

	from := msg.Header.Get("From")
	subject := msg.Header.Get("Subject")

	w.logger.Info("received inbound email via SMTP", "from", from, "subject", subject)

	// In a real implementation we would parse multipart MIME to extract text.
	// For simplicity, we assume the body is readable directly or we extract the first part.

	bodyBytes, _ := io.ReadAll(msg.Body)
	body := string(bodyBytes)

	// Extract prospect ID from "In-Reply-To" or "References" or by looking up the sender email
	// For this phase, we assume the system maps it via the sender email.

	// Create NATS event
	event := InboundReplyEvent{
		From:       from,
		Body:       body,
		ProspectID: "", // To be resolved by sentiment worker or we publish by email
	}

	payload, _ := json.Marshal(event)

	natsMsg := nats.NewMsg("email.inbound.reply.parsed")
	natsMsg.Data = payload

	if err := w.nc.PublishMsg(natsMsg); err != nil {
		w.logger.Error("failed to publish inbound reply to NATS", "error", err)
	}
}

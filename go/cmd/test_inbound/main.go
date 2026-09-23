package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/internal/workers"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dbURL := "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
	natsURL := "nats://127.0.0.1:4222"

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("failed to connect to DB", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	nc, err := nats.Connect(natsURL)
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	cfg, _, _ := config.Load()
	if cfg == nil {
		cfg = &config.Config{}
	}
	if cfg.AWSAccessKeyID == "" {
		cfg.AWSAccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if cfg.AWSSecretAccessKey == "" {
		cfg.AWSSecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if cfg.AWSS3BucketName == "" {
		cfg.AWSS3BucketName = os.Getenv("AWS_S3_BUCKET_NAME")
	}
	if cfg.AWSS3RegionName == "" {
		cfg.AWSS3RegionName = os.Getenv("AWS_S3_REGION_NAME")
	}

	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20,
		BufferItems: 64,
	})

	st, err := store.NewStore(pool, cache, cfg.EncryptionKey)
	if err != nil {
		logger.Error("failed to init store", "error", err)
		os.Exit(1)
	}

	workerDeps := workers.Dependencies{
		Logger: logger,
		Config: cfg,
		Queue:  nc,
		Store:  st,
		DBPool: pool,
	}

	workerManager := workers.NewManager(logger, nc)
	if err := workerManager.LoadFromRegistry(workerDeps); err != nil {
		logger.Error("failed to load database workers from registry", "error", err)
		os.Exit(1)
	}

	pdfPath := "/Users/Yankz/.gemini/antigravity-ide/brain/16ef9a7c-0be5-49f8-aaf9-30549b97d7ce/scratch/synthetic_bank_statement_live.pdf"
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		logger.Error("failed to read PDF", "error", err)
		os.Exit(1)
	}

	msgID := fmt.Sprintf("direct-test-%d", time.Now().Unix())
	payload := map[string]any{
		"FromName":      "Yankz Kuyateh",
		"MessageStream": "inbound",
		"From":          "yankz@fignode.com",
		"FromFull": map[string]any{
			"Email": "yankz@fignode.com",
			"Name":  "Yankz Kuyateh",
		},
		"To":                "accounting@toro-synthetic-bookkeeping.inbound.usetoro.io",
		"OriginalRecipient": "accounting@toro-synthetic-bookkeeping.inbound.usetoro.io",
		"Subject":           "Releve Bancaire Direct Test",
		"MessageID":         msgID,
		"Date":              time.Now().Format(time.RFC1123Z),
		"TextBody":          "Releve pour test direct",
		"Attachments": []map[string]any{
			{
				"Name":          "releve_bancaire_live.pdf",
				"ContentType":   "application/pdf",
				"ContentLength": len(pdfBytes),
				"Content":       base64.StdEncoding.EncodeToString(pdfBytes),
			},
		},
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := nats.NewMsg(core.PostmarkInboundEmailSubject)
	msg.Data = payloadBytes

	// Find the PostmarkInboundEmailWorker and DocumentCDCWorker
	var targetWorker workers.Worker
	var cdcWorker workers.Worker
	for _, w := range workerManager.Workers() {
		for _, sub := range w.Subscriptions() {
			if sub.Subject == core.PostmarkInboundEmailSubject {
				targetWorker = w
			}
			if strings.HasPrefix(sub.Subject, "ledger.toro_core.documents") {
				cdcWorker = w
			}
		}
	}

	if targetWorker == nil {
		logger.Error("could not find PostmarkInboundEmailWorker in worker manager")
		os.Exit(1)
	}

	logger.Info("Executing targetWorker.Handle directly...")
	if err := targetWorker.Handle(ctx, msg); err != nil {
		logger.Error("worker.Handle returned error", "error", err)
		os.Exit(1)
	}
	logger.Info("✅ targetWorker.Handle returned nil (success)!")

	var docID, s3URL string
	err = pool.QueryRow(ctx, `
		SELECT id::text, s3_url 
		FROM toro_core.documents 
		WHERE session_id IN (SELECT id::text FROM toro_core.conversation_sessions WHERE entity_id = 'd0e1a1a0-7080-4500-a000-000070705001')
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&docID, &s3URL)
	if err != nil {
		logger.Error("failed to find created document for CDC dispatch", "error", err)
		os.Exit(1)
	}
	logger.Info("Found newly created document for CDC dispatch", "doc_id", docID, "s3_url", s3URL)

	if cdcWorker != nil {
		cdcEvent := cdc.Event{
			EventID:   fmt.Sprintf("cdc-%s", docID),
			Table:     "toro_core.documents",
			Action:    "INSERT",
			Source:    "toro_internal",
			Timestamp: time.Now(),
			Data: map[string]any{
				"id":         docID,
				"file_name":  "releve_bancaire_live.pdf",
				"s3_url":     s3URL,
				"ocr_status": "PENDING",
			},
		}
		eventBytes, _ := json.Marshal(cdcEvent)
		cdcMsg := &nats.Msg{
			Subject: "ledger.toro_core.documents.insert",
			Data:    eventBytes,
		}
		logger.Info("Executing cdcWorker.Handle...")
		if err := cdcWorker.Handle(ctx, cdcMsg); err != nil {
			logger.Error("cdcWorker.Handle error", "error", err)
		} else {
			logger.Info("✅ cdcWorker.Handle returned nil (dispatched OCR to tasks.perception.1.ocr)!")
		}
	} else {
		logger.Warn("DocumentCDCWorker not found in worker manager")
	}
}

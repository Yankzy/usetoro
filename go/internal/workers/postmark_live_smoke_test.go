package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
	"github.com/Yankzy/usetoro/internal/infra"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

func TestPostmarkInboundEmailWorker_LiveExecution(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err, "failed to connect to DB")
	defer pool.Close()

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err, "failed to connect to NATS")
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

	storageSvc, err := infra.NewS3Service(cfg)
	require.NoError(t, err, "failed to create S3 service")

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w := &PostmarkInboundEmailWorker{
		db:      database.New(pool),
		pool:    pool,
		logger:  logger,
		cfg:     cfg,
		nc:      nc,
		storage: storageSvc,
	}

	pdfPath := "/Users/Yankz/.gemini/antigravity-ide/brain/16ef9a7c-0be5-49f8-aaf9-30549b97d7ce/scratch/synthetic_bank_statement_live.pdf"
	pdfBytes, err := os.ReadFile(pdfPath)
	require.NoError(t, err, "failed to read synthetic PDF")

	msgID := fmt.Sprintf("live-smoke-%d", time.Now().UnixNano())
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
		"Subject":           "Releve Bancaire Live Ingestion Test",
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

	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	msg := nats.NewMsg(core.PostmarkInboundEmailSubject)
	msg.Data = payloadBytes

	t.Logf("Dispatching Postmark inbound email to worker.Handle directly (MessageID: %s)...", msgID)
	err = w.Handle(ctx, msg)
	require.NoError(t, err, "worker.Handle should succeed without error")

	t.Logf("✅ worker.Handle returned successfully! Checking toro_core.documents...")

	var docID string
	var fileName string
	var ocrStatus string
	err = pool.QueryRow(ctx, `
		SELECT id::text, file_name, ocr_status 
		FROM toro_core.documents 
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&docID, &fileName, &ocrStatus)

	require.NoError(t, err, "expected document to be present in toro_core.documents")
	t.Logf("📄 Document found in DB: id=%s, file_name=%s, status=%s", docID, fileName, ocrStatus)
}

func TestOCRResultWorker_LiveExecution(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream()
	require.NoError(t, err)

	natsMsg, err := js.GetMsg("WORKFLOWS", 186)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ocrWorker := &OCRResultWorker{
		db:     database.New(pool),
		pool:   pool,
		logger: logger,
		nc:     nc,
	}

	msg := &nats.Msg{
		Subject: natsMsg.Subject,
		Data:    natsMsg.Data,
	}

	t.Logf("Executing ocrWorker.Handle directly...")
	err = ocrWorker.Handle(ctx, msg)
	require.NoError(t, err)

	var ocrStatus string
	err = pool.QueryRow(ctx, "SELECT ocr_status FROM toro_core.documents WHERE id = '404ba4bd-4e7c-4ace-8520-8665eaff93f9'").Scan(&ocrStatus)
	require.NoError(t, err)
	t.Logf("📄 Document ocr_status after Handle: %s", ocrStatus)
}

func TestGetDocumentByID_Debug(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	docStore := knowledge_system.NewDocumentStore(pool, logger)

	docUUID := uuid.MustParse("267d1760-a574-40bb-b8f1-33db41fc0e5d")
	doc, err := docStore.GetDocumentByID(ctx, docUUID)
	require.NoError(t, err, "GetDocumentByID should not error")
	require.NotNil(t, doc, "doc should not be nil")
	t.Logf("✅ Successfully fetched doc: id=%s, s3_url=%s, meta_len=%d, metadata=%s", doc.ID, doc.S3URL, len(doc.Metadata), string(doc.Metadata))
}

func TestDocumentCDCWorker_LiveExecution(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@127.0.0.1:5435/toro?sslmode=disable"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
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

	storageSvc, err := infra.NewS3Service(cfg)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cdcWorker := &DocumentCDCWorker{
		db:      database.New(pool),
		pool:    pool,
		logger:  logger,
		cfg:     cfg,
		nc:      nc,
		storage: storageSvc,
	}

	// Reset doc status to PENDING
	_, err = pool.Exec(ctx, "UPDATE toro_core.documents SET ocr_status = 'PENDING' WHERE id = '267d1760-a574-40bb-b8f1-33db41fc0e5d'")
	require.NoError(t, err)

	cdcEvent := cdc.Event{
		EventID:   "cdc-test-1",
		Table:     "toro_core.documents",
		Action:    "INSERT",
		Source:    "toro_internal",
		Timestamp: time.Now(),
		Data: map[string]any{
			"id":         "267d1760-a574-40bb-b8f1-33db41fc0e5d",
			"file_name":  "releve_bancaire_live.pdf",
			"s3_url":     "inbound/06f801d7-156a-427d-be22-4764eeab5a8c/9d0df9b5-e86a-483a-86ae-932767d0d741-releve_bancaire_live.pdf",
			"ocr_status": "PENDING",
		},
	}
	eventBytes, _ := json.Marshal(cdcEvent)

	msg := &nats.Msg{
		Subject: "ledger.toro_core.documents.insert",
		Data:    eventBytes,
	}

	t.Logf("Dispatching to cdcWorker.Handle...")
	err = cdcWorker.Handle(ctx, msg)
	require.NoError(t, err)
	t.Logf("✅ cdcWorker.Handle returned successfully!")
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	appconfig "github.com/Yankzy/usetoro/internal/config"
)

const defaultSampleURL = "https://voxprofit.s3.us-east-1.amazonaws.com/2b6e9795-52e6-4dce-a113-59500e9ddc27-facture.pdf?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Checksum-Mode=ENABLED&X-Amz-Credential=AKIATK25RS5VOWJTG3QR%2F20260803%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260803T115650Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&x-id=GetObject&X-Amz-Signature=a4d0835a7675956ad957c2a2c888aebaab8cb39fcdb40f0f7167cda569496429"

const ocrSystemPrompt = `You are an expert OCR document parser. Analyze the document and extract structured JSON matching one of these exact types:

1. Bank Statement (doc_type: "bank_statement"):
   Extract 'bank_name', 'account_number', 'statement_date', 'period', 'starting_balance', 'ending_balance', and 'transactions' list containing items with 'date', 'description', 'amount', 'type' ('debit'/'credit').
   CRITICAL FOR BANK STATEMENTS:
   - Do NOT drop debit/credit, withdrawal/deposit, or polarity sign indicators ('minus', 'brackets', 'none').
   - Also return a 'column_mapping' object.

2. Invoice (doc_type: "invoice"):
   Extract 'vendor_name', 'invoice_number', 'date', 'total_mad', 'ht_mad', 'tva_mad', and 'line_items'.

3. Receipt (doc_type: "receipt"):
   Extract 'vendor_name', 'date', 'total_mad', and 'payment_method'.

4. Other (doc_type: "other"):
   Extract general key-value metadata.

Return ONLY a valid JSON object matching this structure:
{
  "doc_type": "bank_statement" | "invoice" | "receipt" | "other",
  "confidence": 0.95,
  "data": { ... extracted fields ... },
  "column_mapping": { ... optional column mapping for bank statements ... }
}`

func resignS3URL(ctx context.Context, logger *slog.Logger, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, err
	}

	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return rawURL, fmt.Errorf("invalid S3 key in URL path: %s", u.Path)
	}

	bucket := "voxprofit"
	if hostParts := strings.Split(u.Host, "."); len(hostParts) > 0 && hostParts[0] != "s3" {
		bucket = hostParts[0]
	}

	appCfg, _, err := appconfig.Load()
	if err != nil {
		logger.Warn("Error loading app config; skipping S3 URL re-signing", "error", err)
		return rawURL, nil
	}

	region := appCfg.AWSS3RegionName
	if region == "" {
		region = os.Getenv("AWS_S3_REGION_NAME")
	}
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	accessKey := appCfg.AWSAccessKeyID
	if accessKey == "" {
		accessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	secretKey := appCfg.AWSSecretAccessKey
	if secretKey == "" {
		secretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	
	if accessKey == "" || secretKey == "" {
		logger.Warn("AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY not set in app config or environment; skipping S3 URL re-signing", "key", key)
		return rawURL, nil
	}

	logger.Info("🔑 Re-signing expired S3 URL...", "bucket", bucket, "key", key, "region", region)

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return rawURL, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)
	presignClient := s3.NewPresignClient(s3Client)

	presignedReq, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = 24 * time.Hour
	})
	if err != nil {
		return rawURL, fmt.Errorf("failed to presign S3 object: %w", err)
	}

	logger.Info("✅ Successfully generated fresh 24h presigned S3 URL", "key", key)
	return presignedReq.URL, nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	_, _, err := appconfig.Load()
	if err != nil {
		logger.Warn("Error loading app config", "error", err)
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		logger.Error("OPENAI_API_KEY environment variable is missing!")
		os.Exit(1)
	}

	targetURL := defaultSampleURL
	if len(os.Args) > 1 && os.Args[1] != "" {
		targetURL = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	logger.Info("📑 Target Document URL", "url", targetURL)

	// Re-sign S3 URL if signature is expired and credentials exist
	activeURL, err := resignS3URL(ctx, logger, targetURL)
	if err != nil {
		logger.Warn("S3 re-signing error, attempting using raw URL", "error", err)
		activeURL = targetURL
	}

	logger.Info("🚀 Initializing TAP Agent Runtime...", "model", "gpt-5.4")

	cfg := core.AgentConfig{
		DID:          "did:toro:test-ocr-cli",
		Model:        "gpt-5.4",
		SystemPrompt: ocrSystemPrompt,
	}
	rt := agent.NewRuntime(logger, nil, cfg)

	userPrompt := "Document Name: 2b6e9795-52e6-4dce-a113-59500e9ddc27-facture.pdf\nPlease perform visual OCR inspection on the attached document and extract structured JSON matching the system instructions."

	logger.Info("🧠 Executing visual OCR via ExecDocumentURL...")
	startTime := time.Now()

	rawResult, err := rt.ExecDocumentURL(ctx, userPrompt, ocrSystemPrompt, activeURL, "2b6e9795-52e6-4dce-a113-59500e9ddc27-facture.pdf")
	if err != nil {
		logger.Error("❌ Visual OCR execution failed", "error", err)
		os.Exit(1)
	}

	duration := time.Since(startTime)
	logger.Info("✅ Visual OCR completed successfully!", "elapsed", duration.String())

	// Format output JSON
	firstIdx := strings.Index(rawResult, "{")
	lastIdx := strings.LastIndex(rawResult, "}")
	cleanJSON := rawResult
	if firstIdx != -1 && lastIdx != -1 && lastIdx > firstIdx {
		cleanJSON = rawResult[firstIdx : lastIdx+1]
	}

	var prettyJSON map[string]interface{}
	if err := json.Unmarshal([]byte(cleanJSON), &prettyJSON); err == nil {
		formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
		fmt.Println("\n=================== STRUCTURED OCR OUTPUT ===================")
		fmt.Println(string(formatted))
		fmt.Println("=============================================================")
	} else {
		fmt.Println("\n=================== RAW OCR OUTPUT ===================")
		fmt.Println(rawResult)
		fmt.Println("=======================================================")
	}
}

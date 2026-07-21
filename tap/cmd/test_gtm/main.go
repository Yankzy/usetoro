package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/infra/crypto"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

// CampaignTriggerEvent represents a request to execute a campaign step.
type CampaignTriggerEvent struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	ProspectID uuid.UUID `json:"prospect_id"`
	CampaignID uuid.UUID `json:"campaign_id"`
	StepIndex  int       `json:"step_index"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --- STEP 1: Load configuration ---
	fmt.Println("[STEP 1] Loading system configuration...")
	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// --- STEP 2: Connect to NATS Queue Client ---
	fmt.Println("[STEP 2] Connecting to NATS Queue...")
	q, err := queue.NewClient(cfg.NATS.URL, nats.Name("test-gtm-cli"), nats.MaxReconnects(-1))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

	// --- STEP 3: Connect to Database ---
	fmt.Println("[STEP 3] Connecting to PostgreSQL Database...")
	ctx := context.Background()
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to parse db config", "error", err)
		os.Exit(1)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	// --- STEP 4: Setup/Seed Test IDs ---
	fmt.Println("[STEP 4] Setting up mock parameters and UUIDs...")
	tenantID := uuid.New()
	campaignID := uuid.New()
	prospectID := uuid.New()
	landingPageID := uuid.New()
	emailFormID := uuid.New()

	fmt.Printf("Generated Mock IDs:\n - Tenant ID: %s\n - Campaign ID: %s\n - Prospect ID: %s\n - Landing Page Slot: %s\n - Email Form Slot: %s\n",
		tenantID, campaignID, prospectID, landingPageID, emailFormID)

	// --- STEP 5: Seed Active SMTP Email Account ---
	fmt.Println("[STEP 5] Seeding an active SMTP sender account...")

	encryptedPass, err := crypto.Encrypt(string(cfg.EncryptionKey), "dummy-pass")
	if err != nil {
		logger.Error("failed to encrypt dummy password", "error", err)
		os.Exit(1)
	}

	var accountID uuid.UUID
	err = dbPool.QueryRow(ctx, `
		INSERT INTO marketing.email_accounts (email, encrypted_password, status, tenant_id, daily_send_count, daily_send_limit)
		VALUES ($1, $2, 'active', $3, 0, 100)
		ON CONFLICT (email) DO UPDATE SET status = 'active', daily_send_limit = 100, tenant_id = EXCLUDED.tenant_id, encrypted_password = EXCLUDED.encrypted_password
		RETURNING id;
	`, "yankz@usetoro.io", encryptedPass, tenantID).Scan(&accountID)
	if err != nil {
		logger.Error("failed to seed email account", "error", err)
		os.Exit(1)
	}
	fmt.Printf("Sender account registered: %s\n", accountID)

	// --- STEP 6: Seed Campaign ---
	fmt.Println("[STEP 6] Seeding the marketing campaign...")
	_, err = dbPool.Exec(ctx, `
		INSERT INTO marketing.campaigns (id, name, tenant_id, status)
		VALUES ($1, 'GTM Campaign Test Flow', $2, 'active');
	`, campaignID, tenantID)
	if err != nil {
		logger.Error("failed to seed campaign", "error", err)
		os.Exit(1)
	}

	// --- STEP 7: Seed Campaign Steps (Linked to Landing Page and Email Form slots) ---
	fmt.Println("[STEP 7] Seeding Campaign step 0 (Horizontal outreach template)...")
	var stepID uuid.UUID
	err = dbPool.QueryRow(ctx, `
		INSERT INTO marketing.campaign_steps (campaign_id, step_number, subject_template, body_template, delay_duration, landing_page_id, email_form_id)
		VALUES ($1, 0, 'Urgent: Hello {{.first_name}}', 'Hi {{.first_name}}, please review our new offerings at form: {{.email_form_id}} or page: {{.landing_page_id}}', '5 minutes'::interval, $2, $3)
		RETURNING id;
	`, campaignID, landingPageID, emailFormID).Scan(&stepID)
	if err != nil {
		logger.Error("failed to seed campaign step", "error", err)
		os.Exit(1)
	}
	fmt.Printf("Campaign step 0 registered: %s\n", stepID)

	// --- STEP 8: Seed Prospect ---
	fmt.Println("[STEP 8] Seeding target prospect in active state...")
	_, err = dbPool.Exec(ctx, `
		INSERT INTO marketing.prospects (id, email, first_name, last_name, campaign_id, current_step_id, status, tenant_id)
		VALUES ($1, 'target-prospect@example.com', 'John', 'Doe', $2, $3, 'active', $4);
	`, prospectID, campaignID, stepID, tenantID)
	if err != nil {
		logger.Error("failed to seed prospect", "error", err)
		os.Exit(1)
	}

	// --- STEP 9: Publish trigger event to NATS to start the outreach flow ---
	fmt.Println("[STEP 9] Publishing Campaign Trigger event to NATS topic (ase.events.email.campaign.triggered)...")
	evt := CampaignTriggerEvent{
		TenantID:   tenantID,
		ProspectID: prospectID,
		CampaignID: campaignID,
		StepIndex:  0,
	}

	evtBytes, err := json.Marshal(evt)
	if err != nil {
		logger.Error("failed to marshal trigger event", "error", err)
		os.Exit(1)
	}

	targetSubject := "ase.events.email.campaign.triggered"
	if err := q.Conn().Publish(targetSubject, evtBytes); err != nil {
		logger.Error("failed to publish to trigger subject", "error", err)
		os.Exit(1)
	}

	fmt.Println("[STEP 10] Event published successfully! The Campaign Sequencer will now run the Horizontal Iteration flow.")
	fmt.Println("\nTo monitor execution, check the Docker logs of the worker/protocol services:")
	fmt.Println("  docker-compose logs -f protocol")
}

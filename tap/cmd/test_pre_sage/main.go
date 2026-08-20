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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/sage/pnm"
	"github.com/Yankzy/usetoro/internal/queue"
)

const TargetEmail = "yankz@usetoro.io"
const DossierCode = "1042"
const RealmID = "realm_1042"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// if cfg.DatabaseURL == "" || strings.Contains(cfg.DatabaseURL, "@db:5432") || strings.Contains(cfg.DatabaseURL, "localhost") {
	// 	cfg.DatabaseURL = "postgres://toro:toro_password@yankz.local:5435/toro?sslmode=disable"
	// }
	// if cfg.NATS.URL == "" || strings.Contains(cfg.NATS.URL, "nats-1") || strings.Contains(cfg.NATS.URL, "localhost") {
	// 	cfg.NATS.URL = "nats://yankz.local:4222"
	// }

	ctx := context.Background()

	// 1. Connect to PostgreSQL Database
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

	_ = database.New(dbPool)

	// 2. Connect to NATS Queue
	q, err := queue.NewClient(cfg.NATS.URL, nats.Name("test-pre-sage-cli"), nats.MaxReconnects(-1))
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer q.Close()

	logger.Info("Starting Pre-Sage Email MVP Seed & Pipeline Test", "target_email", TargetEmail, "dossier", DossierCode)

	// =========================================================================
	// STEP 1: SEED DATABASE (toro_core.entities & shadow_erp)
	// =========================================================================
	entityID := uuid.New()

	// Seed entity and user for yankz@usetoro.io
	_, err = dbPool.Exec(ctx, `
		INSERT INTO toro_core.entities (id, name, entity_type)
		VALUES ($1, 'Yankz (Toro Admin)', 'apex_cpa')
		ON CONFLICT (id) DO NOTHING;
	`, entityID)
	if err != nil {
		logger.Warn("Database seed entity check", "error", err)
	}

	_, err = dbPool.Exec(ctx, `
		INSERT INTO toro_core.users (entity_id, email, password_hash, full_name)
		VALUES ($1, $2, '$2a$10$dummyhash', 'Yankz Admin')
		ON CONFLICT (email) DO UPDATE SET updated_at = NOW();
	`, entityID, TargetEmail)
	if err != nil {
		logger.Warn("Database seed user check", "error", err)
	}

	// Fetch confirmed entity ID for this user
	var dbEntityID uuid.UUID
	userEmail := "yankz@fignode.com"
	err = dbPool.QueryRow(ctx, `SELECT entity_id FROM toro_core.users WHERE email = $1 LIMIT 1;`, userEmail).Scan(&dbEntityID)
	if err != nil {
		logger.Error("Failed to fetch entity for target email", "email", userEmail, "error", err)
		os.Exit(1)
	}
	logger.Info("Seeded/Resolved Entity ID", "entity_id", dbEntityID.String())

	// Seed shadow_erp.client_dossiers
	_, err = dbPool.Exec(ctx, `
		INSERT INTO shadow_erp.client_dossiers (realm_id, fiduciaire_id, dossier_code, company_name)
		VALUES ($1, $2, $3, 'Atlas SARL (Maroc)')
		ON CONFLICT (fiduciaire_id, dossier_code) DO UPDATE SET updated_at = NOW();
	`, RealmID, dbEntityID, DossierCode)
	if err != nil {
		logger.Error("Failed to seed client_dossiers", "error", err)
		os.Exit(1)
	}
	logger.Info("Seeded shadow_erp.client_dossiers", "dossier_code", DossierCode, "realm_id", RealmID)

	// Seed shadow_erp.accounts (PCM Chart of Accounts)
	accounts := []struct {
		ErpID string
		Name  string
		Type  string
	}{
		{"61440000", "Achats de matieres et fournitures (HT)", "Expense"},
		{"34552000", "TVA Recupérable sur charges (20%)", "Asset"},
		{"44110000", "Fournisseurs (Compte Collectif)", "Liability"},
		{"51410001", "Banque Attijariwafa (MAD)", "Asset"},
	}

	for _, acc := range accounts {
		_, err = dbPool.Exec(ctx, `
			INSERT INTO shadow_erp.accounts (erp_id, realm_id, name, account_type, sync_token)
			VALUES ($1, $2, $3, $4, 'seed_token')
			ON CONFLICT (realm_id, erp_id) DO UPDATE SET name = EXCLUDED.name, updated_at = NOW();
		`, acc.ErpID, RealmID, acc.Name, acc.Type)
		if err != nil {
			logger.Warn("Seed shadow_erp.accounts item warning", "erp_id", acc.ErpID, "error", err)
		}
	}
	logger.Info("Seeded shadow_erp.accounts PCM Chart of Accounts")

	// Seed shadow_erp.vendors (Maroc Telecom CT_Num = IAM001)
	_, err = dbPool.Exec(ctx, `
		INSERT INTO shadow_erp.vendors (erp_id, realm_id, display_name, sync_token, ai_synonyms)
		VALUES ('IAM001', $1, 'Maroc Telecom', 'seed_token', '["IAM CASABLANCA", "MAROC TELECOM SA"]'::jsonb)
		ON CONFLICT (realm_id, erp_id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = NOW();
	`, RealmID)
	if err != nil {
		logger.Warn("Seed shadow_erp.vendors warning", "error", err)
	}
	logger.Info("Seeded shadow_erp.vendors master vendor (IAM001 -> Maroc Telecom)")

	// =========================================================================
	// STEP 2: GENERATE REAL PDF BINARY ATTACHMENTS (BC, BL, Facture, Bank Settlement)
	// =========================================================================
	// 1. Bon de Commande PDF (BC)
	bcText := "BON DE COMMANDE BC-2026-0742 | Client: Atlas SARL | Fournisseur: Maroc Telecom | Total: 15000.00 MAD | Date: 2026-07-20"
	bcPdf := createMinimalPDF("Bon de Commande BC-2026-0742", bcText)

	// 2. Bon de Livraison PDF (BL)
	blText := "BON DE LIVRAISON BL-2026-0742 | Client: Atlas SARL | Fournisseur: Maroc Telecom | Total: 15000.00 MAD | Date: 2026-07-22"
	blPdf := createMinimalPDF("Bon de Livraison BL-2026-0742", blText)

	// 3. Facture PDF (Supplier Invoice)
	factureText := "FACTURE FACT-2026-0742 | Client: Atlas SARL | Fournisseur: Maroc Telecom | HT: 12500.00 MAD | TVA 20%: 2500.00 MAD | Total TTC: 15000.00 MAD | Date: 2026-07-24"
	facturePdf := createMinimalPDF("Facture FACT-2026-0742", factureText)

	// 4. Relevé Bancaire PDF (Bank Settlement)
	bankText := "RELEVE DE COMPTE BANCAIRE | Banque Attijariwafa | Date: 2026-07-25 | Debite: 15000.00 MAD | Libelle: VIREMENT MAROC TELECOM SA | Compte: 51410001"
	bankPdf := createMinimalPDF("Releve Bancaire Juillet 2026", bankText)

	now := time.Now()
	lines := []pnm.JournalEntryLine{
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "61440000",
			AuxAcc:      "",
			PieceRef:    "FACT-2026-0742",
			Libelle:     "Maroc Telecom Invoice HT",
			Debit:       12500.00,
			Credit:      0.00,
		},
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "34552000",
			AuxAcc:      "",
			PieceRef:    "FACT-2026-0742",
			Libelle:     "TVA Recup 20%",
			Debit:       2500.00,
			Credit:      0.00,
		},
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "44110000",
			AuxAcc:      "IAM001",
			PieceRef:    "FACT-2026-0742",
			Libelle:     "Maroc Telecom TTC",
			Debit:       0.00,
			Credit:      15000.00,
		},
	}

	if !pnm.BalanceCheck(lines) {
		logger.Error("BalanceCheck failed for generated entries! Debits do not equal Credits.")
		os.Exit(1)
	}

	pnmBytes := pnm.FormatPNM(lines, pnm.DefaultTemplate())

	logger.Info("Generated 4 real PDF binary documents for 4-Way Match", "bc_bytes", len(bcPdf), "bl_bytes", len(blPdf), "facture_bytes", len(facturePdf), "bank_bytes", len(bankPdf), "expected_pnm_bytes", len(pnmBytes))
	fmt.Printf("\n--- 4-Way Match Real PDF Attachments Created ---\n1. BC-2026-0742.pdf (%d bytes)\n2. BL-2026-0742.pdf (%d bytes)\n3. FACT-2026-0742.pdf (%d bytes)\n4. RELEVE-BANK-2026-07.pdf (%d bytes)\n-------------------------------------------------\n\n",
		len(bcPdf), len(blPdf), len(facturePdf), len(bankPdf))

	// =========================================================================
	// STEP 3: PUBLISH INBOUND EMAIL EVENT TO NATS (Postmark Ingress Simulation)
	// =========================================================================
	inboundPayload := map[string]interface{}{
		"From":              TargetEmail,
		"To":                fmt.Sprintf("rap_%s@a.usetoro.io", DossierCode),
		"OriginalRecipient": fmt.Sprintf("rap_%s@a.usetoro.io", DossierCode),
		"Subject":           fmt.Sprintf("[Atlas SARL] Chain PDF Documents & Relevé (Dossier #%s)", DossierCode),
		"MessageID":         fmt.Sprintf("test-msg-%d", time.Now().Unix()),
		"TextBody":          "Bonjour, ci-joint les 4 fichiers PDF originaux de la chaîne comptable pour Atlas SARL: BC-2026-0742.pdf, BL-2026-0742.pdf, FACT-2026-0742.pdf et RELEVE-BANK-2026-07.pdf.",
		"Attachments": []map[string]interface{}{
			{
				"Name":          "BC-2026-0742.pdf",
				"ContentType":   "application/pdf",
				"ContentLength": len(bcPdf),
				"Content":       base64.StdEncoding.EncodeToString(bcPdf),
			},
			{
				"Name":          "BL-2026-0742.pdf",
				"ContentType":   "application/pdf",
				"ContentLength": len(blPdf),
				"Content":       base64.StdEncoding.EncodeToString(blPdf),
			},
			{
				"Name":          "FACT-2026-0742.pdf",
				"ContentType":   "application/pdf",
				"ContentLength": len(facturePdf),
				"Content":       base64.StdEncoding.EncodeToString(facturePdf),
			},
			{
				"Name":          "RELEVE-BANK-2026-07.pdf",
				"ContentType":   "application/pdf",
				"ContentLength": len(bankPdf),
				"Content":       base64.StdEncoding.EncodeToString(bankPdf),
			},
		},
	}

	inboundBytes, _ := json.Marshal(inboundPayload)
	inboundSubject := "worker.inbox.email.postmark_inbound"

	if err := q.Conn().Publish(inboundSubject, inboundBytes); err != nil {
		logger.Error("Failed to publish inbound email to NATS", "error", err)
		os.Exit(1)
	}
	_ = q.Conn().Publish("worker.inbox.postmark_inbound_email", inboundBytes)
	logger.Info("Published simulated inbound email with 4 real PDF attachments to NATS", "subject", inboundSubject, "recipient", fmt.Sprintf("rap_%s@a.usetoro.io", DossierCode))

	// =========================================================================
	// STEP 4: RECORD RECONCILIATION TASK IN SHADOW_ERP
	// =========================================================================
	_, err = dbPool.Exec(ctx, `
		INSERT INTO shadow_erp.reconciliation_tasks (realm_id, period_label, status, email_thread_id)
		VALUES ($1, '2026-07', 'PENDING_MATCH', $2)
		ON CONFLICT DO NOTHING;
	`, RealmID, fmt.Sprintf("test-msg-%d", time.Now().Unix()))
	if err != nil {
		logger.Warn("Failed to record task in shadow_erp.reconciliation_tasks", "error", err)
	} else {
		logger.Info("Recorded task initialization in shadow_erp.reconciliation_tasks", "status", "PENDING_MATCH")
	}

	fmt.Println("\n==================================================================")
	fmt.Printf("🚀 Pre-Sage Email Pipeline Ingress Complete!\n")
	fmt.Printf("• Database seeded: User %s, Dossier %s (%s)\n", TargetEmail, DossierCode, RealmID)
	fmt.Printf("• 4 Real PDF Documents Sent via NATS: BC, BL, Facture, Relevé Bancaire\n")
	fmt.Printf("• Pipeline Triggered: PostmarkInboundEmailWorker -> PcmOcrWorker -> AseBridgeWorker (pcm_bank_reconciliation DAG)\n")
	fmt.Println("==================================================================")
}

// createMinimalPDF generates a valid PDF 1.4 binary file with Helvetica text
func createMinimalPDF(docTitle, docContent string) []byte {
	streamText := fmt.Sprintf("BT /F1 16 Tf 50 750 Td (%s) Tj /F1 12 Tf 0 -30 Td (%s) Tj ET",
		escapePDFText(docTitle), escapePDFText(docContent))

	pdf := fmt.Sprintf("%%PDF-1.4\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [3 0 R] /Count 1>> endobj\n3 0 obj <</Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources <</Font <</F1 4 0 R>>>> /Contents 5 0 R>> endobj\n4 0 obj <</Type /Font /Subtype /Type1 /BaseFont /Helvetica>> endobj\n5 0 obj <</Length %d>>\nstream\n%s\nendstream\nendobj\nxref\n0 6\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n00000000115 00000 n \n0000000244 00000 n \n0000000315 00000 n \ntrailer <</Size 6 /Root 1 0 R>>\nstartxref\n400\n%%%%EOF\n", len(streamText), streamText)

	return []byte(pdf)
}

func escapePDFText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	return s
}

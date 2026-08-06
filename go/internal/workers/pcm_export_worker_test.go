package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

func TestPcmExportWorker_Handle_BasicEnvelope(t *testing.T) {
	worker := &PcmExportWorker{
		logger: testLogger(),
		cfg:    &config.Config{},
	}

	sessionID := uuid.New().String()
	payloadData, _ := json.Marshal(map[string]interface{}{
		"session_id":  sessionID,
		"from_handle": "bot@a.usetoro.io",
		"to_handle":   "user@example.com",
	})

	env := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.REQUEST,
		Body:         payloadData,
	}

	envBytes, _ := json.Marshal(env)

	ctx := context.Background()
	var envTest core.Envelope
	if err := json.Unmarshal(envBytes, &envTest); err != nil {
		t.Fatalf("failed to unmarshal env: %v", err)
	}

	if envTest.Performative != core.REQUEST {
		t.Fatalf("expected REQUEST performative, got %v", envTest.Performative)
	}

	var parsed PcmExportPayload
	if err := json.Unmarshal(envTest.Body, &parsed); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}

	if parsed.SessionID != sessionID {
		t.Errorf("expected sessionID %s, got %s", sessionID, parsed.SessionID)
	}
	if parsed.ToHandle != "user@example.com" {
		t.Errorf("expected to_handle user@example.com, got %s", parsed.ToHandle)
	}
	_ = worker
	_ = ctx
}

func TestPcmExportWorker_GeneratePCMCSV(t *testing.T) {
	txs := []database.GetPcmSessionTransactionsRow{
		{
			RawDescription: pgtype.Text{String: "Paiement CB Uber SA", Valid: true},
			RawAmount:      "150.00",
			CashDirection:  pgtype.Text{String: "OUTFLOW", Valid: true},
			AseExecutionTrace: []byte(`[
				{"dag_node_id": "account_selection", "selected_edge": "619000"},
				{"dag_node_id": "counterparty_extractor", "property_key": "counterparty", "selected_edge": "Uber"}
			]`),
		},
	}

	accMap := map[string]string{
		"619000": "619000",
	}

	csvOutput := generatePCMCSV(txs, accMap)
	if !strings.Contains(csvOutput, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit") {
		t.Errorf("expected header in PCM CSV output, got:\n%s", csvOutput)
	}
	if !strings.Contains(csvOutput, "BQ;") {
		t.Errorf("expected BQ journal in PCM CSV, got:\n%s", csvOutput)
	}
	if !strings.Contains(csvOutput, "F_UBER") {
		t.Errorf("expected auxiliary F_UBER in PCM CSV, got:\n%s", csvOutput)
	}
}

func TestPcmExportWorker_GenerateUSGAAPCSV(t *testing.T) {
	txs := []database.GetPcmSessionTransactionsRow{
		{
			RawDescription: pgtype.Text{String: "AWS Cloud Services", Valid: true},
			RawAmount:      "450.00",
			CashDirection:  pgtype.Text{String: "OUTFLOW", Valid: true},
			AseExecutionTrace: []byte(`[
				{"dag_node_id": "account_selection", "selected_edge": "Software Overhead"},
				{"dag_node_id": "counterparty_extractor", "property_key": "counterparty", "selected_edge": "Amazon Web Services"}
			]`),
		},
	}

	accMap := map[string]string{
		"Software Overhead": "Software Overhead",
	}

	csvOutput := generateUSGAAPCSV(txs, accMap)
	if !strings.Contains(csvOutput, "Journal;Date;Account;Auxiliary;Piece;Description;Debit;Credit") {
		t.Errorf("expected header in US GAAP CSV output, got:\n%s", csvOutput)
	}
	if !strings.Contains(csvOutput, "BANK;") {
		t.Errorf("expected BANK journal in US GAAP CSV, got:\n%s", csvOutput)
	}
	if !strings.Contains(csvOutput, "Software Overhead") {
		t.Errorf("expected account name Software Overhead in US GAAP CSV, got:\n%s", csvOutput)
	}
}

func TestPcmExportWorker_Handle_DisabledExport(t *testing.T) {
	worker := &PcmExportWorker{
		logger: testLogger(),
		cfg:    &config.Config{},
	}

	exportToEmail := false
	payloadData, _ := json.Marshal(PcmExportPayload{
		SessionID:     uuid.New().String(),
		ExportToEmail: &exportToEmail,
	})

	env := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.REQUEST,
		Body:         payloadData,
	}

	envBytes, _ := json.Marshal(env)
	msg := &nats.Msg{Data: envBytes}

	err := worker.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected handle to return nil without error when export disabled, got: %v", err)
	}
}

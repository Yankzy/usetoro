package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestPcmExportWorker_GeneratePCMCSV_DirectMoroccanAccounts(t *testing.T) {
	txs := []PcmExportTxRow{
		{
			RawDescription:       pgtype.Text{String: "PRLV MAROC TELECOM FIBRE PRO", Valid: true},
			RawAmount:            "1200.00",
			CashDirection:        pgtype.Text{String: "OUTFLOW", Valid: true},
			PredictedAccountName: pgtype.Text{String: "614510", Valid: true},
			PredictedVendorName:  pgtype.Text{String: "Maroc Telecom", Valid: true},
		},
		{
			RawDescription:       pgtype.Text{String: "CARTE AWS EMEA SOFTWARE", Valid: true},
			RawAmount:            "6000.00",
			CashDirection:        pgtype.Text{String: "OUTFLOW", Valid: true},
			PredictedAccountName: pgtype.Text{String: "613670", Valid: true},
			PredictedVendorName:  pgtype.Text{String: "Amazon Web Services", Valid: true},
		},
		{
			RawDescription: pgtype.Text{String: "VIR RECU CLIENT DISTRIBUTION", Valid: true},
			RawAmount:      "24000.00",
			CashDirection:  pgtype.Text{String: "INFLOW", Valid: true},
			MoroccanEnrichment: []byte(`{
				"counterparty": {"normalized_name": "Atlas Distribution"},
				"pcgm_accounting": {"suggested_account": "342100"}
			}`),
		},
	}

	accMap := map[string]string{}

	csvOutput := generatePCMCSV(txs, accMap)
	require.NotEmpty(t, csvOutput)

	assert.Contains(t, csvOutput, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit")
	assert.Contains(t, csvOutput, "614510")
	assert.Contains(t, csvOutput, "F_MAROCTEL")
	assert.Contains(t, csvOutput, "613670")
	assert.Contains(t, csvOutput, "F_AMAZONWE")
	assert.Contains(t, csvOutput, "342100")
	assert.Contains(t, csvOutput, "C_ATLASDIS")
}

func TestPcmExportWorker_GenerateUSGAAPCSV(t *testing.T) {
	txs := []PcmExportTxRow{
		{
			RawDescription:      pgtype.Text{String: "AWS Cloud Services", Valid: true},
			RawAmount:           "450.00",
			CashDirection:       pgtype.Text{String: "OUTFLOW", Valid: true},
			PredictedVendorName: pgtype.Text{String: "Amazon Web Services", Valid: true},
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
		t.Fatalf("expected nil error for disabled export, got %v", err)
	}
}

func TestPcmExportWorker_GenerateExcelWorkbook_Attachment(t *testing.T) {
	txs := []PcmExportTxRow{
		{
			ID:                   pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawDescription:       pgtype.Text{String: "COMMISSIONS BANCAIRES JUILLET", Valid: true},
			RawAmount:            "110.00",
			CashDirection:        pgtype.Text{String: "OUTFLOW", Valid: true},
			PredictedAccountName: pgtype.Text{String: "614700", Valid: true},
			PredictedVendorName:  pgtype.Text{String: "Banque Populaire", Valid: true},
		},
		{
			ID:                   pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawDescription:       pgtype.Text{String: "LOYER COMMERCIAL BD ZERKTOUNI", Valid: true},
			RawAmount:            "8000.00",
			CashDirection:        pgtype.Text{String: "OUTFLOW", Valid: true},
			PredictedAccountName: pgtype.Text{String: "613100", Valid: true},
			PredictedVendorName:  pgtype.Text{String: "SCI Zerktouni", Valid: true},
		},
	}

	accMap := map[string]string{}
	var exportRecs []pcm_cash.ExportRecord
	for _, tx := range txs {
		desc := tx.RawDescription.String
		amount := 110.0
		if desc == "LOYER COMMERCIAL BD ZERKTOUNI" {
			amount = 8000.0
		}
		compteA := extractAuxAccount(desc, tx.PredictedVendorName.String, "OUTFLOW", tx.PredictedAccountName.String)
		exportRecs = append(exportRecs, pcm_cash.ExportRecord{
			ID:             uuid.UUID(tx.ID.Bytes).String(),
			DateStr:        "15/07/2026",
			RawDescription: desc,
			Amount:         amount,
			Direction:      "OUTFLOW",
			AccountCode:    tx.PredictedAccountName.String,
			AuxiliaryCode:  compteA,
			Counterparty:   tx.PredictedVendorName.String,
			StatementType:  "BANK_STATEMENT",
		})
	}

	_ = accMap
	xlsxBytes, err := pcm_cash.GenerateMoroccanBookkeepingWorkbook(exportRecs)
	require.NoError(t, err)
	require.NotEmpty(t, xlsxBytes)
}


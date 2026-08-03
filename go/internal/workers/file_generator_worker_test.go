package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/erp/sage/pnm"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)


func TestFileGeneratorWorker_GeneratePNM(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	payload := FileGeneratorPayload{
		FileType: "pnm",
		FileName: "test_export.pnm",
		PNMLines: []pnm.JournalEntryLine{
			{
				JournalCode: "ACH",
				Date:        time.Now(),
				GeneralAcc:  "614300",
				PieceRef:    "F2026-0712",
				Libelle:     "Orange Maroc HT",
				Debit:       1000.0,
				Credit:      0.0,
			},
			{
				JournalCode: "ACH",
				Date:        time.Now(),
				GeneralAcc:  "345520",
				PieceRef:    "F2026-0712",
				Libelle:     "TVA Recup 20%",
				Debit:       200.0,
				Credit:      0.0,
			},
			{
				JournalCode: "ACH",
				Date:        time.Now(),
				GeneralAcc:  "441100",
				PieceRef:    "F2026-0712",
				Libelle:     "Orange Maroc TTC",
				Debit:       0.0,
				Credit:      1200.0,
			},
		},
	}

	b, fileName, contentType, err := worker.GeneratePNM(payload)
	if err != nil {
		t.Fatalf("unexpected error generating PNM: %v", err)
	}

	if fileName != "test_export.pnm" {
		t.Errorf("expected filename 'test_export.pnm', got '%s'", fileName)
	}

	if contentType != "text/plain" {
		t.Errorf("expected content type 'text/plain', got '%s'", contentType)
	}

	pnmContent := string(b)
	if !strings.Contains(pnmContent, "ACH") || !strings.Contains(pnmContent, "614300") {
		t.Errorf("expected PNM content to contain ACH and account 614300, got:\n%s", pnmContent)
	}
}

func TestFileGeneratorWorker_GenerateCSV(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	payload := FileGeneratorPayload{
		FileType: "csv",
		FileName: "reconciliation.csv",
		Header:   []string{"Date", "Vendor", "Amount_MAD"},
		Rows: [][]string{
			{"15/07/2026", "Orange Maroc SA", "1200.00"},
			{"20/07/2026", "Lydec Casablanca", "450.00"},
		},
	}

	b, fileName, contentType, err := worker.GenerateCSV(payload)
	if err != nil {
		t.Fatalf("unexpected error generating CSV: %v", err)
	}

	if fileName != "reconciliation.csv" {
		t.Errorf("expected filename 'reconciliation.csv', got '%s'", fileName)
	}

	if contentType != "text/csv" {
		t.Errorf("expected content type 'text/csv', got '%s'", contentType)
	}

	csvContent := string(b)
	if !strings.Contains(csvContent, "Date,Vendor,Amount_MAD") || !strings.Contains(csvContent, "Orange Maroc SA") {
		t.Errorf("expected CSV content to contain header and vendor, got:\n%s", csvContent)
	}
}

func TestFileGeneratorWorker_GeneratePDF(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	payload := FileGeneratorPayload{
		FileType:   "pdf",
		FileName:   "invoice.pdf",
		PDFContent: "Orange Maroc SA\nInvoice Total: 1,200.00 MAD",
	}

	b, fileName, contentType, err := worker.GeneratePDF(payload)
	if err != nil {
		t.Fatalf("unexpected error generating PDF: %v", err)
	}

	if fileName != "invoice.pdf" {
		t.Errorf("expected filename 'invoice.pdf', got '%s'", fileName)
	}

	if contentType != "application/pdf" {
		t.Errorf("expected content type 'application/pdf', got '%s'", contentType)
	}

	pdfHeader := string(b[:8])
	if pdfHeader != "%PDF-1.4" {
		t.Errorf("expected PDF header '%%PDF-1.4', got '%s'", pdfHeader)
	}
}

func TestFileGeneratorWorker_GenerateTXT(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	payload := FileGeneratorPayload{
		FileType:   "txt",
		FileName:   "summary.txt",
		PDFContent: "Atlas Office Solutions SARL Summary Report",
	}

	b, fileName, contentType, err := worker.GenerateTXT(payload)
	if err != nil {
		t.Fatalf("unexpected error generating TXT: %v", err)
	}

	if fileName != "summary.txt" {
		t.Errorf("expected filename 'summary.txt', got '%s'", fileName)
	}

	if contentType != "text/plain" {
		t.Errorf("expected content type 'text/plain', got '%s'", contentType)
	}

	if string(b) != "Atlas Office Solutions SARL Summary Report" {
		t.Errorf("unexpected text content: '%s'", string(b))
	}
}

func TestFileGeneratorWorker_Handle_SwitchRouting(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	// Test CSV generation via Handle switch statement
	payload := FileGeneratorPayload{
		FileType: "CSV",
		FileName: "test_switch.csv",
		Header:   []string{"Col1", "Col2"},
		Rows:     [][]string{{"Val1", "Val2"}},
	}

	bodyBytes, _ := json.Marshal(payload)
	env := core.Envelope{
		Performative: core.REQUEST,
		Body:         bodyBytes,
	}
	envBytes, _ := json.Marshal(env)

	natsMsg := nats.Msg{
		Data: envBytes,
	}

	err := worker.Handle(context.Background(), &natsMsg)
	if err != nil {
		t.Fatalf("expected Handle to process CSV without error, got %v", err)
	}

}

func TestFileGeneratorWorker_Handle_UnsupportedFileType(t *testing.T) {
	worker := &FileGeneratorWorker{
		logger: slog.Default(),
	}

	payload := FileGeneratorPayload{
		FileType: "docx",
		FileName: "unsupported.docx",
	}

	bodyBytes, _ := json.Marshal(payload)
	env := core.Envelope{
		Performative: core.REQUEST,
		Body:         bodyBytes,
	}
	envBytes, _ := json.Marshal(env)

	natsMsg := nats.Msg{
		Data: envBytes,
	}

	err := worker.Handle(context.Background(), &natsMsg)
	if err == nil || !strings.Contains(err.Error(), "unsupported file_type") {
		t.Fatalf("expected unsupported file_type error, got %v", err)
	}
}

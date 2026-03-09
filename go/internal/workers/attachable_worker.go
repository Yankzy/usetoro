package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/nats-io/nats.go"
)

// AttachableWorker processes pending receipt uploads using CDC events
type AttachableWorker struct {
	logger            *slog.Logger
	nc                *nats.Conn
	js                nats.JetStreamContext
	attachableService *accounting.AttachableService
}

func NewAttachableWorker(logger *slog.Logger, nc *nats.Conn, attachableService *accounting.AttachableService) (*AttachableWorker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &AttachableWorker{
		logger:            logger,
		nc:                nc,
		js:                js,
		attachableService: attachableService,
	}, nil
}

func (w *AttachableWorker) Start(ctx context.Context) error {
	w.logger.Info("🚀 AttachableWorker CDC event consumer started")

	subject := "ledger.shadow_erp_attachables.insert"

	sub, err := w.js.QueueSubscribe(subject, "toro-attachable-workers", func(msg *nats.Msg) {
		w.handleEvent(ctx, msg)
	}, nats.ManualAck())

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	<-ctx.Done()
	w.logger.Info("🛑 AttachableWorker shutting down")
	return sub.Unsubscribe()
}

func (w *AttachableWorker) handleEvent(ctx context.Context, msg *nats.Msg) {
	var event cdc.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("Failed to parse CDC Event JSON", "error", err)
		msg.Term()
		return
	}

	erpID, ok := event.Data["erp_id"].(string)
	if !ok || !strings.HasPrefix(erpID, "pending-") {
		// Not a pending upload, ignore
		msg.Ack()
		return
	}

	w.logger.Info("Processing pending attachable", "erp_id", erpID)

	realmID, _ := event.Data["realm_id"].(string)
	filename, _ := event.Data["file_name"].(string)
	contentType, _ := event.Data["content_type"].(string)

	refsRaw, ok := event.Data["attachable_refs"].(string)
	if !ok {
		w.logger.Error("Missing attachable_refs in pending upload", "erp_id", erpID)
		msg.Ack() // Ack to prevent poison pill since data is missing
		return
	}

	var refs map[string]string
	if err := json.Unmarshal([]byte(refsRaw), &refs); err != nil {
		w.logger.Error("Invalid attachable_refs JSON", "error", err)
		msg.Ack()
		return
	}

	entityType := refs["entityType"]
	entityID := refs["entityID"]
	filePath := refs["filePath"]

	if filePath == "" {
		w.logger.Error("No filePath found in attachable refs", "erp_id", erpID)
		msg.Ack()
		return
	}

	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		w.logger.Error("Failed to read pending upload file", "filePath", filePath, "error", err)
		// It might be a transient IO issue, Nak for retry
		msg.Nak()
		return
	}

	// Process upload via QBO
	if err := w.attachableService.UploadAttachable(ctx, realmID, entityType, entityID, fileBytes, filename, contentType); err != nil {
		w.logger.Error("Failed to upload attachable to ERP from worker", "error", err)
		msg.Nak()
		return
	}

	// Success: Clean up local file
	_ = os.Remove(filePath)

	msg.Ack()
	w.logger.Info("✅ Successfully processed pending attachable", "erp_id", erpID)
}

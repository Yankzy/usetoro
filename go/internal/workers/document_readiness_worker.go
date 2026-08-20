package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	know "github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

const (
	MaxDocumentReadinessPolls = 3
	DocumentPollInterval      = 1 * time.Second
)

// DocumentReadinessWorker is the document gatekeeper for document-driven
// workflows.
//
// It verifies that every document attached to the workflow has completed
// OCR or embedding processing before allowing downstream workers to continue.
//
// The worker performs at most MaxDocumentReadinessPolls database reads per
// document. It does not perform OCR itself and does not mutate document state.
type DocumentReadinessWorker struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &DocumentReadinessWorker{
			pool:   deps.Store.Pool,
			logger: deps.Logger.With("worker", "document_readiness"),
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *DocumentReadinessWorker) Init(ctx context.Context) error {
	w.logger.Info("DocumentReadinessWorker initialized")
	return nil
}

func (w *DocumentReadinessWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)

	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.document_readiness"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			subject = "worker.inbox.document_readiness"
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "worker-inbox-document_readiness-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// Handle is the worker entrypoint.
//
// Expected orchestrator body:
//
//	{
//	    "payload": {
//	        "data": {
//	            "input": {
//	                "session_id": "...",
//	                "document_ids": ["..."],
//	                "attachments": [...]
//	            }
//	        }
//	    }
//	}
//
// The nested orchestrator request is normalized into the flat input object.
// The flat input is then returned to the orchestrator with document readiness
// information and raw OCR JSON attached.
func (w *DocumentReadinessWorker) Handle(
	ctx context.Context,
	msg *nats.Msg,
) error {
	var env core.Envelope

	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error(
			"failed to unmarshal orchestrator envelope",
			"error", err,
		)

		// Malformed envelopes cannot be processed and should not create
		// an infinite redelivery loop.
		return nil
	}

	input, err := extractOrchestratorInput(env.Body)
	if err != nil {
		w.logger.Error(
			"failed to extract orchestrator input",
			"conversation_id", env.ConversationID,
			"error", err,
		)

		return fmt.Errorf("invalid orchestrator payload: %v", err)
	}

	documentIDs, err := extractDocumentIDs(input)
	if err != nil {
		w.logger.Error(
			"failed to extract document IDs",
			"conversation_id", env.ConversationID,
			"error", err,
		)

		return fmt.Errorf("invalid document_ids: %v", err)
	}

	w.logger.Info(
		"checking document readiness",
		"conversation_id", env.ConversationID,
		"document_count", len(documentIDs),
	)

	// No documents means there is nothing for this gatekeeper to verify.
	if len(documentIDs) == 0 {
		result := cloneInput(input)

		result["status"] = "SUCCESS"
		result["document_verified"] = false
		result["documents"] = []any{}

		return w.replyInform(env, result)
	}

	if w.pool == nil {
		w.logger.Error(
			"document database pool is unavailable",
			"conversation_id", env.ConversationID,
		)

		return fmt.Errorf("document database pool is unavailable")
	}

	docStore := know.NewDocumentStore(w.pool, w.logger)

	documents := make([]map[string]any, 0, len(documentIDs))

	for _, documentID := range documentIDs {
		docUUID, err := uuid.Parse(documentID)
		if err != nil {
			w.logger.Error(
				"invalid document UUID",
				"document_id", documentID,
				"error", err,
			)

			return fmt.Errorf("invalid document UUID: %v", err)
		}

		doc, err := w.waitForDocument(
			ctx,
			docStore,
			docUUID,
		)
		if err != nil {
			w.logger.Error(
				"document failed readiness verification",
				"document_id", documentID,
				"conversation_id", env.ConversationID,
				"error", err,
			)

			return fmt.Errorf("document failed readiness verification: %v", err)
		}

		rawOCRJSON := json.RawMessage(doc.RawOCRJSON)
		// w.logger.Info(
		// 	"document ready",
		// 	"document_id", documentID,
		// 	"ocr_status", doc.OCRStatus,
		// 	"raw_ocr_json", rawOCRJSON,
		// )

		if len(rawOCRJSON) == 0 {
			w.logger.Error(
				"ready document has no raw OCR JSON",
				"document_id", documentID,
				"ocr_status", doc.OCRStatus,
			)

			return fmt.Errorf("document reached ready state but raw_ocr_json is empty")
		}

		if !json.Valid(rawOCRJSON) {
			w.logger.Error(
				"document raw OCR JSON is invalid",
				"document_id", documentID,
				"ocr_status", doc.OCRStatus,
			)

			return fmt.Errorf("document raw_ocr_json contains invalid JSON")
		}

		documents = append(documents, map[string]any{
			"document_id":  doc.ID.String(),
			"ocr_status":   doc.OCRStatus,
			"raw_ocr_json": rawOCRJSON,
		})
	}

	// Start with the original flat input so downstream workers receive all
	// existing workflow context without having to reconstruct it.
	result := cloneInput(input)

	result["status"] = "SUCCESS"
	result["document_verified"] = true
	result["documents"] = documents

	w.logger.Info(
		"all documents verified",
		"document_count", len(documents),
	)

	return w.replyInform(env, result)
}

// waitForDocument performs at most three database reads.
//
// Check 1 happens immediately.
// Check 2 happens one second later.
// Check 3 happens one second after that.
//
// There is no long-running polling loop.
func (w *DocumentReadinessWorker) waitForDocument(
	ctx context.Context,
	docStore *know.DocumentStore,
	documentID uuid.UUID,
) (*know.Document, error) {
	var (
		lastStatus string
		lastErr    error
	)

	for attempt := 1; attempt <= MaxDocumentReadinessPolls; attempt++ {
		doc, err := docStore.GetDocumentByID(ctx, documentID)

		if err != nil {
			lastErr = err

			w.logger.Warn(
				"failed to read document readiness state",
				"document_id", documentID.String(),
				"attempt", attempt,
				"max_attempts", MaxDocumentReadinessPolls,
				"error", err,
			)
		} else if doc == nil {
			lastErr = fmt.Errorf("document not found")

			w.logger.Warn(
				"document not found",
				"document_id", documentID.String(),
				"attempt", attempt,
				"max_attempts", MaxDocumentReadinessPolls,
			)
		} else {
			lastStatus = doc.OCRStatus
			lastErr = nil

			switch doc.OCRStatus {
			case string(know.DocStatusOCRSuccess),
				string(know.DocStatusEmbeddingsSuccess):

				return doc, nil

			case string(know.DocStatusFailed):
				return nil, fmt.Errorf(
					"document OCR processing failed with status %q",
					doc.OCRStatus,
				)
			}

			w.logger.Debug(
				"document not ready",
				"document_id", documentID.String(),
				"ocr_status", doc.OCRStatus,
				"attempt", attempt,
				"max_attempts", MaxDocumentReadinessPolls,
			)
		}

		if attempt == MaxDocumentReadinessPolls {
			break
		}

		timer := time.NewTimer(DocumentPollInterval)

		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}

			return nil, ctx.Err()

		case <-timer.C:
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf(
			"document not ready after %d checks: %w",
			MaxDocumentReadinessPolls,
			lastErr,
		)
	}

	return nil, fmt.Errorf(
		"document not ready after %d checks; last OCR status: %q",
		MaxDocumentReadinessPolls,
		lastStatus,
	)
}

// extractOrchestratorInput unwraps:
//
//	body.payload.data.input
//
// Everything after this function deals only with the flat workflow input.
func extractOrchestratorInput(
	body json.RawMessage,
) (map[string]any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("envelope body is empty")
	}

	var request struct {
		Payload struct {
			Data struct {
				Input map[string]any `json:"input"`
			} `json:"data"`
		} `json:"payload"`
	}

	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf(
			"unmarshal envelope body: %w",
			err,
		)
	}

	if request.Payload.Data.Input == nil {
		return nil, fmt.Errorf(
			"body.payload.data.input is missing",
		)
	}

	return request.Payload.Data.Input, nil
}

// extractDocumentIDs reads document_ids from the normalized input.
//
// If document_ids is absent, attachments are also inspected as a defensive
// fallback. IDs are deduplicated so the database is never queried twice for
// the same document within one workflow execution.
func extractDocumentIDs(
	input map[string]any,
) ([]string, error) {
	seen := make(map[string]struct{})

	documentIDs := make([]string, 0)

	add := func(id string) {
		if id == "" {
			return
		}

		if _, exists := seen[id]; exists {
			return
		}

		seen[id] = struct{}{}
		documentIDs = append(documentIDs, id)
	}

	if rawIDs, exists := input["document_ids"]; exists {
		switch ids := rawIDs.(type) {
		case []any:
			for index, raw := range ids {
				id, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf(
						"document_ids[%d] must be a string",
						index,
					)
				}

				add(id)
			}

		case []string:
			for _, id := range ids {
				add(id)
			}

		default:
			return nil, fmt.Errorf(
				"document_ids must be an array of strings",
			)
		}
	}

	// Normally document_ids should already exist. This fallback prevents
	// attachments from being silently ignored if the producer omits it.
	if len(documentIDs) == 0 {
		if rawAttachments, exists := input["attachments"]; exists {
			attachments, ok := rawAttachments.([]any)
			if !ok {
				return nil, fmt.Errorf(
					"attachments must be an array",
				)
			}

			for index, rawAttachment := range attachments {
				attachment, ok := rawAttachment.(map[string]any)
				if !ok {
					return nil, fmt.Errorf(
						"attachments[%d] must be an object",
						index,
					)
				}

				rawDocumentID, exists := attachment["document_id"]
				if !exists {
					continue
				}

				documentID, ok := rawDocumentID.(string)
				if !ok {
					return nil, fmt.Errorf(
						"attachments[%d].document_id must be a string",
						index,
					)
				}

				add(documentID)
			}
		}
	}

	return documentIDs, nil
}

func cloneInput(input map[string]any) map[string]any {
	cloned := make(map[string]any, len(input)+3)

	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}

func (w *DocumentReadinessWorker) replyInform(
	inEnv core.Envelope,
	resultData map[string]any,
) error {
	resultBytes, err := json.Marshal(resultData)
	if err != nil {
		return fmt.Errorf(
			"marshal document readiness result: %w",
			err,
		)
	}

	proofBytes, err := json.Marshal(core.Proof{
		Type: "document_readiness_result",
		Data: resultBytes,
	})
	if err != nil {
		return fmt.Errorf(
			"marshal document readiness proof: %w",
			err,
		)
	}

	outEnv := core.Envelope{
		ID:             uuid.New().String(),
		ConversationID: inEnv.ConversationID,
		Performative:   core.INFORM,
		Body:           proofBytes,
	}

	outBytes, err := json.Marshal(outEnv)
	if err != nil {
		return fmt.Errorf(
			"marshal document readiness envelope: %w",
			err,
		)
	}

	if w.nc == nil {
		return fmt.Errorf(
			"NATS connection unavailable while publishing document readiness result",
		)
	}

	const inbox = "orchestrator.inbox"

	// w.logger.Debug(
	// 	"publishing document readiness result",
	// 	"ENVELOPE", outEnv,
	// )

	if err := w.nc.Publish(inbox, outBytes); err != nil {
		return fmt.Errorf(
			"publish document readiness result to %s: %w",
			inbox,
			err,
		)
	}

	return nil
}

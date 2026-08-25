package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	accountingservice "github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

type JournalPostingWorker struct {
	service *accountingservice.JournalPostingService
	logger  *slog.Logger
	nc      *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.DBPool == nil {
			return nil, nil
		}
		return &JournalPostingWorker{service: accountingservice.NewJournalPostingService(deps.DBPool), logger: deps.Logger.With("worker", "journal_posting"), nc: deps.Queue}, nil
	})
}

func (w *JournalPostingWorker) Init(context.Context) error { return nil }
func (w *JournalPostingWorker) Stop()                      {}
func (w *JournalPostingWorker) Subscriptions() []SubscriptionConfig {
	subject, err := core.BuildWorkerInboxFromActivity("workers.pcm.journal_post")
	if err != nil {
		subject = "worker.inbox.pcm_journal_post"
	}
	return []SubscriptionConfig{{Subject: subject, Group: "worker-inbox-pcm-journal-post", Options: []nats.SubOpt{nats.DeliverAll(), nats.AckExplicit()}}}
}

func (w *JournalPostingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil || env.Performative != core.REQUEST {
		return fmt.Errorf("journal posting requires a workflow request envelope")
	}
	var payload struct {
		EntityID    string `json:"entity_id"`
		ActorUserID string `json:"actor_user_id"`
		Results     []struct {
			Stage2Hash   string                         `json:"stage2_hash"`
			Stage2Output accountingservice.Stage2Output `json:"stage2_output"`
		} `json:"results"`
		ExpectedProposalHash string                         `json:"expected_proposal_hash"`
		Stage2Output         accountingservice.Stage2Output `json:"stage2_output"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil {
		return err
	}
	if len(payload.Results) == 0 && payload.Stage2Output.SchemaVersion != "" {
		payload.Results = append(payload.Results, struct {
			Stage2Hash   string                         `json:"stage2_hash"`
			Stage2Output accountingservice.Stage2Output `json:"stage2_output"`
		}{Stage2Hash: payload.ExpectedProposalHash, Stage2Output: payload.Stage2Output})
	}
	posted := make([]accountingservice.JournalPostingResult, 0, len(payload.Results))
	skipped := 0
	for _, candidate := range payload.Results {
		if candidate.Stage2Output.Outcome != accountingservice.Stage2ProposedTreatment {
			skipped++
			continue
		}
		result, err := w.service.PostApprovedProposal(ctx, accountingservice.PostApprovedProposalCommand{EntityID: payload.EntityID, ActorUserID: payload.ActorUserID, ExpectedProposalHash: candidate.Stage2Hash, Output: candidate.Stage2Output})
		if err != nil {
			return err
		}
		posted = append(posted, result)
	}
	return publishWorkflowResult(w.nc, msg.Reply, env, "journal_posting", map[string]interface{}{"status": "SUCCESS", "entity_id": payload.EntityID, "posted": posted, "posted_count": len(posted), "skipped_count": skipped})
}

func publishWorkflowResult(nc *nats.Conn, replySubject string, request core.Envelope, proofType string, data interface{}) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	proofBody, err := json.Marshal(core.Proof{Type: core.ProofAPI, Timestamp: time.Now().Unix(), Data: encoded})
	if err != nil {
		return err
	}
	reply := core.Envelope{
		ID: uuid.NewString(), Timestamp: time.Now().UTC(), SenderDID: "did:toro:worker:" + proofType,
		ReceiverDID: workflows.OrchestratorDID, Performative: core.INFORM,
		ConversationID: request.ConversationID, Body: proofBody,
	}
	replyBytes, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	target := replySubject
	if target == "" || strings.HasPrefix(target, "$JS.ACK.") {
		target = workflows.OrchestratorInbox
	}
	if nc == nil {
		return fmt.Errorf("workflow result queue is unavailable")
	}
	return nc.Publish(target, replyBytes)
}

package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
)

type QBOFetchRequest struct {
	RealmID    string `json:"realm_id" description:"The QBO Realm ID for the company"`
	EntityType string `json:"entity_type" description:"The type of entity (e.g. 'Invoice', 'Bill', 'Vendor', 'Customer')"`
	EntityID   string `json:"entity_id" description:"The QBO ID of the entity"`
}

type QBOFetchWorker struct {
	logger *slog.Logger
	deps   Dependencies
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &QBOFetchWorker{
			logger: deps.Logger,
			deps:   deps,
		}, nil
	})
}

func (w *QBOFetchWorker) Init(ctx context.Context) error {
	return nil
}

func (w *QBOFetchWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.deps.Config.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject = "workers.qbo.fetch"
	}
	group := workerCfg.Group
	if group == "" {
		group = "qbo-fetch-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable("qbo-fetch"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *QBOFetchWorker) ToolName() string {
	return "fetch_qbo_entity"
}

func (w *QBOFetchWorker) ToolDescription() string {
	return "Fetch the full details of a specific QuickBooks Online (QBO) entity such as an Invoice, Bill, or Customer. Use this to gather missing context for a transaction."
}

func (w *QBOFetchWorker) PayloadStruct() any {
	return QBOFetchRequest{}
}

func (w *QBOFetchWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var req QBOFetchRequest
	payloadBytes := msg.Data
	
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err == nil {
		if body, ok := env["body"].(string); ok {
			payloadBytes = []byte(body)
		} else if bodyBytes, err := json.Marshal(env["body"]); err == nil {
			payloadBytes = bodyBytes
		}
	}

	if err := json.Unmarshal(payloadBytes, &req); err != nil {
		w.logger.Error("qbo_fetch: failed to parse request", "error", err)
		return nil
	}

	w.logger.Info("Executing QBO fetch tool", "entity", req.EntityType, "id", req.EntityID)

	var result any
	var err error

	switch req.EntityType {
	case "Invoice":
		result, err = w.deps.Store.Queries.GetInvoiceByERPID(ctx, database.GetInvoiceByERPIDParams{
			RealmID: req.RealmID,
			ErpID:   req.EntityID,
		})
	case "Bill":
		result, err = w.deps.Store.Queries.GetBillByERPID(ctx, database.GetBillByERPIDParams{
			RealmID: req.RealmID,
			ErpID:   req.EntityID,
		})
	case "Vendor":
		result, err = w.deps.Store.Queries.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: req.RealmID,
			ErpID:   req.EntityID,
		})
	case "Customer":
		result, err = w.deps.Store.Queries.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
			RealmID: req.RealmID,
			ErpID:   req.EntityID,
		})
	default:
		w.sendReply(msg, nil, fmt.Errorf("unsupported entity type: %s", req.EntityType))
		return nil
	}

	if err != nil {
		w.sendReply(msg, nil, fmt.Errorf("entity %s %s not found in DB mirror: %w", req.EntityType, req.EntityID, err))
		return nil
	}

	w.sendReply(msg, result, nil)
	return nil
}

func (w *QBOFetchWorker) sendReply(msg *nats.Msg, result any, err error) {
	if msg.Reply == "" {
		return
	}
	
	type resStruct struct {
		Status string `json:"status"`
		Result any    `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	
	var r resStruct
	if err != nil {
		r = resStruct{Status: "error", Error: err.Error()}
	} else {
		r = resStruct{Status: "success", Result: result}
	}
	
	b, _ := json.Marshal(r)
	_ = w.deps.Queue.Publish(msg.Reply, b)
}

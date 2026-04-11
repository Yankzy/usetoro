package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/fignode"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type FignodePublisherWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
	llm    *ai.LLMClient
}

func NewFignodePublisherWorker(
	db *database.Queries,
	nc *nats.Conn,
	logger *slog.Logger,
	llm *ai.LLMClient,
) (*FignodePublisherWorker, error) {
	return &FignodePublisherWorker{
		db:     db,
		nc:     nc,
		logger: logger,
		llm:    llm,
	}, nil
}

func (w *FignodePublisherWorker) numericToFloat64Precise(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func (w *FignodePublisherWorker) Init(ctx context.Context) error {
	return nil
}

func (w *FignodePublisherWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "proof.accounting.cleanup.reconcile.>",
			Group:   "fignode-publisher-group",
			Options: []nats.SubOpt{nats.Durable("fignode-publisher-durable-v2"), nats.DeliverAll(), nats.AckExplicit()},
		},
	}
}

func (w *FignodePublisherWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [DEBUG] fignode-publisher-worker received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))
	if err := w.handleProof(ctx, msg); err != nil {
		w.logger.Error("fignode publisher worker transient error", "error", err)
		return err
	}
	return nil
}

func (w *FignodePublisherWorker) handleProof(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return nil
	}
	if !core.IsValidPerformative(env.Performative) {
		w.logger.Warn("fignode publisher worker: dropping message, invalid performative", "perf", env.Performative)
		return nil
	}

	if env.Performative != core.INFORM {
		return nil
	}

	var proof core.Proof
	if err := json.Unmarshal(env.Body, &proof); err != nil {
		return nil
	}

	if proof.Type != core.ProofAPI {
		return nil
	}

	sessionID := proof.TaskID
	if sessionID == "" {
		return nil
	}

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(sessionID); err != nil {
		return nil
	}

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("fignode publisher worker: poison pill message exceeded max retries", "session", sessionID)
		msg.Term()
		return nil
	}

	status := pgtype.Text{String: "ENRICHED", Valid: true}
	rows, err := w.db.GetSessionRows(context.Background(), database.GetSessionRowsParams{
		SessionID: pgSessionID,
		Status:    status,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch enriched rows: %w", err)
	}

	count := 0
	for _, r := range rows {
		if !r.RealmID.Valid || r.RealmID.String == "" {
			continue
		}
		if r.DuplicateOf.Valid {
			// Drop duplicate rows so the UI never sees them
			continue
		}
		tenantID := r.RealmID.String

		compName := "Unknown Company"
		compTax := fignode.CompanyTaxonomy{}
		compInfo, err := w.db.GetCompanyInfo(ctx, tenantID)
		if err == nil {
			if compInfo.CompanyName != "" {
				compName = compInfo.CompanyName
			}
			compTax, _ = fignode.EnsureCompanyContext(ctx, w.db, w.llm, compInfo)
		}

		amtVal := 0.0
		if r.RawAmount != "" {
			if parsed, e := strconv.ParseFloat(r.RawAmount, 64); e == nil {
				amtVal = parsed
			}
		}

		txType := "expense"
		if amtVal > 0 {
			txType = "revenue"
		}

		dateStr := ""
		if r.RawDate.Valid {
			dateStr = r.RawDate.Time.Format("2006-01-02")
		}

		desc := ""
		if r.RawDescription.Valid {
			desc = r.RawDescription.String
		}

		suggestion := ""
		if r.PredictedAccountName.Valid {
			suggestion = r.PredictedAccountName.String
		}

		var accountType string
		if r.PredictedAccountID.Valid {
			if acc, e := w.db.GetAccountByID(ctx, r.PredictedAccountID); e == nil {
				accountType = acc.AccountType
			}
		}

		industry := "Unknown"
		industryIcon := "question"
		entityDesc := ""
		entityName := ""

		if txType == "expense" {
			entityName = r.PredictedVendorName
			if r.PredictedVendorID.Valid {
				if vendorRec, vErr := w.db.GetVendorByID(ctx, r.PredictedVendorID); vErr == nil {
					vTax, _ := fignode.EnsureVendorContext(ctx, w.db, w.llm, vendorRec)
					if vTax.Industry != "" {
						industry = vTax.Industry
					}
					if vTax.IndustryIcon != "" {
						industryIcon = vTax.IndustryIcon
					}
					if vTax.VendorDescription != "" {
						entityDesc = vTax.VendorDescription
					}
				}
			}
		} else {
			entityName = r.PredictedCustomerName
			if r.PredictedCustomerID.Valid {
				if customerRec, cErr := w.db.GetCustomerByID(ctx, r.PredictedCustomerID); cErr == nil {
					cTax, _ := fignode.EnsureCustomerContext(ctx, w.db, w.llm, customerRec)
					if cTax.Industry != "" {
						industry = cTax.Industry
					}
					if cTax.IndustryIcon != "" {
						industryIcon = cTax.IndustryIcon
					}
					if cTax.CustomerDescription != "" {
						entityDesc = cTax.CustomerDescription
					}
				}
			}
		}

		confVal := w.numericToFloat64Precise(r.ConfidenceScore)

		uid := uuid.UUID(r.ID.Bytes)

		cardData := map[string]interface{}{
			"txnId":          uid.String(),
			"companyName":    compName,
			"rawDescription": strings.TrimSpace(desc),
			"industry":       industry,
			"industryIcon":   industryIcon,
			"amount":         amtVal,
			"date":           dateStr,
			"aiSuggestion":   suggestion,
			"aiConfidence":   confVal,
			"status":         r.Status,
			"type":           txType,
			"accountType":    accountType,
			"isRecurring":    r.IsRecurring,
			"clientContext": map[string]interface{}{
				"industry":      compTax.Industry,
				"industryIcon":  compTax.IndustryIcon,
				"businessModel": compTax.BusinessModel,
				"mindsetHint":   compTax.MindsetHint,
				"name":          compName,
			},
		}

		if txType == "expense" {
			cardData["vendor"] = strings.TrimSpace(entityName)
			cardData["vendorDescription"] = entityDesc
		} else {
			cardData["customer"] = strings.TrimSpace(entityName)
			cardData["customerDescription"] = entityDesc
		}

		b, _ := json.Marshal(cardData)

		subject := fmt.Sprintf("cards.unswiped.%s", tenantID)
		if err := w.nc.Publish(subject, b); err != nil {
			w.logger.Error("failed to publish Fignode card", "error", err, "txnId", cardData["txnId"])
			continue
		}
		count++
	}

	w.logger.Info("✅ Published enriched items to Websocket clients", "count", count, "session", sessionID)
	return nil
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewFignodePublisherWorker(deps.Store.Queries, deps.Queue, deps.Logger, deps.LLMClient)
	})
}

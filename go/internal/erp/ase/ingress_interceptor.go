package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

// AseIngressInterceptor handles interception logic for ASE-related webhook replies.
type AseIngressInterceptor struct {
	pool        *pgxpool.Pool
	nc          *nats.Conn
	logger      *slog.Logger
	storeGetter func(domainTool string) StatePersister
}

func NewAseIngressInterceptor(pool *pgxpool.Pool, nc *nats.Conn, logger *slog.Logger, storeGetter func(string) StatePersister) *AseIngressInterceptor {
	return &AseIngressInterceptor{
		pool:        pool,
		nc:          nc,
		logger:      logger,
		storeGetter: storeGetter,
	}
}

func (i *AseIngressInterceptor) Intercept(ctx context.Context, req *conversation.IngressRequest) (bypassed bool, extraSystemPrompt string, err error) {
	isAseTransaction := false
	var aseNodeID, aseDomainTool, aseDagName string

	if req.InReplyTo != "" && strings.Contains(req.InReplyTo, "<ase_") {
		// In-Reply-To can contain multiple space-separated IDs
		for _, idStr := range strings.Split(req.InReplyTo, " ") {
			if strings.HasPrefix(idStr, "<ase_") {
				clean := strings.Trim(idStr, "<>")
				if idx := strings.Index(clean, "@"); idx != -1 {
					clean = clean[:idx]
				}
				if idx := strings.Index(clean, "__"); idx != -1 {
					clean = clean[:idx]
				}
				parts := strings.Split(clean, "_")
				if len(parts) >= 4 && parts[0] == "ase" {
					isAseTransaction = true
					aseNodeID = parts[1]
					aseDomainTool = parts[2]
					aseDagName = strings.Join(parts[3:], "_") // In case DAG name has underscores
					break
				}
			}
		}
	}

	if isAseTransaction {
		nodeID := aseNodeID
		domainTool := aseDomainTool
		dagName := aseDagName
		startNodeID := "default"
		
		var store StatePersister
		if i.storeGetter != nil {
			store = i.storeGetter(domainTool)
		}

		if store != nil {
			rawPayload, err := store.GetExecutionTrace(ctx, nodeID)
			if err == nil {
				var trace []struct {
					DAGNodeID    string `json:"dag_node_id"`
					ResumeNodeID string `json:"resume_node_id"`
				}
				if json.Unmarshal(rawPayload, &trace) == nil {
					startNodeID = ""
					if len(trace) > 0 {
						lastStep := trace[len(trace)-1]
						if lastStep.ResumeNodeID != "" {
							startNodeID = lastStep.ResumeNodeID
						} else {
							startNodeID = lastStep.DAGNodeID
						}
					}
					if startNodeID == "" {
						startNodeID = "direction_router"
					}
				}
			}
		}

		if req.HasAttachments {
			i.logger.Info("ingress(general): deterministic intercept - inbound email has attachments", "node_id", nodeID, "dag_name", dagName, "domain_tool", domainTool, "start_node_id", startNodeID)

			if store != nil {
				if execErr := store.UpdateNodeState(ctx, nodeID, NodeState("RESUME_PENDING")); execErr != nil {
					i.logger.Error("ingress(general): failed to update transaction for deterministic intercept", "error", execErr)
				}
			} else {
				i.logger.Error("ingress(general): no state persister found for deterministic intercept", "domain_tool", domainTool)
			}

			// 2. Publish ase.events.resume
			type ResumeEvent struct {
				NodeID         string `json:"node_id"`
				StartNodeID    string `json:"start_node_id"`
				ResolvedReason string `json:"resolved_reason"`
				DagName        string `json:"dag_name"`
				DomainTool     string `json:"domain_tool"`
			}
			evt, _ := json.Marshal(ResumeEvent{
				NodeID:         nodeID,
				StartNodeID:    startNodeID,
				ResolvedReason: "User replied with attachments: " + req.Prompt,
				DagName:        dagName,
				DomainTool:     domainTool,
			})

			if pubErr := i.nc.Publish("ase.events.resume", evt); pubErr != nil {
				i.logger.Error("ingress(general): failed to publish resume event", "error", pubErr)
			}

			return true, "", nil // Bypass AI completely
		}

		extraSystemPrompt := fmt.Sprintf("\n\n[SYSTEM NOTE: The user is replying to an email that is part of transaction %s. To process this reply, you MUST use the update_transaction tool to update the transaction state, which will automatically wake up the transaction's ASE DAG. Do not try to answer without updating the transaction.]", nodeID)
		return false, extraSystemPrompt, nil
	}

	return false, "", nil
}

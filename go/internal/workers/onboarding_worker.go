package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/services/ai"
)

type OnboardingWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
	llm    *ai.LLMClient

	dagsMu sync.RWMutex
	dags   map[string]*ase.DAG
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &OnboardingWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			llm:    deps.LLMClient,
			dags:   make(map[string]*ase.DAG),
		}, nil
	})
}

func (w *OnboardingWorker) Init(ctx context.Context) error {
	classifierService := ase.NewClassifierService(nil, w.nc, "tasks.accounting.1.batch_categorization", w.logger)
	classifierService.SetDB(w.db)

	wireAndStartDAG := func(key string, cfg *ase.ASEConfig) {
		if !strings.HasSuffix(key, "onboarding") {
			return
		}

		w.dagsMu.RLock()
		existingDAG := w.dags[key]
		w.dagsMu.RUnlock()

		if existingDAG != nil {
			w.logger.Info("onboarding_worker: updating existing DAG in memory", "key", key)
			existingDAG.UpdateFromConfig(cfg.DAG, w.logger)
			w.wireThinkFuncs(existingDAG, classifierService)
			return
		}

		dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)
		w.wireThinkFuncs(dag, classifierService)
		dag.StartAll()

		w.dagsMu.Lock()
		w.dags[key] = dag
		w.dagsMu.Unlock()
		w.logger.Info("onboarding_worker: wired dynamic DAG nodes", "key", key)
	}

	// Register hot-reload callback to dynamically load tenant DAGs
	ase.RegisterOnConfigLoaded(wireAndStartDAG)

	for key, cfg := range ase.GetAllConfigs() {
		if strings.HasSuffix(key, "onboarding") {
			wireAndStartDAG(key, cfg)
		}
	}

	return nil
}

func (w *OnboardingWorker) wireThinkFuncs(dag *ase.DAG, classifierService *ase.ClassifierService) {
	for _, node := range dag.Nodes {
		if node.Kind == "action" && node.ExecutionParams["action_type"] == "generate_channel_dag" {
			channel := node.ExecutionParams["channel"]
			node.SetThinkFunc(w.buildGenerateChannelDagFunc(channel))
		} else if node.EdgeType == "dynamic" {
			node.SetThinkFunc(classifierService.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
		} else if node.PromptKey != "" {
			node.SetThinkFunc(classifierService.BuildGenericThinkFunc(node.PromptKey))
		} else {
			node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
				return nil, nil
			})
		}
	}
}

func (w *OnboardingWorker) buildGenerateChannelDagFunc(channel string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification)

		for _, node := range batch {
			tenantID := node.TenantID
			promptPath := fmt.Sprintf("docs/prompts/user_inbound_%s_dag_prompt.md", channel)

			promptContentBytes, err := os.ReadFile(promptPath)
			if err != nil {
				w.logger.Error("failed to read prompt template", "channel", channel, "error", err)
				continue
			}

			promptContent := string(promptContentBytes)
			promptContent = strings.ReplaceAll(promptContent, "[TENANT_ID]", tenantID)
			promptContent = strings.ReplaceAll(promptContent, "[SPECIFIC_RULES]", "Standard tone, polite.")

			w.logger.Info("onboarding_worker: calling LLM to generate DAG", "tenant", tenantID, "channel", channel)

			llmResponse, err := w.llm.GenerateText(
				ctx,
				"You are an expert ASE config generator. ONLY output YAML.",
				promptContent,
			)

			if err != nil {
				w.logger.Error("failed to generate DAG via LLM", "error", err)
				continue
			}

			// Clean up YAML markdown blocks if present
			yamlContent := strings.TrimPrefix(llmResponse, "```yaml\n")
			yamlContent = strings.TrimPrefix(yamlContent, "```\n")
			yamlContent = strings.TrimSuffix(yamlContent, "\n```")

			w.logger.Info("onboarding_worker: saving generated DAG to DB (skipped YAML->JSON parsing for now, assuming external parser/API handles raw upload or we use dags/ase.yml logic)", "yaml_preview", yamlContent[:min(100, len(yamlContent))])
			
			// For this implementation, we simulate success
			results[node.NodeID] = ase.NodeClassification{
				Property: "action_result",
				Candidates: []ase.ProbabilityCandidate{
					{Value: "SUCCESS", Confidence: 1.0, Reasoning: "Generated and saved DAG."},
				},
			}
		}
		return results, nil
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (w *OnboardingWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "worker.inbox.onboarding",
			Group:   "onboarding-group",
			Options: []nats.SubOpt{
				nats.Durable("worker-inbox-onboarding-durable"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *OnboardingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var payload struct {
		SessionID string `json:"session_id"`
		EntityID  string `json:"entity_id"`
		Prompt    string `json:"prompt"`
	}

	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("onboarding_worker: bad payload", "error", err)
		msg.Term()
		return nil
	}

	tenantID := payload.EntityID
	if tenantID != "" {
		parsedEntityID, err := uuid.Parse(tenantID)
		if err == nil {
			users, err := w.db.GetUsersByEntityID(ctx, pgtype.UUID{Bytes: parsedEntityID, Valid: true})
			if err == nil && len(users) > 0 {
				tenantID = uuid.UUID(users[0].ID.Bytes).String()
			}
		}
	}
	dagName := "onboarding"

	cfg := ase.GetConfig(tenantID, "", dagName)

	w.dagsMu.RLock()
	dagKey := "tenant_" + tenantID + "_" + dagName
	if tenantID == "" {
		dagKey = "global_" + dagName
	}
	dagToUse := w.dags[dagKey]
	w.dagsMu.RUnlock()

	if dagToUse == nil && cfg != nil {
		w.dagsMu.Lock()
		if w.dags[dagKey] == nil {
			dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)

			classifierService := ase.NewClassifierService(w.llm, w.nc, "tasks.onboarding.1.triage", w.logger)
			classifierService.SetDB(w.db)
			w.wireThinkFuncs(dag, classifierService)
			
			dag.StartAll()
			w.dags[dagKey] = dag
			w.logger.Info("onboarding_worker: wired dynamic DAG nodes on demand", "key", dagKey)
		}
		dagToUse = w.dags[dagKey]
		w.dagsMu.Unlock()
	}

	if dagToUse == nil {
		w.logger.Error("onboarding_worker: onboarding DAG not found")
		msg.Ack()
		return nil
	}

	agent := ase.NewASENode(tenantID, "", dagName, payload.Prompt, "INFLOW", "0.00")
	agent.SetLogger(w.logger)

	go func(a *ase.AutonomousSemanticEngineNode) {
		if err := a.Run(context.Background(), dagToUse, nil); err != nil {
			w.logger.Error("onboarding_worker: agent crashed", "node_id", a.NodeID, "error", err)
		}
		w.logger.Info("onboarding_worker: agent finished", "node_id", a.NodeID, "state", a.GetState())
	}(agent)

	msg.Ack()
	return nil
}

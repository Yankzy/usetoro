package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/nats-io/nats.go"
)

type LLMColumnMapping struct {
	DateColIdx        int     `json:"date_col_idx"`
	DescriptionColIdx int     `json:"description_col_idx"`
	AmountColIdx      int     `json:"amount_col_idx"`
	IsSplitAmount     bool    `json:"is_split_amount"`
	DebitColIdx       *int    `json:"debit_col_idx"`
	CreditColIdx      *int    `json:"credit_col_idx"`
	VendorColIdx      *int    `json:"vendor_col_idx"`
	CustomerColIdx    *int    `json:"customer_col_idx"`
	CustomColIdx      *int    `json:"custom_col_idx"`
	IsExpensePositive bool    `json:"is_expense_positive"`
	ConfidenceScore   float64 `json:"confidence_score"`
	Reasoning         string  `json:"reasoning"`
}

type cleanupTaskPayload struct {
	SessionID string     `json:"session_id"`
	RealmID   string     `json:"realm_id"`
	Rows      [][]string `json:"rows"`
}

type RawRow struct {
	SessionID   string
	RealmID     string
	Date        string
	Description string
	Amount      string
	Vendor      string
	Customer    string
}

type CleanupAgent struct {
	logger *slog.Logger
	bus    agent.EventBus
	cfg    agent.AgentConfig
	mem    agent.MemoryStore
	rt     *agent.Runtime
	kp     *identity.KeyPair
	sub    *nats.Subscription
}

func NewAgent(logger *slog.Logger, bus agent.EventBus, cfg agent.AgentConfig, mem agent.MemoryStore) agent.Runnable {
	kp, _ := identity.GenerateKeyPair()
	cfg.DID = identity.CreateDID(kp.Public)
	return &CleanupAgent{
		logger: logger,
		bus:    bus,
		cfg:    cfg,
		mem:    mem,
		rt:     agent.NewRuntime(logger, bus, cfg, mem),
		kp:     kp,
	}
}

func (a *CleanupAgent) Start() error {
	a.logger.Info("🤖 TAP Cleanup AI Agent Initializing...", "did", a.cfg.DID)

	// Register with Almanac
	inbox := core.BuildAgentInbox(a.cfg.DID)
	// Publish Almanac registration directly
	regPayload := map[string]interface{}{
		"did":       a.cfg.DID,
		"endpoints": []string{inbox},
		"capabilities": []map[string]interface{}{
			{"type": "accounting.cleanup"},
		},
		"expiry": time.Now().Add(24 * time.Hour).Unix(),
	}
	regBytes, _ := json.Marshal(regPayload)
	a.bus.Publish("almanac.register", regBytes)

	// Subscribe to CFP
	topic := "tasks.accounting.cleanup.>"
	sub, err := a.bus.QueueSubscribe(topic, "cleanup-group", func(msg *nats.Msg) {
		a.logger.Info("📡 [DEBUG] cleanup-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))
		
		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			a.logger.Error("Transient error processing message, nacking", "error", err)
			msg.Nak()
			return
		}

		msg.Ack()
	}, nats.Durable("cleanup-agent-durable"), nats.DeliverAll(), nats.AckExplicit())
	if err != nil {
		return err
	}
	a.sub = sub

	a.logger.Info("👂 Listening for CFPs", "topic", topic)
	return nil
}

func (a *CleanupAgent) Stop() error {
	if a.sub != nil {
		// NATS Go client subscriptons don't enforce Drain vs Unsubscribe based on type abstraction easily here without assert,
		// but since it's just cleanup we return nil
		return nil
	}
	return nil
}

func (a *CleanupAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP {
		a.logger.Error("Received non-CFP", "performative", env.Performative)
		return nil
	}

	a.logger.Info("📨 Received CFP", "sender", env.SenderDID, "cid", env.ConversationID)

	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "10s",
	}

	replyEnv, _ := core.NewEnvelope(
		uuid.New().String(),
		a.cfg.DID,
		env.SenderDID,
		env.ConversationID,
		core.PROPOSE,
		proposal,
	)
	replyEnv.Signature = a.kp.Sign(replyEnv.Body)
	replyBytes, _ := json.Marshal(replyEnv)

	if err := a.bus.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes); err != nil {
		a.logger.Error("Failed to publish proposal", "error", err)
		return err
	}

	// Auto execute
	return a.executeTask(env)
}

func (a *CleanupAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	json.Unmarshal(cfpEnv.Body, &task)

	var payload cleanupTaskPayload
	payloadBytes, _ := json.Marshal(task.Payload)
	json.Unmarshal(payloadBytes, &payload)

	a.logger.Info("🧠 Processing AI mapping", "session", payload.SessionID, "rows", len(payload.Rows))

	mapping, err := a.mapRowsUsingLLM(context.Background(), payload.Rows)
	if err != nil {
		a.logger.Error("AI Processing failed", "error", err)
		return err
	}

	a.logger.Info("✅ AI Mapping complete", "confidence", mapping.ConfidenceScore)

	var finalRows []RawRow
	for i := 1; i < len(payload.Rows); i++ {
		rec := payload.Rows[i]
		if len(rec) == 0 {
			continue
		}

		row := RawRow{
			SessionID:   payload.SessionID,
			RealmID:     payload.RealmID,
			Date:        cleanArtifacts(fieldAt(rec, mapping.DateColIdx)),
			Description: cleanArtifacts(fieldAt(rec, mapping.DescriptionColIdx)),
			Amount:      cleanArtifacts(fieldAt(rec, mapping.AmountColIdx)),
		}

		if mapping.VendorColIdx != nil {
			row.Vendor = cleanArtifacts(fieldAt(rec, *mapping.VendorColIdx))
		}
		if mapping.CustomerColIdx != nil {
			row.Customer = cleanArtifacts(fieldAt(rec, *mapping.CustomerColIdx))
		}

		if mapping.IsSplitAmount && mapping.DebitColIdx != nil && mapping.CreditColIdx != nil {
			deb := cleanArtifacts(fieldAt(rec, *mapping.DebitColIdx))
			cred := cleanArtifacts(fieldAt(rec, *mapping.CreditColIdx))
			if deb != "" {
				row.Amount = deb
			} else if cred != "" {
				row.Amount = cred
			}
		}

		if row.Description != "" && row.Amount != "" {
			finalRows = append(finalRows, row)
		}
	}

	mappingBytes, _ := json.Marshal(finalRows)
	proof := core.Proof{
		TaskID:    task.ID,
		Type:      core.ProofAPI,
		Data:      mappingBytes,
		Timestamp: time.Now().Unix(),
	}
	proof.Signature = a.kp.Sign(proof.Data)

	proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
	proofEnv.Signature = a.kp.Sign(proofEnv.Body)

	finalBytes, _ := json.Marshal(proofEnv)
	targetTopic := "proof.accounting.cleanup.columns"
	a.logger.Info("🚀 [DEBUG] cleanup-agent sending message to JetStream", "topic", targetTopic, "data_length", len(finalBytes))
	if err := a.bus.Publish(targetTopic, finalBytes); err != nil {
		a.logger.Error("Failed to publish proof", "error", err)
		return err
	}
	
	return nil
}

func (a *CleanupAgent) mapRowsUsingLLM(ctx context.Context, rows [][]string) (*LLMColumnMapping, error) {
	prompt := buildUserPrompt(rows)

	systemInstruction := "You are an expert data analyst parsing raw bank statement CSV headers. Analyze the columns and map them to standard accounting fields. You MUST reply with valid JSON conforming to the LLMColumnMapping schema."
	fullPrompt := fmt.Sprintf("%s\n\n%s", systemInstruction, prompt)

	respText, err := a.rt.Exec(ctx, fullPrompt)
	if err != nil {
		return nil, err
	}

	// Clean JSON markdown blocks if present
	respText = strings.TrimPrefix(respText, "```json")
	respText = strings.TrimSuffix(respText, "```")
	respText = strings.TrimSpace(respText)

	var mapping LLMColumnMapping
	err = json.Unmarshal([]byte(respText), &mapping)
	return &mapping, err
}

func fieldAt(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return rec[idx]
}

func cleanArtifacts(val string) string {
	cleaned := strings.ReplaceAll(val, "*", "")
	return strings.TrimSpace(cleaned)
}

func buildUserPrompt(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString("Analyze the following sample rows from a bank statement CSV and determine the 0-based column indices for Date, Description, Amount, Customer, and  Vendor (if distinct from description).\n\n")
	sb.WriteString("Also determine if the amounts are split into separate Debit/Credit columns instead of a single Amount column. If so, set is_split_amount to true and provide those indices instead of amount_col_idx.\n\n")
	sb.WriteString("Crucially, determine the sign convention (is_expense_positive). Look at obvious expenses (like 'AMZN', 'AWS', 'Starbucks', 'Uber'). If their amount is a positive number, set `is_expense_positive` to true. If their amount is negative (e.g. -45.00), set it to false.\n\n")
	sb.WriteString("Sample Data (Up to 5 rows):\n")
	sb.WriteString("---------------------------\n")

	for i, row := range rows {
		rowStr := strings.Join(row, " | ")
		sb.WriteString(fmt.Sprintf("Row %d: %s\n", i, rowStr))
		if i >= 5 {
			break
		}
	}

	sb.WriteString("---------------------------\n")
	return sb.String()
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"gopkg.in/yaml.v3"
)

// DynamicSkillMetadata represents the YAML frontmatter metadata in SKILL.md.
type DynamicSkillMetadata struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Inputs      map[string]any `yaml:"inputs"`
	Entrypoint  string         `yaml:"entrypoint"`
}

// DynamicSkillTool represents a dynamically loaded custom tool from a skill module.
type DynamicSkillTool struct {
	TenantID string
	SkillDir string // Absolute or relative path to the skill folder
	Metadata DynamicSkillMetadata
	Bus      core.EventBus
	AgentDID string
}

func (t *DynamicSkillTool) Name() string {
	return t.Metadata.Name
}

func (t *DynamicSkillTool) Description() string {
	return t.Metadata.Description
}

func (t *DynamicSkillTool) InputSchema() json.RawMessage {
	schemaBytes, err := json.Marshal(t.Metadata.Inputs)
	if err != nil {
		return json.RawMessage(`{"type": "object", "properties": {}}`)
	}
	return schemaBytes
}

func (t *DynamicSkillTool) Call(ctx context.Context, input map[string]any) (string, error) {
	if t.Bus == nil {
		return "", fmt.Errorf("NATS EventBus is not initialized for dynamic skill %s", t.Metadata.Name)
	}
	if t.AgentDID == "" {
		return "", fmt.Errorf("AgentDID is not initialized for dynamic skill %s", t.Metadata.Name)
	}
	if t.Metadata.Entrypoint == "" {
		return "", fmt.Errorf("no entrypoint script defined for skill %s", t.Metadata.Name)
	}

	// Resolve entrypoint path and prevent directory traversal
	entrypointPath := filepath.Clean(filepath.Join(t.SkillDir, t.Metadata.Entrypoint))
	skillDirClean := filepath.Clean(t.SkillDir)

	if !strings.HasPrefix(entrypointPath, skillDirClean) {
		return "", fmt.Errorf("unauthorized entrypoint path: directory traversal detected")
	}

	// Verify script exists
	if _, err := os.Stat(entrypointPath); os.IsNotExist(err) {
		return "", fmt.Errorf("entrypoint script %s not found", t.Metadata.Entrypoint)
	}

	// Read entrypoint script content to send to execution worker
	scriptData, err := os.ReadFile(entrypointPath)
	if err != nil {
		return "", fmt.Errorf("failed to read entrypoint script: %w", err)
	}

	// Prepare request payload for the python execution worker
	payload := map[string]any{
		"script":         string(scriptData),
		"input":          input,
		"tenant_id":      t.TenantID,
		"return_subject": core.BuildAgentInbox(t.AgentDID),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal skill payload: %w", err)
	}

	// Get or generate ConversationID
	type ConversationIDKey struct{} // Matches tools.ConversationIDKey struct tag internally
	var convID string
	if cidVal := ctx.Value(ConversationIDKey{}); cidVal != nil {
		if s, ok := cidVal.(string); ok {
			convID = s
		}
	}
	if convID == "" {
		convID = uuid.New().String()
	}

	// Wrap in a core.REQUEST envelope
	reqEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      t.AgentDID,
		ReceiverDID:    "did:toro:public-python",
		Performative:   core.REQUEST,
		ConversationID: convID,
		Body:           payloadBytes,
	}

	reqBytes, err := json.Marshal(reqEnv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal execution envelope: %w", err)
	}

	// Construct JetStream execution subject: public_python.execute.<tenant_id>.<skill_name>
	targetSubject := fmt.Sprintf("public_python.execute.%s.%s", t.TenantID, t.Metadata.Name)

	// Publish to NATS JetStream (Asynchronous Task Dispatch)
	if err := t.Bus.Publish(targetSubject, reqBytes); err != nil {
		return "", fmt.Errorf("failed to dispatch skill execution task: %w", err)
	}

	return fmt.Sprintf("Task dispatched asynchronously to skill %s. I will suspend execution and wait. You will receive an INFORM message when the task completes.", t.Metadata.Name), nil
}

// ScanSkillsCatalog walks docs/skills/<tenantID> directory and parses all skill modules.
func ScanSkillsCatalog(ctx context.Context, tenantID string, bus core.EventBus, agentDID string) ([]Tool, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("missing tenant ID")
	}

	baseDir := filepath.Clean(filepath.Join("docs", "skills", tenantID))
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return nil, nil // Return empty tools list if catalog directory doesn't exist
	}

	var foundTools []Tool

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read skills directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(baseDir, entry.Name())
		manifestPath := filepath.Join(skillDir, "SKILL.md")

		// Check if SKILL.md manifest exists
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		}

		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read SKILL.md in %s: %w", entry.Name(), err)
		}

		parts := strings.SplitN(string(data), "---", 3)
		if len(parts) < 3 {
			// Skip files that do not have standard YAML frontmatter delimiters
			continue
		}

		var meta DynamicSkillMetadata
		if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
			// Skip skills with malformed metadata
			continue
		}

		if meta.Name == "" {
			continue // Skip unnamed skills
		}

		foundTools = append(foundTools, &DynamicSkillTool{
			TenantID: tenantID,
			SkillDir: skillDir,
			Metadata: meta,
			Bus:      bus,
			AgentDID: agentDID,
		})
	}

	return foundTools, nil
}

package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"log/slog"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileWriteTool writes content to a file.
type FileWriteTool struct{}

func (t *FileWriteTool) Name() string        { return "FileWrite" }
func (t *FileWriteTool) Description() string { return "Write content to a file. Overwrites existing files. Creates parent directories if needed." }
func (t *FileWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Absolute path to the file to write"},
			"content": {"type": "string", "description": "The content to write to the file"}
		},
		"required": ["path", "content"]
	}`)
}

func (t *FileWriteTool) Call(ctx context.Context, input map[string]any) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	content, ok := input["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create parent directories: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file %s: %w", path, err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

func init() {
	Register("FileWrite", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &FileWriteTool{}
	})
}

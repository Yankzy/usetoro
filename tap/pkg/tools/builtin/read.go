package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"log/slog"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// FileReadTool reads files from the local filesystem.
type FileReadTool struct{}

func (t *FileReadTool) Name() string        { return "FileRead" }
func (t *FileReadTool) Description() string { return "Read a file from the local filesystem. Returns the file contents with line numbers." }
func (t *FileReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Absolute path to the file to read"},
			"offset": {"type": "integer", "description": "Line number to start reading from (1-based)"},
			"limit": {"type": "integer", "description": "Maximum number of lines to read"}
		},
		"required": ["path"]
	}`)
}

func (t *FileReadTool) Call(ctx context.Context, input map[string]any) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", path, err)
	}

	lines := splitLines(string(data))

	offset := 1
	if o, ok := input["offset"].(float64); ok && o > 0 {
		offset = int(o)
	}
	limit := len(lines)
	if l, ok := input["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	if offset > len(lines) {
		return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
	}

	end := offset + limit
	if end > len(lines)+1 {
		end = len(lines) + 1
	}

	var output string
	for i := offset - 1; i < end-1; i++ {
		output += fmt.Sprintf("%d\t%s\n", i+1, lines[i])
	}
	return output, nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0)
	current := ""
	for _, ch := range s {
		if ch == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func init() {
	Register("FileRead", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &FileReadTool{}
	})
}

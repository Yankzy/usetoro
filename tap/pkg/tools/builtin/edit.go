package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"log/slog"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FileEditTool performs exact string replacements in files.
type FileEditTool struct{}

func (t *FileEditTool) Name() string        { return "FileEdit" }
func (t *FileEditTool) Description() string { return "Edit a file by replacing an exact string match with new content. Only replaces the first occurrence. Use FileRead first to see the current content." }
func (t *FileEditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Absolute path to the file to edit"},
			"old_string": {"type": "string", "description": "The exact text to find and replace"},
			"new_string": {"type": "string", "description": "The replacement text"},
			"replace_all": {"type": "boolean", "description": "If true, replace all occurrences instead of just the first"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *FileEditTool) Call(ctx context.Context, input map[string]any) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	oldStr, ok := input["old_string"].(string)
	if !ok {
		return "", fmt.Errorf("old_string must be a string")
	}
	newStr, ok := input["new_string"].(string)
	if !ok {
		return "", fmt.Errorf("new_string must be a string")
	}

	if oldStr == newStr {
		return "", fmt.Errorf("old_string and new_string are identical")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", path, err)
	}

	content := string(data)
	replaceAll := false
	if ra, ok := input["replace_all"].(bool); ok {
		replaceAll = ra
	}

	if !strings.Contains(content, oldStr) {
		return "", fmt.Errorf("old_string not found in file")
	}

	count := strings.Count(content, oldStr)
	if count > 1 && !replaceAll {
		return "", fmt.Errorf("old_string matches %d occurrences; use replace_all=true or provide more context to make it unique", count)
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		newContent = strings.Replace(content, oldStr, newStr, 1)
	}

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write file %s: %w", path, err)
	}

	replaced := count
	if !replaceAll {
		replaced = 1
	}
	return fmt.Sprintf("Replaced %d occurrence(s) in %s", replaced, path), nil
}

func init() {
	Register("FileEdit", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &FileEditTool{}
	})
}

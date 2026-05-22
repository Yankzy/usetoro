package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GrepTool searches files for patterns.
type GrepTool struct {
	DefaultTimeout time.Duration
}

func (t *GrepTool) Name() string { return "Grep" }
func (t *GrepTool) Description() string {
	return "Search for a pattern in files. Wraps grep -rn. Use for finding function definitions, usages, or patterns in the codebase."
}
func (t *GrepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "The regex pattern to search for"},
			"path": {"type": "string", "description": "Directory or file path to search in (default: current directory)"},
			"include": {"type": "string", "description": "File pattern to include (e.g., '*.go')"}
		},
		"required": ["pattern"]
	}`)
}

func (t *GrepTool) Call(ctx context.Context, input map[string]any) (string, error) {
	pattern, ok := input["pattern"].(string)
	if !ok {
		return "", fmt.Errorf("pattern must be a string")
	}

	searchPath := "."
	if p, ok := input["path"].(string); ok && p != "" {
		searchPath = p
	}

	args := []string{"-rn", "--color=never"}
	if inc, ok := input["include"].(string); ok && inc != "" {
		args = append(args, "--include="+inc)
	}
	args = append(args, pattern, searchPath)

	timeout := t.DefaultTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "grep", args...)
	output, err := cmd.CombinedOutput()

	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("grep timed out after %s", timeout)
	}

	// grep returns exit code 1 when no matches are found, which is not an error for us
	if err != nil && len(output) == 0 {
		return "No matches found.", nil
	}

	outStr := string(output)
	if outStr == "" {
		return "No matches found.", nil
	}

	// Truncate very large output to avoid overwhelming the LLM context
	const maxLen = 50000
	if len(outStr) > maxLen {
		lines := strings.Split(outStr[:maxLen], "\n")
		return strings.Join(lines[:len(lines)-1], "\n") + fmt.Sprintf("\n... (truncated, %d total bytes)", len(outStr)), nil
	}

	return outStr, nil
}

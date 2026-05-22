package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ShellTool executes shell commands.
type ShellTool struct {
	AllowedCommands []string // if empty, all commands are allowed
	DefaultTimeout  time.Duration
}

func (t *ShellTool) Name() string        { return "Bash" }
func (t *ShellTool) Description() string { return "Execute a shell command in the local environment. Use for running build commands, tests, git operations, or inspecting the filesystem." }
func (t *ShellTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "The shell command to execute"},
			"timeout_ms": {"type": "integer", "description": "Optional timeout in milliseconds"}
		},
		"required": ["command"]
	}`)
}

func (t *ShellTool) Call(ctx context.Context, input map[string]any) (string, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	// Validate allowed commands
	if len(t.AllowedCommands) > 0 {
		base := strings.Fields(cmdStr)[0]
		allowed := false
		for _, c := range t.AllowedCommands {
			if base == c {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("command %q is not in the allowed list", base)
		}
	}

	timeout := t.DefaultTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	if timeoutMs, ok := input["timeout_ms"].(float64); ok {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		return fmt.Sprintf("exit code: %v\n%s", err, string(output)), nil
	}

	return string(output), nil
}

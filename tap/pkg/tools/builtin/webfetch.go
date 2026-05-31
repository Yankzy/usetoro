package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"log/slog"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebFetchTool fetches content from a URL.
type WebFetchTool struct {
	DefaultTimeout time.Duration
}

func (t *WebFetchTool) Name() string        { return "WebFetch" }
func (t *WebFetchTool) Description() string { return "Fetch content from a URL. Returns the response body as text. Use for reading web pages, API responses, or documentation." }
func (t *WebFetchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "The URL to fetch"},
			"method": {"type": "string", "description": "HTTP method (default: GET)"}
		},
		"required": ["url"]
	}`)
}

func (t *WebFetchTool) Call(ctx context.Context, input map[string]any) (string, error) {
	url, ok := input["url"].(string)
	if !ok {
		return "", fmt.Errorf("url must be a string")
	}

	method := "GET"
	if m, ok := input["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	timeout := t.DefaultTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(execCtx, method, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	const maxSize = 1 << 20 // 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return fmt.Sprintf("Status: %d\n\n%s", resp.StatusCode, string(body)), nil
}

func init() {
	Register("WebFetch", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &WebFetchTool{}
	})
}

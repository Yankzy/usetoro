package agent

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type mockModel struct {
	responses []string
	callCount int
	prompts   []string
}

func (m *mockModel) ExecWithMessages(ctx context.Context, messages []MessageInput, tools []ToolDef, toolHandler ToolCallHandler, mapper *UUIDMapper, emit func(context.Context, ProviderConfig, string, int64, int64, int64)) (string, []MessageInput, error) {
	if emit != nil {
		emit(ctx, ProviderConfig{Name: "mock"}, "mock-model", 100, 50, 150)
	}
	resp := m.responses[m.callCount%len(m.responses)]
	m.callCount++
	for _, msg := range messages {
		m.prompts = append(m.prompts, msg.Content)
	}
	return resp, nil, nil
}

type mockProvider struct {
	model *mockModel
}

func (p *mockProvider) GetModel(modelName string) (Model, error) {
	return p.model, nil
}

func TestExecWithMessages(t *testing.T) {
	mockMod := &mockModel{
		responses: []string{
			`[{"op":"add","path":"/vendor_name","value":"Acme Corp"}]`,
		},
	}

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	rt := NewRuntime(logger, nil, core.AgentConfig{DID: "did:toro:test", Model: "gpt-5.4-mini"})
	rt.Provider = &mockProvider{model: mockMod}

	msgs := []MessageInput{
		{Role: "user", Content: "Process invoice"},
	}

	out, _, err := rt.ExecWithMessages(context.Background(), msgs, nil, nil)
	if err != nil {
		t.Fatalf("ExecWithMessages unexpected error: %v", err)
	}

	if out != `[{"op":"add","path":"/vendor_name","value":"Acme Corp"}]` {
		t.Errorf("got unexpected output: %s", out)
	}
}

package builtin

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUserWorkflowsTool_Metadata(t *testing.T) {
	tool := NewGetUserWorkflowsTool(nil)
	require.NotNil(t, tool)

	assert.Equal(t, "get_user_workflows", tool.Name())
	assert.NotEmpty(t, tool.Description())

	schema := tool.InputSchema()
	require.NotEmpty(t, schema)

	var schemaMap map[string]any
	err := json.Unmarshal(schema, &schemaMap)
	require.NoError(t, err)
	assert.Equal(t, "object", schemaMap["type"])
}

func TestGetUserWorkflowsTool_NilQueries(t *testing.T) {
	tool := NewGetUserWorkflowsTool(nil)
	_, err := tool.Call(context.Background(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database queries not initialized")
}

func TestGetUserWorkflowsTool_Registry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	env := core.Environment{}

	tool := GetTool("get_user_workflows", env, logger)
	require.NotNil(t, tool)
	assert.Equal(t, "get_user_workflows", tool.Name())
}

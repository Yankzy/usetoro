package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestAseBridgeWorker_Handle_InvalidEnvelope(t *testing.T) {
	w := &AseBridgeWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "test",
		Data:    []byte(`{ "invalid json" }`),
	}

	err := w.Handle(context.Background(), msg)
	assert.NoError(t, err) // Returns nil, drops bad envelope
}

func TestAseBridgeWorker_Handle_InvalidPerformative(t *testing.T) {
	w := &AseBridgeWorker{
		logger: slog.Default(),
	}

	env := core.Envelope{
		Performative: core.INFORM,
	}
	data, _ := json.Marshal(env)

	msg := &nats.Msg{
		Subject: "test",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	assert.NoError(t, err) // Returns nil, drops non-REQUEST
}

func TestAseBridgeWorker_Handle_MissingDagName(t *testing.T) {
	w := &AseBridgeWorker{
		logger: slog.Default(),
	}

	env := core.Envelope{
		Performative: core.REQUEST,
		Body:         []byte(`{}`), // Missing dag_name
	}
	data, _ := json.Marshal(env)

	msg := &nats.Msg{
		Subject: "test",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	assert.Error(t, err)
	assert.Equal(t, "dag_name is required in workflow config or payload", err.Error())
}

func TestAseBridgeWorker_Handle_MissingDomainTool(t *testing.T) {
	w := &AseBridgeWorker{
		logger: slog.Default(),
	}

	env := core.Envelope{
		Performative: core.REQUEST,
		Body:         []byte(`{"config": {"dag_name": "test_dag", "domain_tool": "missing_tool"}}`), 
	}
	data, _ := json.Marshal(env)

	msg := &nats.Msg{
		Subject: "test",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	assert.Error(t, err)
}

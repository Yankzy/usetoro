package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoroccanEnrichmentWorker_Subscriptions(t *testing.T) {
	worker := &MoroccanEnrichmentWorker{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
	}

	subs := worker.Subscriptions()
	require.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.pcm.moroccan_enrichment", subs[0].Subject)
	assert.Equal(t, "worker-inbox-pcm_moroccan_enrichment-group", subs[0].Group)
}

func TestMoroccanEnrichmentWorker_ParseInboundMessage(t *testing.T) {
	worker := &MoroccanEnrichmentWorker{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
	}

	sessionID := uuid.New().String()
	realmID := "morocco-realm-1"

	// Case 1: TAP Core Envelope
	taskBody, _ := json.Marshal(MoroccanEnrichmentPayload{
		SessionID: sessionID,
		RealmID:   realmID,
	})
	envelope, _ := core.NewEnvelope(
		uuid.New().String(),
		"orchestrator",
		"worker.pcm.moroccan_enrichment",
		"conv-123",
		core.REQUEST,
		json.RawMessage(taskBody),
	)
	envBytes, _ := json.Marshal(envelope)

	reqEnv, payload, err := worker.parseInboundMessage(&nats.Msg{Data: envBytes})
	require.NoError(t, err)
	assert.Equal(t, sessionID, payload.SessionID)
	assert.Equal(t, realmID, payload.RealmID)
	assert.Equal(t, "conv-123", reqEnv.ConversationID)

	// Case 2: Direct JSON payload
	directPayloadBytes, _ := json.Marshal(MoroccanEnrichmentPayload{
		SessionID: sessionID,
		RealmID:   realmID,
	})
	_, payload2, err2 := worker.parseInboundMessage(&nats.Msg{Data: directPayloadBytes})
	require.NoError(t, err2)
	assert.Equal(t, sessionID, payload2.SessionID)
	assert.Equal(t, realmID, payload2.RealmID)
}

func TestMoroccanEnrichmentWorker_EnrichmentExecution(t *testing.T) {
	eng := enrichment.NewMoroccanEnrichmentEngine()
	worker := &MoroccanEnrichmentWorker{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
		engine: eng,
	}

	ctx := context.Background()

	testRows := []database.FignodeStagingTransaction{
		{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawDescription: pgtype.Text{String: "PRLV اتصالات المغرب FACTURE FIBRE PRO", Valid: true},
			RawAmount:      "2400.00",
			CashDirection:  pgtype.Text{String: "OUTFLOW", Valid: true},
			ParsedDate:     pgtype.Date{Time: time.Now(), Valid: true},
		},
		{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawDescription: pgtype.Text{String: "CARTE AWS CLOUD HOSTING", Valid: true},
			RawAmount:      "12000.00",
			CashDirection:  pgtype.Text{String: "OUTFLOW", Valid: true},
			ParsedDate:     pgtype.Date{Time: time.Now(), Valid: true},
		},
	}

	for _, tx := range testRows {
		rawTxn := enrichment.RawTransaction{
			TransactionID:   uuid.UUID(tx.ID.Bytes).String(),
			RawDescription:  tx.RawDescription.String,
			Amount:          12000.00,
			Currency:        "MAD",
			CashDirection:   enrichment.CashDirection(tx.CashDirection.String),
			TransactionDate: tx.ParsedDate.Time,
			AccountCode:     "514100",
		}

		envelope := worker.engine.EnrichTransaction(ctx, "tenant-ma", rawTxn)
		require.NotEmpty(t, envelope.Counterparty.NormalizedName)
		require.NotEmpty(t, envelope.PCGMAccounting.SuggestedAccount)
		assert.GreaterOrEqual(t, envelope.ConfidenceScore, 0.60)

		if tx.RawDescription.String == "CARTE AWS CLOUD HOSTING" {
			assert.True(t, envelope.Counterparty.IsForeignService)
			assert.True(t, envelope.TaxAndCompliance.RASApplicable)
			if envelope.TaxAndCompliance.RASAccount != nil {
				assert.Equal(t, "445800", *envelope.TaxAndCompliance.RASAccount)
			}
			assert.Equal(t, 0.10, envelope.TaxAndCompliance.RASRate)
			assert.Equal(t, "613670", envelope.PCGMAccounting.SuggestedAccount)
		} else if tx.RawDescription.String == "PRLV اتصالات المغرب FACTURE FIBRE PRO" {
			assert.Equal(t, "Maroc Telecom (Itissalat Al-Maghrib S.A.)", envelope.Counterparty.NormalizedName)
			assert.Equal(t, "614510", envelope.PCGMAccounting.SuggestedAccount)
			assert.Equal(t, 0.20, envelope.PCGMAccounting.DefaultTVARate)
		}
	}
}

func TestMoroccanEnrichmentWorker_PublishCompletionProof(t *testing.T) {
	worker := &MoroccanEnrichmentWorker{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
	}

	payload := &MoroccanEnrichmentPayload{
		SessionID: "sess-12345",
		RealmID:   "realm-morocco",
	}
	reqEnv := &core.Envelope{
		ConversationID: "cid-987",
	}

	err := worker.publishCompletionProof(payload, reqEnv, 10, 10, 50*time.Millisecond, "")
	assert.NoError(t, err)
}

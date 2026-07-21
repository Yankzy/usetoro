package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"

	"github.com/Yankzy/usetoro/internal/database"
)

type mockEmailSequencerStore struct {
	GetProspectByIDFunc              func(context.Context, pgtype.UUID) (database.GetProspectByIDRow, error)
	GetCampaignStepFunc              func(context.Context, database.GetCampaignStepParams) (database.GetCampaignStepRow, error)
	GetNextAvailableEmailAccountFunc func(context.Context, pgtype.UUID) (database.GetNextAvailableEmailAccountRow, error)
	GetEmailLogsForProspectFunc      func(context.Context, database.GetEmailLogsForProspectParams) ([]database.GetEmailLogsForProspectRow, error)
	HasProspectInteractedFunc        func(context.Context, database.HasProspectInteractedParams) (bool, error)
	InsertScheduledJobFunc           func(context.Context, database.InsertScheduledJobParams) (pgtype.UUID, error)
}

func (m *mockEmailSequencerStore) GetProspectByID(ctx context.Context, id pgtype.UUID) (database.GetProspectByIDRow, error) {
	return m.GetProspectByIDFunc(ctx, id)
}

func (m *mockEmailSequencerStore) GetCampaignStep(ctx context.Context, params database.GetCampaignStepParams) (database.GetCampaignStepRow, error) {
	return m.GetCampaignStepFunc(ctx, params)
}

func (m *mockEmailSequencerStore) GetNextAvailableEmailAccount(ctx context.Context, id pgtype.UUID) (database.GetNextAvailableEmailAccountRow, error) {
	return m.GetNextAvailableEmailAccountFunc(ctx, id)
}

func (m *mockEmailSequencerStore) GetEmailLogsForProspect(ctx context.Context, params database.GetEmailLogsForProspectParams) ([]database.GetEmailLogsForProspectRow, error) {
	return m.GetEmailLogsForProspectFunc(ctx, params)
}

func (m *mockEmailSequencerStore) HasProspectInteracted(ctx context.Context, params database.HasProspectInteractedParams) (bool, error) {
	return m.HasProspectInteractedFunc(ctx, params)
}

func (m *mockEmailSequencerStore) InsertScheduledJob(ctx context.Context, params database.InsertScheduledJobParams) (pgtype.UUID, error) {
	return m.InsertScheduledJobFunc(ctx, params)
}

func TestEmailSequencerWorker_Handle_InvalidJSON(t *testing.T) {
	w := &EmailSequencerWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "test",
		Data:    []byte(`{ "invalid json" }`),
	}

	err := w.Handle(context.Background(), msg)
	assert.NoError(t, err) // returns nil, drops bad payload
}

func TestEmailSequencerWorker_Handle_InactiveProspect(t *testing.T) {
	prospectID := uuid.New()
	campaignID := uuid.New()
	tenantID := uuid.New()

	mockDB := &mockEmailSequencerStore{
		GetProspectByIDFunc: func(ctx context.Context, id pgtype.UUID) (database.GetProspectByIDRow, error) {
			return database.GetProspectByIDRow{
				ID:       pgtype.UUID{Bytes: prospectID, Valid: true},
				Email:    "test@prospect.com",
				Status:   "opted_out", // inactive status
				TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			}, nil
		},
	}

	w := &EmailSequencerWorker{
		db:     mockDB,
		logger: slog.Default(),
	}

	evt := CampaignTriggerEvent{
		TenantID:   tenantID,
		ProspectID: prospectID,
		CampaignID: campaignID,
		StepIndex:  0,
	}
	data, _ := json.Marshal(evt)

	msg := &nats.Msg{
		Subject: "test",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	assert.NoError(t, err) // drops inactive prospect events
}

func TestEmailSequencerWorker_Handle_MissingStep(t *testing.T) {
	prospectID := uuid.New()
	campaignID := uuid.New()
	tenantID := uuid.New()

	mockDB := &mockEmailSequencerStore{
		GetProspectByIDFunc: func(ctx context.Context, id pgtype.UUID) (database.GetProspectByIDRow, error) {
			return database.GetProspectByIDRow{
				ID:       pgtype.UUID{Bytes: prospectID, Valid: true},
				Email:    "test@prospect.com",
				Status:   "active",
				TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			}, nil
		},
		HasProspectInteractedFunc: func(ctx context.Context, params database.HasProspectInteractedParams) (bool, error) {
			return false, nil
		},
		GetCampaignStepFunc: func(ctx context.Context, params database.GetCampaignStepParams) (database.GetCampaignStepRow, error) {
			return database.GetCampaignStepRow{}, errors.New("sql: no rows in result set")
		},
	}

	w := &EmailSequencerWorker{
		db:     mockDB,
		logger: slog.Default(),
	}

	evt := CampaignTriggerEvent{
		TenantID:   tenantID,
		ProspectID: prospectID,
		CampaignID: campaignID,
		StepIndex:  0,
	}
	data, _ := json.Marshal(evt)

	msg := &nats.Msg{
		Subject: "test",
		Data:    data,
	}

	err := w.Handle(context.Background(), msg)
	assert.NoError(t, err) // drops missing campaign step events
}

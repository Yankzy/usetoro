package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

type mockASEQuerier struct {
	database.Querier
	listAllFunc      func(ctx context.Context) ([]database.ToroCoreAseDag, error)
	listByTenantFunc func(ctx context.Context, tenantID pgtype.UUID) ([]database.ToroCoreAseDag, error)
	listVersionsFunc func(ctx context.Context, dagID pgtype.UUID) ([]database.ListASEDagVersionsRow, error)
	getVersionFunc   func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error)
	updateConfigFunc func(ctx context.Context, arg database.UpdateASEConfigByIDParams) (database.ToroCoreAseDag, error)
}

func (m *mockASEQuerier) ListAllASEConfigs(ctx context.Context) ([]database.ToroCoreAseDag, error) {
	if m.listAllFunc != nil {
		return m.listAllFunc(ctx)
	}
	return nil, nil
}

func (m *mockASEQuerier) ListASEConfigsByTenant(ctx context.Context, tenantID pgtype.UUID) ([]database.ToroCoreAseDag, error) {
	if m.listByTenantFunc != nil {
		return m.listByTenantFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockASEQuerier) ListASEDagVersions(ctx context.Context, dagID pgtype.UUID) ([]database.ListASEDagVersionsRow, error) {
	if m.listVersionsFunc != nil {
		return m.listVersionsFunc(ctx, dagID)
	}
	return nil, nil
}

func (m *mockASEQuerier) GetASEDagVersion(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
	if m.getVersionFunc != nil {
		return m.getVersionFunc(ctx, id)
	}
	return database.ToroCoreAseDagVersion{}, nil
}

func (m *mockASEQuerier) UpdateASEConfigByID(ctx context.Context, arg database.UpdateASEConfigByIDParams) (database.ToroCoreAseDag, error) {
	if m.updateConfigFunc != nil {
		return m.updateConfigFunc(ctx, arg)
	}
	return database.ToroCoreAseDag{}, nil
}

func TestHandleListASEConfigs(t *testing.T) {
	logger := testLogger()
	handler := &Handler{
		Logger: logger,
	}

	tenantUUID := uuid.New()
	mockConfigs := []database.ToroCoreAseDag{
		{
			ID:              pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:        pgtype.UUID{Bytes: tenantUUID, Valid: true},
			RealmID:         pgtype.Text{String: "realm-123", Valid: true},
			Name:            "test-config",
			DagConfig:       []byte(`{"nodes":{}}`),
			HyperParameters: []byte(`{"confidence_threshold":0.95}`),
			Prompts:         []byte(`{}`),
			CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}

	tests := []struct {
		name           string
		queryParams    string
		mockSetup      func(m *mockASEQuerier)
		expectedStatus int
		verifyJSON     func(t *testing.T, body string)
	}{
		{
			name:        "Success - List All",
			queryParams: "",
			mockSetup: func(m *mockASEQuerier) {
				m.listAllFunc = func(ctx context.Context) ([]database.ToroCoreAseDag, error) {
					return mockConfigs, nil
				}
			},
			expectedStatus: http.StatusOK,
			verifyJSON: func(t *testing.T, body string) {
				var parsed []map[string]interface{}
				err := json.Unmarshal([]byte(body), &parsed)
				assert.NoError(t, err)
				assert.Len(t, parsed, 1)

				// Verify JSON keys are lowercase
				item := parsed[0]
				assert.Contains(t, item, "id")
				assert.Contains(t, item, "tenant_id")
				assert.Contains(t, item, "realm_id")
				assert.Contains(t, item, "name")
				assert.Contains(t, item, "created_at")
				assert.Contains(t, item, "updated_at")

				// Ensure uppercase keys do NOT exist
				assert.NotContains(t, item, "ID")
				assert.NotContains(t, item, "TenantID")
				assert.NotContains(t, item, "RealmID")
				assert.NotContains(t, item, "Name")

				assert.Equal(t, "test-config", item["name"])
			},
		},
		{
			name:        "Success - List by Tenant ID",
			queryParams: "?tenant_id=" + tenantUUID.String(),
			mockSetup: func(m *mockASEQuerier) {
				m.listByTenantFunc = func(ctx context.Context, tenantID pgtype.UUID) ([]database.ToroCoreAseDag, error) {
					assert.Equal(t, tenantUUID, uuid.UUID(tenantID.Bytes))
					return mockConfigs, nil
				}
			},
			expectedStatus: http.StatusOK,
			verifyJSON: func(t *testing.T, body string) {
				var parsed []map[string]interface{}
				err := json.Unmarshal([]byte(body), &parsed)
				assert.NoError(t, err)
				assert.Len(t, parsed, 1)
				assert.Equal(t, "test-config", parsed[0]["name"])
			},
		},
		{
			name:           "Error - Invalid Tenant ID",
			queryParams:    "?tenant_id=invalid-uuid",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "Error - DB Failure",
			queryParams: "",
			mockSetup: func(m *mockASEQuerier) {
				m.listAllFunc = func(ctx context.Context) ([]database.ToroCoreAseDag, error) {
					return nil, errors.New("db disconnect")
				}
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := &mockASEQuerier{}
			tc.mockSetup(mockDB)
			handler.DB = mockDB

			req := httptest.NewRequest(http.MethodGet, "/api/ase/configs"+tc.queryParams, nil)
			w := httptest.NewRecorder()

			handler.HandleListASEConfigs(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, w.Body.String())
			}
		})
	}
}

func TestHandleListASEDagVersions(t *testing.T) {
	logger := testLogger()
	handler := &Handler{Logger: logger}

	dagUUID := uuid.New()
	versionUUID := uuid.New()
	mockVersions := []database.ListASEDagVersionsRow{
		{
			ID:            pgtype.UUID{Bytes: versionUUID, Valid: true},
			DagID:         pgtype.UUID{Bytes: dagUUID, Valid: true},
			VersionNumber: 1,
			CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}

	tests := []struct {
		name           string
		queryParams    string
		mockSetup      func(m *mockASEQuerier)
		expectedStatus int
		verifyJSON     func(t *testing.T, body string)
	}{
		{
			name:        "Success",
			queryParams: "?dag_id=" + dagUUID.String(),
			mockSetup: func(m *mockASEQuerier) {
				m.listVersionsFunc = func(ctx context.Context, dagID pgtype.UUID) ([]database.ListASEDagVersionsRow, error) {
					assert.Equal(t, dagUUID, uuid.UUID(dagID.Bytes))
					return mockVersions, nil
				}
			},
			expectedStatus: http.StatusOK,
			verifyJSON: func(t *testing.T, body string) {
				var parsed []map[string]interface{}
				err := json.Unmarshal([]byte(body), &parsed)
				assert.NoError(t, err)
				assert.Len(t, parsed, 1)
				assert.Equal(t, float64(1), parsed[0]["version_number"])
			},
		},
		{
			name:           "Error - Missing dag_id",
			queryParams:    "",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Error - Invalid dag_id format",
			queryParams:    "?dag_id=invalid",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "Error - DB Failure",
			queryParams: "?dag_id=" + dagUUID.String(),
			mockSetup: func(m *mockASEQuerier) {
				m.listVersionsFunc = func(ctx context.Context, dagID pgtype.UUID) ([]database.ListASEDagVersionsRow, error) {
					return nil, errors.New("db fail")
				}
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := &mockASEQuerier{}
			tc.mockSetup(mockDB)
			handler.DB = mockDB

			req := httptest.NewRequest(http.MethodGet, "/ase/config/versions"+tc.queryParams, nil)
			w := httptest.NewRecorder()

			handler.HandleListASEDagVersions(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, w.Body.String())
			}
		})
	}
}

func TestHandleGetASEDagVersion(t *testing.T) {
	logger := testLogger()
	handler := &Handler{Logger: logger}

	dagUUID := uuid.New()
	versionUUID := uuid.New()
	mockVersion := database.ToroCoreAseDagVersion{
		ID:              pgtype.UUID{Bytes: versionUUID, Valid: true},
		DagID:           pgtype.UUID{Bytes: dagUUID, Valid: true},
		VersionNumber:   1,
		DagConfig:       []byte(`{"nodes":{}}`),
		HyperParameters: []byte(`{"confidence_threshold":0.95}`),
		Prompts:         []byte(`{}`),
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	tests := []struct {
		name           string
		queryParams    string
		mockSetup      func(m *mockASEQuerier)
		expectedStatus int
		verifyJSON     func(t *testing.T, body string)
	}{
		{
			name:        "Success",
			queryParams: "?id=" + versionUUID.String(),
			mockSetup: func(m *mockASEQuerier) {
				m.getVersionFunc = func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
					assert.Equal(t, versionUUID, uuid.UUID(id.Bytes))
					return mockVersion, nil
				}
			},
			expectedStatus: http.StatusOK,
			verifyJSON: func(t *testing.T, body string) {
				var parsed map[string]interface{}
				err := json.Unmarshal([]byte(body), &parsed)
				assert.NoError(t, err)
				assert.Equal(t, float64(1), parsed["version_number"])
				assert.Contains(t, parsed, "dag")
				assert.Contains(t, parsed, "hyper_parameters")
				assert.Contains(t, parsed, "prompts")
			},
		},
		{
			name:           "Error - Missing id",
			queryParams:    "",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Error - Invalid id format",
			queryParams:    "?id=invalid",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "Error - Not Found",
			queryParams: "?id=" + versionUUID.String(),
			mockSetup: func(m *mockASEQuerier) {
				m.getVersionFunc = func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
					return database.ToroCoreAseDagVersion{}, errors.New("not found")
				}
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := &mockASEQuerier{}
			tc.mockSetup(mockDB)
			handler.DB = mockDB

			req := httptest.NewRequest(http.MethodGet, "/ase/config/version"+tc.queryParams, nil)
			w := httptest.NewRecorder()

			handler.HandleGetASEDagVersion(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, w.Body.String())
			}
		})
	}
}

func TestHandleRestoreASEDagVersion(t *testing.T) {
	logger := testLogger()
	handler := &Handler{Logger: logger}

	dagUUID := uuid.New()
	versionUUID := uuid.New()
	mockVersion := database.ToroCoreAseDagVersion{
		ID:              pgtype.UUID{Bytes: versionUUID, Valid: true},
		DagID:           pgtype.UUID{Bytes: dagUUID, Valid: true},
		VersionNumber:   1,
		DagConfig:       []byte(`{"nodes":{}}`),
		HyperParameters: []byte(`{"confidence_threshold":0.95}`),
		Prompts:         []byte(`{}`),
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	mockConfig := database.ToroCoreAseDag{
		ID:              pgtype.UUID{Bytes: dagUUID, Valid: true},
		TenantID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		RealmID:         pgtype.Text{String: "realm-1", Valid: true},
		Name:            "default",
		DagConfig:       mockVersion.DagConfig,
		HyperParameters: mockVersion.HyperParameters,
		Prompts:         mockVersion.Prompts,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	tests := []struct {
		name           string
		method         string
		body           string
		mockSetup      func(m *mockASEQuerier)
		expectedStatus int
		verifyJSON     func(t *testing.T, body string)
	}{
		{
			name:   "Success",
			method: http.MethodPost,
			body:   `{"id":"` + versionUUID.String() + `"}`,
			mockSetup: func(m *mockASEQuerier) {
				m.getVersionFunc = func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
					assert.Equal(t, versionUUID, uuid.UUID(id.Bytes))
					return mockVersion, nil
				}
				m.updateConfigFunc = func(ctx context.Context, arg database.UpdateASEConfigByIDParams) (database.ToroCoreAseDag, error) {
					assert.Equal(t, dagUUID, uuid.UUID(arg.ID.Bytes))
					assert.Equal(t, mockVersion.DagConfig, arg.DagConfig)
					assert.Equal(t, mockVersion.HyperParameters, arg.HyperParameters)
					assert.Equal(t, mockVersion.Prompts, arg.Prompts)
					return mockConfig, nil
				}
			},
			expectedStatus: http.StatusOK,
			verifyJSON: func(t *testing.T, body string) {
				var parsed map[string]interface{}
				err := json.Unmarshal([]byte(body), &parsed)
				assert.NoError(t, err)
				assert.Contains(t, parsed, "id")
				assert.Equal(t, "default", parsed["name"])
			},
		},
		{
			name:           "Error - Non POST Method",
			method:         http.MethodGet,
			body:           "",
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "Error - Invalid Body JSON",
			method:         http.MethodPost,
			body:           `{invalid}`,
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Error - Missing ID",
			method:         http.MethodPost,
			body:           `{"id":""}`,
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Error - Invalid UUID ID",
			method:         http.MethodPost,
			body:           `{"id":"invalid"}`,
			mockSetup:      func(m *mockASEQuerier) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "Error - Version Not Found",
			method: http.MethodPost,
			body:   `{"id":"` + versionUUID.String() + `"}`,
			mockSetup: func(m *mockASEQuerier) {
				m.getVersionFunc = func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
					return database.ToroCoreAseDagVersion{}, errors.New("not found")
				}
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:   "Error - DB Update Failure",
			method: http.MethodPost,
			body:   `{"id":"` + versionUUID.String() + `"}`,
			mockSetup: func(m *mockASEQuerier) {
				m.getVersionFunc = func(ctx context.Context, id pgtype.UUID) (database.ToroCoreAseDagVersion, error) {
					return mockVersion, nil
				}
				m.updateConfigFunc = func(ctx context.Context, arg database.UpdateASEConfigByIDParams) (database.ToroCoreAseDag, error) {
					return database.ToroCoreAseDag{}, errors.New("update failed")
				}
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := &mockASEQuerier{}
			tc.mockSetup(mockDB)
			handler.DB = mockDB

			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			var req *http.Request
			if bodyReader != nil {
				req = httptest.NewRequest(tc.method, "/ase/config/restore", bodyReader)
			} else {
				req = httptest.NewRequest(tc.method, "/ase/config/restore", nil)
			}
			w := httptest.NewRecorder()

			handler.HandleRestoreASEDagVersion(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			if tc.verifyJSON != nil {
				tc.verifyJSON(t, w.Body.String())
			}
		})
	}
}


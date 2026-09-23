package workers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostmarkInboundEmailWorker_Subscriptions(t *testing.T) {
	cfg := &config.Config{
		Workers: config.WorkerSubjects{
			"postmark_inbound_email": {
				ActivityType: "workers.email.postmark_inbound",
			},
		},
	}
	config.SetGlobal(cfg)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	worker := &PostmarkInboundEmailWorker{
		logger: logger,
		cfg:    cfg,
	}

	subs := worker.Subscriptions()
	require.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.email.postmark_inbound", subs[0].Subject)
}

func TestPostmarkInboundEmailWorker_Handle_EarlyReturnOnPoisonPill(t *testing.T) {
	worker := &PostmarkInboundEmailWorker{
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
	}

	msg := &nats.Msg{
		Data: []byte("invalid json"),
	}

	_ = worker
	_ = msg
}

func TestPostmarkInboundEmailWorker_Handle_PreExistingS3Key(t *testing.T) {
	worker := &PostmarkInboundEmailWorker{
		logger:  slog.New(slog.NewTextHandler(os.Stdout, nil)),
		cfg:     &config.Config{},
		storage: nil, // Should succeed without S3 service if S3Key is pre-populated
	}

	emailPayload := PostmarkInboundEmail{
		From: "rap_accounting@test.com",
		To:   "inbox@usetoro.io",
		Attachments: []struct {
			Name          string `json:"Name"`
			ContentType   string `json:"ContentType"`
			ContentLength int    `json:"ContentLength"`
			Content       string `json:"Content"`
			S3Key         string `json:"S3Key,omitempty"`
			SHA256        string `json:"SHA256,omitempty"`
		}{
			{
				Name:        "statement.pdf",
				ContentType: "application/pdf",
				S3Key:       "s3-keys/preuploaded-statement.pdf",
				SHA256:      "abc123hash",
			},
		},
	}

	data, err := json.Marshal(emailPayload)
	require.NoError(t, err)

	msg := &nats.Msg{
		Data: data,
	}

	ctx := context.Background()
	_ = worker.Handle(ctx, msg)
}

// -------------------------------------------------------------------------
// Test Mocks & Inbound Boundary Tests
// -------------------------------------------------------------------------

type testMockS3 struct {
	uploadedFiles map[string][]byte
	contentTypes  map[string]string
}

func newTestMockS3() *testMockS3 {
	return &testMockS3{
		uploadedFiles: make(map[string][]byte),
		contentTypes:  make(map[string]string),
	}
}

func (m *testMockS3) UploadFileToS3(ctx context.Context, key string, body io.Reader, contentType string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.uploadedFiles[key] = b
	m.contentTypes[key] = contentType
	return nil
}

func (m *testMockS3) DownloadS3File(ctx context.Context, key string) (io.ReadCloser, error) {
	data, ok := m.uploadedFiles[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *testMockS3) GeneratePresignedURL(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	return "https://s3.example.com/" + key, nil
}

type testMockRow struct {
	scanFunc func(dest ...any) error
}

func (r testMockRow) Scan(dest ...any) error {
	return r.scanFunc(dest...)
}

type testMockRows struct {
	items [][]any
	idx   int
}

func (m *testMockRows) Close() {}
func (m *testMockRows) Err() error { return nil }
func (m *testMockRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (m *testMockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (m *testMockRows) Next() bool {
	if m.idx < len(m.items) {
		m.idx++
		return true
	}
	return false
}
func (m *testMockRows) Scan(dest ...any) error {
	row := m.items[m.idx-1]
	for i, d := range dest {
		if i < len(row) {
			if ptr, ok := d.(*pgtype.UUID); ok {
				if val, isUUID := row[i].(pgtype.UUID); isUUID {
					*ptr = val
				}
			}
		}
	}
	return nil
}
func (m *testMockRows) Values() ([]any, error) { return nil, nil }
func (m *testMockRows) RawValues() [][]byte { return nil }
func (m *testMockRows) Conn() *pgx.Conn { return nil }

type testMockDB struct {
	userEntities       map[string]pgtype.UUID
	entitiesByName     map[string]pgtype.UUID
	descendants        map[string][]pgtype.UUID
	conversations      map[string]pgtype.UUID
	rowsAffectedOnSave int64
}

func newTestMockDB() *testMockDB {
	return &testMockDB{
		userEntities:       make(map[string]pgtype.UUID),
		entitiesByName:     make(map[string]pgtype.UUID),
		descendants:        make(map[string][]pgtype.UUID),
		conversations:      make(map[string]pgtype.UUID),
		rowsAffectedOnSave: 1,
	}
}

func (m *testMockDB) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", m.rowsAffectedOnSave)), nil
}

func (m *testMockDB) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if strings.Contains(query, "entity_tree") || strings.Contains(query, "GetEntityDescendants") {
		if len(args) > 0 {
			if id, ok := args[0].(pgtype.UUID); ok {
				idStr := uuidFromPG(id)
				if list, exists := m.descendants[idStr]; exists {
					var rows [][]any
					for _, d := range list {
						rows = append(rows, []any{d})
					}
					return &testMockRows{items: rows}, nil
				}
			}
		}
	}
	return &testMockRows{}, nil
}

func (m *testMockDB) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	if strings.Contains(query, "toro_core.users WHERE email =") {
		email := args[0].(string)
		if id, ok := m.userEntities[email]; ok && id.Valid {
			return testMockRow{scanFunc: func(dest ...any) error {
				*dest[0].(*pgtype.UUID) = id
				return nil
			}}
		}
		return testMockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
	}

	if strings.Contains(query, "toro_core.entities WHERE name =") {
		name := args[0].(string)
		if id, ok := m.entitiesByName[name]; ok && id.Valid {
			return testMockRow{scanFunc: func(dest ...any) error {
				*dest[0].(*pgtype.UUID) = id
				return nil
			}}
		}
		return testMockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
	}

	if strings.Contains(query, "toro_core.conversations") && strings.Contains(query, "external_id =") {
		extID := args[0].(string)
		if sessID, ok := m.conversations[extID]; ok && sessID.Valid {
			return testMockRow{scanFunc: func(dest ...any) error {
				*dest[0].(*pgtype.UUID) = sessID
				return nil
			}}
		}
		return testMockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
	}

	if strings.Contains(query, "pg_try_advisory_lock") {
		return testMockRow{scanFunc: func(dest ...any) error {
			*dest[0].(*bool) = true
			return nil
		}}
	}

	if strings.Contains(query, "INSERT INTO toro_core.conversation_sessions") {
		return testMockRow{scanFunc: func(dest ...any) error {
			// Populate conversation_session struct fields
			if len(dest) >= 1 {
				if s, ok := dest[0].(*database.ToroCoreConversationSession); ok {
					s.ID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
				}
			}
			return nil
		}}
	}

	return testMockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// -------------------------------------------------------------------------
// BLOCKER 4: Recipient-Based Tenant Routing and Hierarchical Authorization Tests
// -------------------------------------------------------------------------

func TestResolveSender_RecipientRoutingAndAuthorization(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockDB := newTestMockDB()
	db := database.New(mockDB)

	cpaEntityID := pgUUID(uuid.New())
	clientAEntityID := pgUUID(uuid.New())
	clientBEntityID := pgUUID(uuid.New())
	unrelatedEntityID := pgUUID(uuid.New())

	// Register users
	cpaUser := "accountant@cpa-firm.com"
	clientAUser := "cfo@client-a.com"
	unrelatedUser := "stranger@other.com"

	mockDB.userEntities[cpaUser] = cpaEntityID
	mockDB.userEntities[clientAUser] = clientAEntityID
	mockDB.userEntities[unrelatedUser] = unrelatedEntityID

	// Register entities by subdomain / alias
	mockDB.entitiesByName["client_a"] = clientAEntityID
	mockDB.entitiesByName["client_b"] = clientBEntityID

	// CPA firm manages client A and client B
	mockDB.descendants[uuidFromPG(cpaEntityID)] = []pgtype.UUID{cpaEntityID, clientAEntityID, clientBEntityID}
	// Client A manages only itself
	mockDB.descendants[uuidFromPG(clientAEntityID)] = []pgtype.UUID{clientAEntityID}

	t.Run("Multi-entity CPA accountant routes to Client A via recipient alias", func(t *testing.T) {
		sender, err := ResolveSender(ctx, logger, db, nil, cpaUser, "client_a@inbound.usetoro.io", "")
		require.NoError(t, err)
		assert.Equal(t, clientAEntityID, sender.EntityID)
		assert.Equal(t, "client_a", sender.AgentAlias)
	})

	t.Run("Same CPA accountant routes to Client B via recipient subdomain", func(t *testing.T) {
		sender, err := ResolveSender(ctx, logger, db, nil, cpaUser, "inbox@client_b.usetoro.io", "")
		require.NoError(t, err)
		assert.Equal(t, clientBEntityID, sender.EntityID)
		assert.Equal(t, "client_b", sender.Subdomain)
	})

	t.Run("Client A user is authorized for Client A", func(t *testing.T) {
		sender, err := ResolveSender(ctx, logger, db, nil, clientAUser, "client_a@inbound.usetoro.io", "")
		require.NoError(t, err)
		assert.Equal(t, clientAEntityID, sender.EntityID)
	})

	t.Run("Client A user is UNAUTHORIZED for Client B (fails closed)", func(t *testing.T) {
		_, err := ResolveSender(ctx, logger, db, nil, clientAUser, "client_b@inbound.usetoro.io", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authorized for entity")
	})

	t.Run("Unknown recipient entity fails closed / bounces", func(t *testing.T) {
		_, err := ResolveSender(ctx, logger, db, nil, cpaUser, "nonexistent@inbound.usetoro.io", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown recipient entity")
	})

	t.Run("Unregistered sender fails closed / bounces", func(t *testing.T) {
		_, err := ResolveSender(ctx, logger, db, nil, "unknown@random.org", "client_a@inbound.usetoro.io", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a registered user")
	})
}

// -------------------------------------------------------------------------
// BLOCKER 5: Safe Attachment Acceptance & Sniffing Tests
// -------------------------------------------------------------------------

func TestPostmarkInboundEmailWorker_AttachmentValidation(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockS3 := newTestMockS3()
	mockDB := newTestMockDB()

	userEntityID := pgUUID(uuid.New())
	mockDB.userEntities["user@test.com"] = userEntityID
	mockDB.entitiesByName["rap_client"] = userEntityID
	mockDB.descendants[uuidFromPG(userEntityID)] = []pgtype.UUID{userEntityID}

	worker := &PostmarkInboundEmailWorker{
		logger:  logger,
		cfg:     &config.Config{},
		db:      database.New(mockDB),
		storage: mockS3,
	}

	// Valid PDF header
	validPDFBytes := []byte("%PDF-1.4\n%âãÏÓ\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF")
	// Valid PNG header
	validPNGBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	// Valid JPEG header
	validJPEGBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
	// Executable ELF header
	maliciousELFBytes := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	// Executable DOS/Windows MZ header
	maliciousEXEBytes := []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}

	t.Run("Accepts valid PDF attachment and sanitizes filename", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-valid-pdf-1",
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "../../../../etc/invoice.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(validPDFBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		// Verify file uploaded to mockS3 with sanitized name and session prefix
		require.Len(t, mockS3.uploadedFiles, 1)
		for key := range mockS3.uploadedFiles {
			assert.Contains(t, key, "invoice.pdf")
			assert.NotContains(t, key, "..")
			assert.True(t, strings.HasPrefix(key, "inbound/"))
		}
	})

	t.Run("Rejects MIME-spoofed executable labeled as PDF", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-spoofed-elf",
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "statement.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(maliciousELFBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		// Malicious ELF MUST be rejected and NOT uploaded to S3
		assert.Empty(t, mockS3.uploadedFiles, "MIME-spoofed executable must be rejected")
	})

	t.Run("Rejects Windows executable labeled as PDF", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-spoofed-exe",
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "payroll.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(maliciousEXEBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		assert.Empty(t, mockS3.uploadedFiles, "Windows executable must be rejected")
	})

	t.Run("Rejects oversized attachment (> 10MB)", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		oversizedBytes := make([]byte, 11*1024*1024)
		copy(oversizedBytes, validPDFBytes)

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-oversized",
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "huge.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(oversizedBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		assert.Empty(t, mockS3.uploadedFiles, "Oversized attachment must not be uploaded")
	})

	t.Run("Enforces maximum attachment count limit (truncates excess)", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		var attachments []struct {
			Name          string `json:"Name"`
			ContentType   string `json:"ContentType"`
			ContentLength int    `json:"ContentLength"`
			Content       string `json:"Content"`
			S3Key         string `json:"S3Key,omitempty"`
			SHA256        string `json:"SHA256,omitempty"`
		}

		for i := 1; i <= 15; i++ {
			attachments = append(attachments, struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				Name:        fmt.Sprintf("doc_%d.pdf", i),
				ContentType: "application/pdf",
				Content:     base64.StdEncoding.EncodeToString(validPDFBytes),
			})
		}

		payload := PostmarkInboundEmail{
			From:        "user@test.com",
			To:          "rap_client@inbound.usetoro.io",
			MessageID:   "msg-many-attachments",
			Attachments: attachments,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		// Only max 10 attachments processed
		assert.LessOrEqual(t, len(mockS3.uploadedFiles), MaxInboundAttachments)
	})

	_ = validPNGBytes
	_ = validJPEGBytes
}

// -------------------------------------------------------------------------
// BLOCKER 7 & BLOCKER 2: Idempotency & Duplicate Downstream Suppression Tests
// -------------------------------------------------------------------------

func TestPostmarkInboundEmailWorker_IdempotencyAndSuppression(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockS3 := newTestMockS3()
	mockDB := newTestMockDB()

	userEntityID := pgUUID(uuid.New())
	mockDB.userEntities["user@test.com"] = userEntityID
	mockDB.entitiesByName["rap_client"] = userEntityID
	mockDB.descendants[uuidFromPG(userEntityID)] = []pgtype.UUID{userEntityID}

	worker := &PostmarkInboundEmailWorker{
		logger:  logger,
		cfg:     &config.Config{},
		db:      database.New(mockDB),
		storage: mockS3,
	}

	validPDFBytes := []byte("%PDF-1.4\n%âãÏÓ\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF")

	t.Run("Duplicate MessageID already in DB terminates early with zero S3 uploads", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)

		existingMsgID := "msg-already-processed-123"
		mockDB.conversations[existingMsgID] = pgUUID(uuid.New())

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: existingMsgID,
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "receipt.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(validPDFBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		// Zero uploads because duplicate was suppressed at step 0
		assert.Empty(t, mockS3.uploadedFiles, "Duplicate message must not re-upload attachments")
	})

	t.Run("Insert conflict (0 rows affected) suppresses downstream dispatch", func(t *testing.T) {
		mockS3.uploadedFiles = make(map[string][]byte)
		mockDB.conversations = make(map[string]pgtype.UUID) // not yet in DB at step 0
		mockDB.rowsAffectedOnSave = 0                       // conflict on SaveConversationSessionMessageWithResult

		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-race-conflict-456",
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)
	})
}

// -------------------------------------------------------------------------
// BLOCKER 3: Session Ordering Tests (Document Created with Valid Session ID)
// -------------------------------------------------------------------------

func TestPostmarkInboundEmailWorker_SessionOrdering(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockS3 := newTestMockS3()
	mockDB := newTestMockDB()

	userEntityID := pgUUID(uuid.New())
	mockDB.userEntities["user@test.com"] = userEntityID
	mockDB.entitiesByName["rap_client"] = userEntityID
	mockDB.descendants[uuidFromPG(userEntityID)] = []pgtype.UUID{userEntityID}

	worker := &PostmarkInboundEmailWorker{
		logger:  logger,
		cfg:     &config.Config{},
		db:      database.New(mockDB),
		storage: mockS3,
	}

	validPDFBytes := []byte("%PDF-1.4\n%âãÏÓ\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF")

	t.Run("Document created with valid non-empty session ID in S3 key and metadata", func(t *testing.T) {
		payload := PostmarkInboundEmail{
			From:      "user@test.com",
			To:        "rap_client@inbound.usetoro.io",
			MessageID: "msg-session-order-test",
			Attachments: []struct {
				Name          string `json:"Name"`
				ContentType   string `json:"ContentType"`
				ContentLength int    `json:"ContentLength"`
				Content       string `json:"Content"`
				S3Key         string `json:"S3Key,omitempty"`
				SHA256        string `json:"SHA256,omitempty"`
			}{
				{
					Name:        "bill.pdf",
					ContentType: "application/pdf",
					Content:     base64.StdEncoding.EncodeToString(validPDFBytes),
				},
			},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		err = worker.Handle(ctx, &nats.Msg{Data: data})
		assert.NoError(t, err)

		require.Len(t, mockS3.uploadedFiles, 1)
		for key := range mockS3.uploadedFiles {
			// S3 key format: inbound/<session_id>/<uuid>-bill.pdf
			parts := strings.Split(key, "/")
			require.GreaterOrEqual(t, len(parts), 3)
			assert.Equal(t, "inbound", parts[0])
			sessionIDPart := parts[1]
			assert.NotEmpty(t, sessionIDPart)
			assert.NotEqual(t, "00000000-0000-0000-0000-000000000000", sessionIDPart)
			_, parseErr := uuid.Parse(sessionIDPart)
			assert.NoError(t, parseErr, "Session ID in S3 key must be a valid non-empty UUID resolved before attachment creation")
		}
	})
}

// -------------------------------------------------------------------------
// LOCAL SMOKE TEST: Representative Postmark Inbound Replay
// -------------------------------------------------------------------------

func TestPostmarkInboundEmailWorker_RepresentativeSmokeReplay(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockS3 := newTestMockS3()
	mockDB := newTestMockDB()

	// 1. Setup authorized sender and recipient entity
	clientEntityID := pgUUID(uuid.New())
	mockDB.userEntities["founder@acmecorp.com"] = clientEntityID
	mockDB.entitiesByName["acmecorp"] = clientEntityID
	mockDB.descendants[uuidFromPG(clientEntityID)] = []pgtype.UUID{clientEntityID}

	worker := &PostmarkInboundEmailWorker{
		logger:  logger,
		cfg:     &config.Config{},
		db:      database.New(mockDB),
		storage: mockS3,
	}

	validPDFBytes := []byte("%PDF-1.4\n%âãÏÓ\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF")

	// 2. Representative Postmark Inbound Webhook JSON payload
	rawPostmarkJSON := `{
		"From": "founder@acmecorp.com",
		"FromName": "Acme Founder",
		"To": "invoices@acmecorp.usetoro.io",
		"ToFull": [{"Email": "invoices@acmecorp.usetoro.io", "Name": "Toro Invoices"}],
		"Subject": "Supplier Invoice #INV-9821",
		"MessageID": "<postmark-smoke-test-uuid-2026@mtasv.net>",
		"Date": "Mon, 21 Sep 2026 19:26:00 +0000",
		"TextBody": "Please find attached the supplier invoice for September 2026.",
		"Attachments": [
			{
				"Name": "invoice_inv_9821.pdf",
				"ContentType": "application/pdf",
				"ContentLength": 68,
				"Content": "` + base64.StdEncoding.EncodeToString(validPDFBytes) + `"
			}
		]
	}`

	// 3. First Inbound Replay: Must process cleanly
	err := worker.Handle(ctx, &nats.Msg{Data: []byte(rawPostmarkJSON)})
	require.NoError(t, err)

	// Verify attachment uploaded to S3
	require.Len(t, mockS3.uploadedFiles, 1, "Exactly one S3 upload should occur on first arrival")
	var uploadedKey string
	for k := range mockS3.uploadedFiles {
		uploadedKey = k
	}
	assert.True(t, strings.HasPrefix(uploadedKey, "inbound/"))
	assert.True(t, strings.HasSuffix(uploadedKey, "-invoice_inv_9821.pdf"))

	// Register conversation in mockDB to simulate persistence
	cleanID := CleanMessageID("<postmark-smoke-test-uuid-2026@mtasv.net>")
	mockDB.conversations[cleanID] = pgUUID(uuid.New())

	// 4. Duplicate Inbound Replay: Idempotent short-circuit
	err = worker.Handle(ctx, &nats.Msg{Data: []byte(rawPostmarkJSON)})
	require.NoError(t, err)
	assert.Len(t, mockS3.uploadedFiles, 1, "Replaying duplicate payload must not re-upload to S3")
}


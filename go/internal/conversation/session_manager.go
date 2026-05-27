package conversation

import (
	"context"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// SessionManager handles CRUD operations for conversation sessions.
type SessionManager struct {
	db     *database.Queries
	logger *slog.Logger
}

func NewSessionManager(db *database.Queries, logger *slog.Logger) *SessionManager {
	return &SessionManager{db: db, logger: logger}
}

// FindOrCreateSession looks for an active session matching the participant,
// or creates a new one. Returns the session and whether it was newly created.
func (m *SessionManager) FindOrCreateSession(ctx context.Context, params FindOrCreateParams) (database.ToroCoreConversationSession, bool, error) {
	existing, err := m.db.GetActiveSessionByParticipant(ctx, database.GetActiveSessionByParticipantParams{
		EntityID:          params.EntityID,
		ParticipantHandle: params.ParticipantHandle,
		Source:            params.Source,
	})
	if err == nil && existing.ID.Valid {
		m.logger.Debug("found existing conversation session",
			"session_id", uuidToString(existing.ID),
			"participant", params.ParticipantHandle,
			"source", params.Source,
		)
		return existing, false, nil
	}

	session, err := m.insertSession(ctx, params)
	if err != nil {
		return database.ToroCoreConversationSession{}, false, err
	}
	return session, true, nil
}

// CreateSession always creates a new session without searching for an existing one.
// Use this for channels with threading (email) where each new message without a
// reply reference starts a fresh conversation.
func (m *SessionManager) CreateSession(ctx context.Context, params FindOrCreateParams) (database.ToroCoreConversationSession, error) {
	return m.insertSession(ctx, params)
}

func (m *SessionManager) insertSession(ctx context.Context, params FindOrCreateParams) (database.ToroCoreConversationSession, error) {
	m.logger.Info("creating new conversation session",
		"participant", params.ParticipantHandle,
		"source", params.Source,
	)

	var subject pgtype.Text
	if params.Subject != "" {
		subject = pgtype.Text{String: params.Subject, Valid: true}
	}

	var sysPrompt pgtype.Text
	if params.SystemPrompt != "" {
		sysPrompt = pgtype.Text{String: params.SystemPrompt, Valid: true}
	}

	contextJSON := params.ContextJSON
	if len(contextJSON) == 0 {
		contextJSON = []byte("{}")
	}

	return m.db.InsertConversationSession(ctx, database.InsertConversationSessionParams{
		EntityID:          params.EntityID,
		Source:            params.Source,
		ParticipantHandle: params.ParticipantHandle,
		ToroHandle:        params.ToroHandle,
		Subject:           subject,
		SystemPrompt:      sysPrompt,
		ContextJson:       contextJSON,
	})
}

// GetSession returns a session by ID.
func (m *SessionManager) GetSession(ctx context.Context, id pgtype.UUID) (database.ToroCoreConversationSession, error) {
	return m.db.GetConversationSession(ctx, id)
}

// UpdateSession updates a session's status and context.
// Pass nil/empty values for fields you don't want to change.
func (m *SessionManager) UpdateSession(ctx context.Context, id pgtype.UUID, status, systemPrompt string, contextJSON []byte) error {
	var statusParam pgtype.Text
	if status != "" {
		statusParam = pgtype.Text{String: status, Valid: true}
	}
	var sysPrompt pgtype.Text
	if systemPrompt != "" {
		sysPrompt = pgtype.Text{String: systemPrompt, Valid: true}
	}
	var ctxJSON []byte
	if len(contextJSON) > 0 {
		ctxJSON = contextJSON
	}
	return m.db.UpdateConversationSession(ctx, database.UpdateConversationSessionParams{
		ID:           id,
		Status:       statusParam,
		SystemPrompt: sysPrompt,
		ContextJson:  ctxJSON,
	})
}

// GetSessionHistory returns all messages for a session in chronological order.
func (m *SessionManager) GetSessionHistory(ctx context.Context, sessionID pgtype.UUID) ([]database.ToroCoreConversation, error) {
	return m.db.GetSessionConversations(ctx, sessionID)
}

// FindOrCreateParams are the parameters for FindOrCreateSession.
type FindOrCreateParams struct {
	EntityID          pgtype.UUID
	Source            string
	ParticipantHandle string
	ToroHandle        string
	Subject           string
	SystemPrompt      string
	ContextJSON       []byte
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

package fignode

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
)

type Handler struct {
	logger        *slog.Logger
	db            *database.Queries
	pool          *pgxpool.Pool
	redis         *redis.Client
	cache         *Cache
	privKey       ed25519.PrivateKey
	authenticator *auth.Authenticator
	emailSender   auth.EmailSender
	leaderboard   *LeaderboardService
}

func NewHandler(
	logger *slog.Logger,
	db *database.Queries,
	pool *pgxpool.Pool,
	redisClient *redis.Client,
	cache *Cache,
	privKey ed25519.PrivateKey,
	authenticator *auth.Authenticator,
	emailSender auth.EmailSender,
	leaderboard *LeaderboardService,
) *Handler {
	return &Handler{
		logger:        logger,
		db:            db,
		pool:          pool,
		redis:         redisClient,
		cache:         cache,
		privKey:       privKey,
		authenticator: authenticator,
		emailSender:   emailSender,
		leaderboard:   leaderboard,
	}
}

// NewRouter builds the http.ServeMux for all Fignode API routes.
func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()
	// Protected (require auth)
	mux.Handle("GET /api/v1/user/stats", h.requireAuth(http.HandlerFunc(h.HandleGetStats)))
	mux.Handle("GET /api/v1/leaderboard", h.requireAuth(http.HandlerFunc(h.HandleGetLeaderboard)))

	mux.Handle("POST /api/v1/team/invite", h.requireAuth(http.HandlerFunc(h.HandleTeamInvite)))
	mux.Handle("POST /api/v1/team/invite/validate", h.requireAuth(http.HandlerFunc(h.HandleTeamInviteValidate)))

	// Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return mux
}

// --- Auth middleware ---

func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := r.Header.Get("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		} else {
			writeError(w, 401, "UNAUTHORIZED", "Missing or malformed Authorization header")
			return
		}

		claims, err := h.authenticator.VerifyToken(r.Context(), tokenStr)
		if err != nil {
			writeError(w, 401, "UNAUTHORIZED", "Invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), auth.UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, auth.EntityIDKey, claims.EntityID)
		ctx = context.WithValue(ctx, auth.RoleKey, claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getUserID(r *http.Request) (uuid.UUID, bool) {
	val, ok := r.Context().Value(auth.UserIDKey).(uuid.UUID)
	return val, ok
}

// --- Handlers ---

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "INVALID_BODY", "Invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" || req.FirstName == "" || req.LastName == "" {
		writeError(w, 400, "MISSING_FIELDS", "email, password, first name, and last name are required")
		return
	}

	hash := hashPassword(req.Password)

	// Employees get a standalone entity (type=employee)
	entityID, err := h.db.CreateEntity(r.Context(), database.CreateEntityParams{
		Name:       req.Email, // Organization name
		EntityType: "employee",
		PlanTier:   pgtype.Text{String: "internal", Valid: true},
	})
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to create entity")
		return
	}

	userID, err := h.db.CreateEmployeeUser(r.Context(), database.CreateEmployeeUserParams{
		EntityID:     entityID,
		Email:        req.Email,
		PasswordHash: hash,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			writeError(w, 409, "CONFLICT", "Email already taken")
			return
		}
		writeError(w, 500, "INTERNAL", "Failed to create user")
		return
	}

	var ptFirstName pgtype.Text
	ptFirstName.Scan(req.FirstName)
	var ptLastName pgtype.Text
	ptLastName.Scan(req.LastName)

	if err := h.db.CreateEmployeeProfile(r.Context(), database.CreateEmployeeProfileParams{
		UserID:    userID,
		FirstName: ptFirstName,
		LastName:  ptLastName,
		IsManager: req.IsManager,
	}); err != nil {
		writeError(w, 500, "INTERNAL", "Failed to create employee profile")
		return
	}

	token, err := h.signToken(userID, entityID)
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to generate token")
		return
	}

	pub, err := h.db.GetEmployeePublic(r.Context(), userID)
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to read profile")
		return
	}

	writeJSON(w, 200, AuthResponse{
		Token: token,
		User:  employeePublicToResponse(pub),
	})
}

func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "INVALID_BODY", "Invalid request body")
		return
	}

	employee, err := h.db.GetEmployeeByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, 401, "INVALID_CREDENTIALS", "Invalid email or password")
		return
	}

	if !checkPassword(req.Password, employee.PasswordHash) {
		writeError(w, 401, "INVALID_CREDENTIALS", "Invalid email or password")
		return
	}

	token, err := h.signToken(employee.ID, employee.EntityID)
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to generate token")
		return
	}

	pub, err := h.db.GetEmployeePublic(r.Context(), employee.ID)
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to read profile")
		return
	}

	writeJSON(w, 200, AuthResponse{
		Token: token,
		User:  employeePublicToResponse(pub),
	})
}

func (h *Handler) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeError(w, 401, "UNAUTHORIZED", "Missing user context")
		return
	}

	if cached, ok := h.cache.GetStats(r.Context(), userID); ok {
		writeJSON(w, 200, cached)
		return
	}

	pgUID := pgtype.UUID{Bytes: userID, Valid: true}
	stats, err := h.db.GetEmployeeStats(r.Context(), pgUID)
	if err != nil {
		writeError(w, 500, "INTERNAL", "Failed to fetch stats")
		return
	}

	resp := &UserStats{
		TotalCleared:    int(stats.TotalCleared),
		TodayCleared:    int(stats.TodayCleared),
		Streak:          int(stats.Streak),
		AiAccuracyScore: numericToFloat64(stats.AiAccuracyScore),
	}

	h.cache.SetStats(r.Context(), userID, resp)
	writeJSON(w, 200, resp)
}

func (h *Handler) HandleGetLeaderboard(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeError(w, 401, "UNAUTHORIZED", "Missing user context")
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "all-time"
	}

	if cached, ok := h.cache.GetLeaderboard(r.Context(), period); ok {
		// Mark current user in cached data
		pgUID := pgtype.UUID{Bytes: userID, Valid: true}
		if employee, err := h.db.GetEmployeeByID(r.Context(), pgUID); err == nil {
			for i := range cached {
				cached[i].IsCurrentUser = cached[i].Email == employee.Email
			}
		}
		writeJSON(w, 200, cached)
		return
	}

	items, err := h.leaderboard.GetLeaderboard(r.Context(), period, userID)
	if err != nil {
		if ae, ok := AsAppError(err); ok {
			writeError(w, ae.Status, ae.Code, ae.Message)
			return
		}
		writeError(w, 500, "INTERNAL", "Failed to fetch leaderboard")
		return
	}

	h.cache.SetLeaderboard(r.Context(), period, items)
	writeJSON(w, 200, items)
}

// --- Helpers ---

func (h *Handler) signToken(userID, entityID pgtype.UUID) (string, error) {
	uid := uuid.UUID(userID.Bytes)
	eid := uuid.UUID(entityID.Bytes)

	claims := auth.UserClaims{
		UserID:   uid,
		EntityID: eid,
		Role:     "member",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			Issuer:    "usetoro-auth",
			Subject:   uid.String(),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(h.privKey)
}

func hashPassword(password string) string {
	salt := []byte("fignode-employee-salt")
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return hex.EncodeToString(hash)
}

func checkPassword(password, hash string) bool {
	return hashPassword(password) == hash
}

func employeePublicToResponse(p database.GetEmployeePublicRow) UserPublic {
	var firstName *string
	if p.FirstName.Valid {
		f := p.FirstName.String
		firstName = &f
	}
	var lastName *string
	if p.LastName.Valid {
		l := p.LastName.String
		lastName = &l
	}
	return UserPublic{
		ID:              uuid.UUID(p.ID.Bytes).String(),
		Email:           p.Email,
		FirstName:       firstName,
		LastName:        lastName,
		IsManager:       p.IsManager,
		AiAccuracyScore: numericToFloat64(p.AiAccuracyScore),
		Streak:          int(p.Streak),
		TotalCleared:    int(p.TotalCleared),
		TodayCleared:    int(p.TodayCleared),
		CreatedAt:       p.CreatedAt.Time.Format(time.RFC3339),
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorEnvelope{
		Error: APIError{Code: code, Message: message, StatusCode: status},
	})
}

func isDuplicateKeyError(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// numericToFloat64Precise returns higher precision
func numericToFloat64Precise(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return math.Round(f.Float64*1000) / 1000
}

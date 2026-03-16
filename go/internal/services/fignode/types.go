package fignode

import (
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	BatchSize = 50
)

// --- API response / request types ---

type TransactionResponse struct {
	ID                string        `json:"id"`
	RawDescription    string        `json:"rawDescription"`
	Vendor            string        `json:"vendor"`
	Industry          string        `json:"industry"`
	IndustryIcon      string        `json:"industryIcon"`
	VendorDescription string        `json:"vendorDescription"`
	VendorUrl         *string       `json:"vendorUrl"`
	Location          string        `json:"location"`
	IsRecurring       bool          `json:"isRecurring"`
	ClientContext     ClientContext `json:"clientContext"`
	Amount            float64       `json:"amount"`
	Date              string        `json:"date"`
	AccountType       string        `json:"accountType"`
	Timestamp         string        `json:"timestamp"`
	AiSuggestion      string        `json:"aiSuggestion"`
	AiConfidence      float64       `json:"aiConfidence"`
	Status            string        `json:"status"`
}

type ClientContext struct {
	Industry      string `json:"industry"`
	IndustryIcon  string `json:"industryIcon"`
	BusinessModel string `json:"businessModel"`
	MindsetHint   string `json:"mindsetHint"`
	AccentColor   string `json:"accentColor"`
	AccentBg      string `json:"accentBg"`
}

type ClassifyRequest struct {
	Category string `json:"category"`
	Action   string `json:"action"` // APPROVE or RECLASSIFY
}

type ClassifyResponse struct {
	TransactionID string `json:"transactionId"`
	Category      string `json:"category"`
	Action        string `json:"action"`
	Status        string `json:"status"`
}

type SkipResponse struct {
	TransactionID string `json:"transactionId"`
	Skipped       bool   `json:"skipped"`
}

type UserPublic struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	FirstName       *string `json:"firstName"`
	LastName        *string `json:"lastName"`
	IsManager       bool    `json:"isManager"`
	AiAccuracyScore float64 `json:"aiAccuracyScore"`
	Streak          int     `json:"streak"`
	TotalCleared    int     `json:"totalCleared"`
	TodayCleared    int     `json:"todayCleared"`
	CreatedAt       string  `json:"createdAt"`
}

type UserStats struct {
	TotalCleared    int     `json:"totalCleared"`
	TodayCleared    int     `json:"todayCleared"`
	Streak          int     `json:"streak"`
	AiAccuracyScore float64 `json:"aiAccuracyScore"`
}

type LeaderboardItem struct {
	Rank          int      `json:"rank"`
	Email         string   `json:"email"`
	Cleared       int      `json:"cleared"`
	Streak        int      `json:"streak"`
	Badges        []string `json:"badges"`
	IsCurrentUser bool     `json:"isCurrentUser"`
}

type RegisterRequest struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	IsManager bool   `json:"isManager"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string     `json:"token"`
	User  UserPublic `json:"user"`
}

type TeamInviteRequest struct {
	Email    string `json:"email"`
	TenantID string `json:"tenantId"`
	FirmName string `json:"firmName"`
}

type TeamInviteResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
}

type TeamInviteValidateRequest struct {
	Token string `json:"token"`
}

type TeamInviteValidateResponse struct {
	Valid    bool   `json:"valid"`
	Email    string `json:"email"`
	TenantID string `json:"tenantId"`
	FirmName string `json:"firmName"`
}

type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

// --- Conversion helpers ---

func numericToFloat64(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return math.Round(f.Float64*100) / 100
}

func float64ToNumeric(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf("%f", f))
	return n
}

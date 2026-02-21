package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type UserClaims struct {
	UserID   uuid.UUID `json:"user_id"`
	EntityID uuid.UUID `json:"entity_id"`
	Role     string    `json:"role"`
	jwt.RegisteredClaims
}

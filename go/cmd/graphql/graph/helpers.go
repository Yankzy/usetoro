package graph

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/cmd/graphql/dataloader"
	"github.com/Yankzy/usetoro/cmd/graphql/graph/model"
	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

func UserLoader(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	loader := dataloader.For(ctx).UserLoader
	thunk := loader.Load(ctx, dataloader.StringKey(userID.String()))
	result, err := thunk()
	if err != nil {
		return nil, err
	}

	user, ok := result.(database.ToroCoreUser)
	if !ok {
		return nil, fmt.Errorf("system error: invalid user type")
	}

	return &model.User{
		ID:       uuid.UUID(user.ID.Bytes).String(),
		Email:    user.Email,
		Role:     user.Role.String,
		TenantID: uuid.UUID(user.EntityID.Bytes).String(),
	}, nil
}

func hashPassword(password string) string {
	salt := []byte("somesalt")
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return hex.EncodeToString(hash)
}

func checkPassword(password, hash string) bool {
	return hashPassword(password) == hash
}

func generateTokens(user database.ToroCoreUser, privKey ed25519.PrivateKey) (string, string, time.Time, error) {
	userID, err := uuid.FromBytes(user.ID.Bytes[:])
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid user uuid: %w", err)
	}
	entityID, err := uuid.FromBytes(user.EntityID.Bytes[:])
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid entity uuid: %w", err)
	}

	claims := auth.UserClaims{
		UserID:   userID,
		EntityID: entityID,
		Role:     user.Role.String,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			Issuer:    "usetoro-auth",
			Subject:   userID.String(),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	accessToken, err := token.SignedString(privKey)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshToken := uuid.New().String()
	expires := time.Now().Add(7 * 24 * time.Hour)
	return accessToken, refreshToken, expires, nil
}

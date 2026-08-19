// Package auth handles admin/dashboard authentication: password hashing
// and JWT issuance/verification for the Flutter app's REST API session.
//
// This is JWT-based rather than session-cookie-based specifically because
// the client is a Flutter app across mobile/desktop/web, not a browser —
// see the M0 plan's UI section for that tradeoff.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken covers any parse/verify failure or expiry, deliberately
// undifferentiated so callers don't leak which case it was.
var ErrInvalidToken = errors.New("invalid or expired token")

const accessTokenTTL = 24 * time.Hour

type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// Manager issues and verifies JWTs signed with a single shared secret.
//
// MVP scope: no refresh-token flow — a token is valid for accessTokenTTL
// and the client re-logs-in after that. Fine for a single-admin-user
// self-hosted tool; a refresh flow is a near-term follow-up if this grows
// beyond that.
type Manager struct {
	secret []byte
}

func NewManager(secret []byte) *Manager {
	return &Manager{secret: secret}
}

func (m *Manager) IssueToken(userID, email string) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(accessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m *Manager) ParseToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

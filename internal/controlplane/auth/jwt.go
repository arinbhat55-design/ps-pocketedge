// Package auth handles admin/dashboard authentication: password hashing
// and JWT issuance/verification for the Flutter app's REST API session.
//
// This is JWT-based rather than session-cookie-based specifically because
// the client is a Flutter app across mobile/desktop/web, not a browser —
// see the M0 plan's UI section for that tradeoff.
package auth

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken covers any parse/verify failure or expiry, deliberately
// undifferentiated so callers don't leak which case it was.
var ErrInvalidToken = errors.New("invalid or expired token")

// ErrAuthUnavailable means the token may be fine but the user's current role
// could not be checked (e.g. the database is down). Callers answer 503, not
// 401, so a transient outage doesn't sign clients out.
var ErrAuthUnavailable = errors.New("authentication temporarily unavailable")

const accessTokenTTL = 24 * time.Hour

type Claims struct {
	UserID  string `json:"uid"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	Local   bool   `json:"local,omitempty"`
	Version int    `json:"version"`
	jwt.RegisteredClaims
}

// Manager issues and verifies JWTs signed with a single shared secret.
//
// MVP scope: no refresh-token flow — a token is valid for accessTokenTTL
// and the client re-logs-in after that. Fine for a single-admin-user
// self-hosted tool; a refresh flow is a near-term follow-up if this grows
// beyond that.
type Manager struct {
	secret               []byte
	localSessionsAllowed atomic.Bool
	roleLookup           func(context.Context, string) (string, error)
	dashboardOrigins     map[string]bool
}

func NewManager(secret []byte) *Manager {
	return &Manager{secret: secret}
}

func (m *Manager) IssueToken(userID, email, role string) (string, error) {
	return m.issueToken(userID, email, role, false)
}

func (m *Manager) IssueLocalToken(userID, email, role string) (string, error) {
	if !m.LocalSessionsAllowed() {
		return "", ErrInvalidToken
	}
	return m.issueToken(userID, email, role, true)
}

func (m *Manager) SetLocalSessionsAllowed(allowed bool) {
	m.localSessionsAllowed.Store(allowed)
}

func (m *Manager) LocalSessionsAllowed() bool {
	return m.localSessionsAllowed.Load()
}

// SetRoleLookup makes authorization use the user's current role, so deleting
// or demoting an account takes effect without waiting for its JWT to expire.
// lookup returns "" with a nil error for a user that no longer exists; any
// error is treated as the lookup being unavailable. Configure it before
// serving requests.
func (m *Manager) SetRoleLookup(lookup func(context.Context, string) (string, error)) {
	m.roleLookup = lookup
}

// AuthenticateRequest verifies a token presented on r. Local-session tokens
// are only honored on requests that are themselves local, so a copied token
// is useless from another machine or through a proxy.
func (m *Manager) AuthenticateRequest(r *http.Request, token string) (*Claims, error) {
	claims, err := m.ParseToken(token)
	if err != nil {
		return nil, err
	}
	if claims.Local && !m.IsLocalRequest(r) {
		return nil, ErrInvalidToken
	}
	if m.roleLookup != nil {
		role, err := m.roleLookup(r.Context(), claims.UserID)
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		claims.Role = role
	}
	if claims.Role != "admin" && claims.Role != "viewer" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// WriteAuthError answers a failed AuthenticateRequest.
func WriteAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrAuthUnavailable) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (m *Manager) issueToken(userID, email, role string, local bool) (string, error) {
	claims := Claims{
		UserID:  userID,
		Email:   email,
		Role:    role,
		Local:   local,
		Version: 1,
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
	if err != nil || !token.Valid || claims.Version != 1 || (claims.Local && !m.LocalSessionsAllowed()) {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

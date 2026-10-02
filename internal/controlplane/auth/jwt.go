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
	// SessionVersion is the user's session version when the token was
	// issued; the token stops working once it changes (see UserState).
	// Tokens from before it existed carry 0, which counts as 1.
	SessionVersion int `json:"sv,omitempty"`
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
	userLookup           func(context.Context, string) (UserState, error)
	dashboardOrigins     map[string]bool
}

func NewManager(secret []byte) *Manager {
	return &Manager{secret: secret}
}

// IssueToken issues a login session token. sessionVersion is the user's
// current session version (store.User.SessionVersion).
func (m *Manager) IssueToken(userID, email, role string, sessionVersion int) (string, error) {
	return m.issueToken(userID, email, role, sessionVersion, false)
}

func (m *Manager) IssueLocalToken(userID, email, role string, sessionVersion int) (string, error) {
	if !m.LocalSessionsAllowed() {
		return "", ErrInvalidToken
	}
	return m.issueToken(userID, email, role, sessionVersion, true)
}

func (m *Manager) SetLocalSessionsAllowed(allowed bool) {
	m.localSessionsAllowed.Store(allowed)
}

func (m *Manager) LocalSessionsAllowed() bool {
	return m.localSessionsAllowed.Load()
}

// UserState is what AuthenticateRequest checks a token against on every
// request.
type UserState struct {
	// Role is the user's current role; "" for a user that no longer exists.
	Role string
	// SessionVersion, when non-zero, rejects tokens issued under any other
	// version (it's bumped when the user's password changes).
	SessionVersion int
}

// SetUserLookup makes authorization use the user's current state, so
// deleting or demoting an account, or changing its password, takes effect
// without waiting for its JWT to expire. Any error is treated as the
// lookup being unavailable. Configure it before serving requests.
func (m *Manager) SetUserLookup(lookup func(context.Context, string) (UserState, error)) {
	m.userLookup = lookup
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
	if m.userLookup != nil {
		user, err := m.userLookup(r.Context(), claims.UserID)
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		claims.Role = user.Role
		tokenSession := claims.SessionVersion
		if tokenSession == 0 {
			tokenSession = 1
		}
		if user.SessionVersion != 0 && tokenSession != user.SessionVersion {
			return nil, ErrInvalidToken
		}
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

func (m *Manager) issueToken(userID, email, role string, sessionVersion int, local bool) (string, error) {
	claims := Claims{
		UserID:         userID,
		Email:          email,
		Role:           role,
		Local:          local,
		Version:        1,
		SessionVersion: sessionVersion,
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

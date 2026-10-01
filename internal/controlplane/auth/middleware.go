package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey int

const claimsContextKey contextKey = 0

// RequireAuth wraps next, rejecting requests without a valid
// `Authorization: Bearer <jwt>` header and injecting the parsed Claims into
// the request context for handlers to read via ClaimsFromContext.
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		claims, err := m.AuthenticateRequest(r, token)
		if err != nil {
			WriteAuthError(w, err)
			return
		}
		if claims.Role == "viewer" && !viewerMayAccess(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), claimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Viewers can use read endpoints and the few POST endpoints that only
// inspect/transform data. New write routes are denied by default.
func viewerMayAccess(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch r.Pattern {
		case "GET /api/servers/{id}/volumes/{name}/files/content",
			"GET /api/compose-files",
			"GET /api/compose-files/{id}",
			"GET /api/compose-files/{id}/versions",
			"GET /api/compose-files/{id}/versions/{versionId}",
			"GET /api/env-var-groups",
			"GET /api/env-var-groups/{id}",
			"GET /api/git-repositories/{id}/file",
			"GET /api/deployments/{id}/revisions/{revision}":
			return false
		}
		return true
	}
	if r.Method == http.MethodPost {
		switch r.Pattern {
		case "POST /api/auth/change-password",
			"POST /api/servers/{id}/containers/{containerId}/inspect",
			"POST /api/compose-files/parse",
			"POST /api/compose-files/render",
			"POST /api/deployments/preview",
			"POST /api/databases/preview":
			return true
		}
	}
	return false
}

// RequireAdmin wraps next like RequireAuth, additionally rejecting
// requests whose caller isn't role "admin" with 403.
func (m *Manager) RequireAdmin(next http.Handler) http.Handler {
	return m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok || claims.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// ClaimsFromContext retrieves the Claims injected by RequireAuth.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*Claims)
	return claims, ok
}

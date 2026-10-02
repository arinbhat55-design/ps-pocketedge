package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// uuidPathParams are the route wildcards that always hold a database UUID.
// Any other wildcard (containerId, name, revision, ...) is left alone.
var uuidPathParams = map[string]bool{"id": true, "userId": true, "versionId": true, "repoId": true}

// uuidQueryParams are the query parameters that, when present, always
// hold a database UUID.
var uuidQueryParams = []string{"serverId", "deploymentId", "registryId", "composeFileId", "actorId", "ownerId"}

// isUUID reports whether s is a UUID in its canonical 36-character form,
// the only form Postgres's uuid type is guaranteed to accept.
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && len(s) == 36
}

// withPathIDValidation answers 400 for a request whose UUID wildcard or
// UUID query parameter isn't a UUID, before the handler passes it to
// Postgres — which would reject it with a type error and turn it into a
// 500.
func withPathIDValidation(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			if name, value, ok := invalidUUIDParam(pattern, r.URL.Path); !ok {
				http.Error(w, name+" "+value+" is not a valid ID", http.StatusBadRequest)
				return
			}
			query := r.URL.Query()
			for _, name := range uuidQueryParams {
				if value := query.Get(name); value != "" && !isUUID(value) {
					http.Error(w, name+" "+value+" is not a valid ID", http.StatusBadRequest)
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// invalidUUIDParam matches path against a ServeMux pattern ("GET
// /api/servers/{id}") segment by segment and reports the first UUID
// wildcard whose value isn't a UUID; ok is true when there is none.
func invalidUUIDParam(pattern, path string) (name, value string, ok bool) {
	if _, p, found := strings.Cut(pattern, " "); found {
		pattern = p
	}
	patternSegs := strings.Split(strings.Trim(pattern, "/"), "/")
	pathSegs := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range patternSegs {
		if i >= len(pathSegs) || !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		if !uuidPathParams[name] {
			continue
		}
		if !isUUID(pathSegs[i]) {
			return name, pathSegs[i], false
		}
	}
	return "", "", true
}

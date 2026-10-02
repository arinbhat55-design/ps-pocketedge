package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// uuidPathParams are the route wildcards that always hold a database UUID.
// Any other wildcard (containerId, name, revision, ...) is left alone.
var uuidPathParams = map[string]bool{"id": true, "userId": true, "versionId": true, "repoId": true}

// withPathIDValidation answers 400 for a request whose UUID wildcard isn't
// a UUID, before the handler passes it to Postgres — which would reject it
// with a type error and turn it into a 500.
func withPathIDValidation(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			if name, value, ok := invalidUUIDParam(pattern, r.URL.Path); !ok {
				http.Error(w, name+" "+value+" is not a valid ID", http.StatusBadRequest)
				return
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
		if _, err := uuid.Parse(pathSegs[i]); err != nil || len(pathSegs[i]) != 36 {
			return name, pathSegs[i], false
		}
	}
	return "", "", true
}

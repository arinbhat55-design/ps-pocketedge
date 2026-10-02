package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessDoesNotExposeDatabaseFailure(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		handler := handleReadiness(func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("missing readiness timeout")
			}
			if healthy {
				return nil
			}
			return errors.New("postgres://user:secret@host/db")
		})
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest("GET", "/api/health", nil))
		want := http.StatusOK
		if !healthy {
			want = http.StatusServiceUnavailable
		}
		if response.Code != want || strings.Contains(response.Body.String(), "secret") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}

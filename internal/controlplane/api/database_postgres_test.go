package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

func TestPgIdentQuotesOnlySafeNames(t *testing.T) {
	for in, want := range map[string]string{
		"app":         `"app"`,
		"Reporting_1": `"Reporting_1"`,
		"_x":          `"_x"`,
	} {
		got, err := pgIdent(in)
		if err != nil || got != want {
			t.Errorf("pgIdent(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "1app", "app-db", "app db", `app"; DROP DATABASE x; --`, "app;", "pg_monitor", "PG_admin",
		"a234567890123456789012345678901234567890123456789012345678901234", // 64 chars, over NAMEDATALEN-1
	} {
		_, err := pgIdent(in)
		var ae *actionError
		if !errors.As(err, &ae) || ae.status != 400 {
			t.Errorf("pgIdent(%q) = %v, want a 400 action error", in, err)
		}
	}
}

func TestPgLiteralEscapesQuotes(t *testing.T) {
	for in, want := range map[string]string{
		"plain":           "'plain'",
		"it's":            "'it''s'",
		"'; DROP ROLE x;": "'''; DROP ROLE x;'",
		"":                "''",
	} {
		if got := pgLiteral(in); got != want {
			t.Errorf("pgLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPostgresMajor(t *testing.T) {
	for in, want := range map[string]int{"17": 17, "17.2": 17, "18.0": 18} {
		if got, err := postgresMajor(in); err != nil || got != want {
			t.Errorf("postgresMajor(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "latest", "17.2.1", "17-alpine", ".1"} {
		if _, err := postgresMajor(in); err == nil {
			t.Errorf("postgresMajor(%q) accepted", in)
		}
	}
}

func TestValidatePostgresGrant(t *testing.T) {
	inst := &store.DatabaseInstance{AdminUsername: "dbadmin"}
	for _, perm := range []string{"read", "write", "none"} {
		if err := validatePostgresGrant(inst, "reporting", perm); err != nil {
			t.Errorf("permission %q rejected: %v", perm, err)
		}
	}
	for name, c := range map[string]struct{ user, perm string }{
		"empty permission":   {"reporting", ""},
		"unknown permission": {"reporting", "admin"},
		"administrator":      {"dbadmin", "read"},
		"administrator case": {"DBAdmin", "none"},
		"postgres superuser": {"postgres", "write"},
	} {
		if err := validatePostgresGrant(inst, c.user, c.perm); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The admin endpoints decode psql's single-value output as JSON, so each
// query must run on a stock server and yield the shape the app reads.
// Needs TEST_DATABASE_URL, like the store integration tests.
func TestPostgresAdminSQLRuns(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL SQL integration test")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	query := func(sql string) any {
		t.Helper()
		var raw string
		if err := conn.QueryRow(ctx, "SELECT ("+sql+")::text").Scan(&raw); err != nil {
			t.Fatalf("query failed: %v\n%s", err, sql)
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, raw)
		}
		return v
	}

	var state string
	if err := conn.QueryRow(ctx, pgInsightsStateSQL).Scan(&state); err != nil {
		t.Fatalf("insights state query failed: %v", err)
	}
	// A stock server has neither the extension nor the preload.
	if state != "missing" {
		t.Errorf("insights state = %q, want missing", state)
	}

	if _, ok := query(pgSessionsSQL).([]any); !ok {
		t.Error("sessions query did not return a JSON array")
	}
	sizes, ok := query(pgSizesSQL).(map[string]any)
	if !ok {
		t.Fatal("sizes query did not return a JSON object")
	}
	dbs, _ := sizes["databases"].([]any)
	if len(dbs) == 0 {
		t.Fatalf("sizes.databases empty: %v", sizes)
	}
	if first, _ := dbs[0].(map[string]any); first["name"] == nil || first["bytes"] == nil {
		t.Errorf("database size entry = %v", dbs[0])
	}
	if _, ok := sizes["tables"].([]any); !ok {
		t.Errorf("sizes.tables not an array: %v", sizes["tables"])
	}
}

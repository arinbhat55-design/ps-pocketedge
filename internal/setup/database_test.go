package setup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This integration test creates and deletes its own database, never touching
// tables in the supplied database. Set SETUP_TEST_DATABASE_URL to opt in.
func TestMigrationsOnFreshDatabaseAndRetry(t *testing.T) {
	address := os.Getenv("SETUP_TEST_DATABASE_URL")
	if address == "" {
		t.Skip("set SETUP_TEST_DATABASE_URL for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("pspe_setup_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)")
	target, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	target.Path = "/" + name
	if err = migrate(ctx, target.String()); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, target.String()); err != nil {
		t.Fatal("retry failed", err)
	}
	conn, err := pgx.Connect(ctx, target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM pspe_setup_migrations").Scan(&count); err != nil || count != 31 {
		t.Fatal("incomplete schema", count, err)
	}
	var standardVersion int
	var dirty bool
	if err = conn.QueryRow(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&standardVersion, &dirty); err != nil || standardVersion != 31 || dirty {
		t.Fatal("incompatible standard migration history", standardVersion, dirty, err)
	}
	if _, err = conn.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
		t.Fatal(err)
	}
	if migrate(ctx, target.String()) == nil {
		t.Fatal("accepted dirty standard migration history")
	}
	if _, err = conn.Exec(ctx, "UPDATE schema_migrations SET dirty=false"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO pspe_setup_migrations(version) VALUES(999)"); err != nil {
		t.Fatal(err)
	}
	if migrate(ctx, target.String()) == nil {
		t.Fatal("accepted a newer schema")
	}
}

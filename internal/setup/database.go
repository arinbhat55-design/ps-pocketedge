package setup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/migrations"
	"github.com/jackc/pgx/v5"
)

// migrate uses an advisory lock and a transaction per migration. It recognizes
// golang-migrate history and refuses to guess the version of an existing DB.
func migrate(ctx context.Context, databaseURL string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, "SET search_path TO public"); err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(724910832)"); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(724910832)")
	var ours, legacy, users bool
	err = conn.QueryRow(ctx, "SELECT to_regclass('public.pspe_setup_migrations') IS NOT NULL, to_regclass('public.schema_migrations') IS NOT NULL, to_regclass('public.users') IS NOT NULL").Scan(&ours, &legacy, &users)
	if err != nil {
		return err
	}
	version, installedVersion := 0, 0
	if ours {
		err = conn.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM pspe_setup_migrations").Scan(&installedVersion)
		if err != nil {
			return err
		}
		version = installedVersion
	}
	if legacy {
		var dirty bool
		var rows int
		if err = conn.QueryRow(ctx, "SELECT count(*),COALESCE(MAX(version),0),COALESCE(BOOL_OR(dirty),false) FROM schema_migrations").Scan(&rows, &version, &dirty); err != nil {
			return err
		}
		if dirty {
			return errors.New("database has an incomplete migration; repair it before continuing")
		}
		if rows > 1 || (rows == 0 && users) || installedVersion > version {
			return errors.New("database migration histories conflict; repair them before continuing")
		}
	} else if users && !ours {
		return errors.New("existing application database has no verified migration history; setup will not modify it")
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	latest := len(entries)
	if version > latest {
		return errors.New("database schema is newer than this installer; use a newer installer")
	}
	if _, err = conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS pspe_setup_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	if !legacy {
		if _, err = conn.Exec(ctx, "CREATE TABLE schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL)"); err != nil {
			return err
		}
		if _, err = conn.Exec(ctx, "INSERT INTO schema_migrations(version,dirty) VALUES($1,false)", version); err != nil {
			return err
		}
	}
	if version > 0 {
		if _, err = conn.Exec(ctx, "INSERT INTO pspe_setup_migrations(version) VALUES($1) ON CONFLICT DO NOTHING", version); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		n, _ := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if n <= version {
			continue
		}
		sql, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, string(sql))
		if err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO pspe_setup_migrations(version) VALUES($1)", n)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "DELETE FROM schema_migrations")
			if err == nil {
				_, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version,dirty) VALUES($1,false)", n)
			}
		}
		if err != nil {
			tx.Rollback(context.Background())
			return fmt.Errorf("migration %04d failed: %w", n, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// driverName holds the current DB driver ("sqlite" or "postgres").
// Set by Open; used by Rebind.
var driverName string

// Rebind rewrites ? placeholders to $1, $2, … for Postgres.
// For SQLite it returns the query unchanged.
func Rebind(query string) string {
	if driverName != "postgres" {
		return query
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			fmt.Fprintf(&b, "$%d", n)
			n++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

// Open opens the database, runs migrations, and returns the *sql.DB.
// dbURL may be "sqlite://./stackd.db" or "postgres://...".
func Open(dbURL string) (*sql.DB, error) {
	var (
		db  *sql.DB
		err error
	)

	scheme, _, found := strings.Cut(dbURL, "://")
	if !found {
		return nil, fmt.Errorf("db.Open: invalid DB_URL %q (expected scheme://...)", dbURL)
	}

	switch scheme {
	case "sqlite":
		filePath := strings.TrimPrefix(dbURL, "sqlite://")
		db, err = sql.Open("sqlite", filePath)
		if err != nil {
			return nil, fmt.Errorf("db.Open sqlite: %w", err)
		}
		if _, err = db.ExecContext(context.Background(), "PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, fmt.Errorf("db.Open WAL pragma: %w", err)
		}
		driverName = "sqlite"
	case "postgres", "postgresql":
		db, err = sql.Open("pgx", dbURL)
		if err != nil {
			return nil, fmt.Errorf("db.Open postgres: %w", err)
		}
		driverName = "postgres"
	default:
		return nil, fmt.Errorf("db.Open: unsupported scheme %q", scheme)
	}

	if err = db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db.Open ping: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("db.Open iofs: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("db.Open migrate: failed to open database: %w", err)
	}
	defer m.Close()

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		// Dirty state means a previous migration attempt failed mid-run.
		// Force version back to uninitialized so we can retry cleanly.
		var dirtyErr *migrate.ErrDirty
		if errors.As(err, &dirtyErr) {
			slog.Warn("dirty migration state detected, forcing reset", "version", dirtyErr.Version)
			if ferr := m.Force(-1); ferr != nil {
				db.Close()
				return nil, fmt.Errorf("db.Open migrate force: %w", ferr)
			}
			if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				db.Close()
				return nil, fmt.Errorf("db.Open migrate up (retry): %w", err)
			}
		} else {
			db.Close()
			return nil, fmt.Errorf("db.Open migrate up: %w", err)
		}
	}

	slog.Info("database ready", "driver", driverName, "url", dbURL)
	return db, nil
}

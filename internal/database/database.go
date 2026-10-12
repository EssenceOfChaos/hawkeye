// Package database owns PostgreSQL connectivity and schema migrations.
package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pgx pool and verifies the database is reachable.
func Connect(ctx context.Context, url string, maxConns int32, timeout time.Duration) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = maxConns

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func newMigrator(url string, files fs.FS) (*migrate.Migrate, error) {
	src, err := iofs.New(files, ".")
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	// golang-migrate selects its driver from the URL scheme.
	m, err := migrate.NewWithSourceInstance("iofs", src, toMigrateURL(url))
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}
	return m, nil
}

// MigrateUp applies all pending migrations. It is a no-op when the schema is current.
func MigrateUp(url string, files fs.FS) error {
	m, err := newMigrator(url, files)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrateDown reverts the most recent migration.
func MigrateDown(url string, files fs.FS) error {
	m, err := newMigrator(url, files)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migration: %w", err)
	}
	return nil
}

// Version reports the current schema version and whether it is dirty.
func Version(url string, files fs.FS) (uint, bool, error) {
	m, err := newMigrator(url, files)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

func toMigrateURL(url string) string {
	for _, p := range []string{"postgresql://", "postgres://"} {
		if len(url) > len(p) && url[:len(p)] == p {
			return "pgx5://" + url[len(p):]
		}
	}
	return url
}

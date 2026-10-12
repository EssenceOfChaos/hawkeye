// Package testutil provides helpers for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/database"
	"hawkeye/migrations"
)

// BaseURL returns TEST_DATABASE_URL or skips the test when it is unset.
func BaseURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	return u
}

// NewDatabase creates an isolated, fully migrated database (so packages can test in parallel)
// and drops it when the test ends. It returns the connection URL.
func NewDatabase(t testing.TB) string {
	t.Helper()
	base := BaseURL(t)
	ctx := context.Background()

	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "hawkeye_t_" + hex.EncodeToString(b[:])

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctx, base)
		if err != nil {
			return
		}
		defer c.Close(ctx)
		_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + name
	if err := database.MigrateUp(u.String(), migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return u.String()
}

// NewPool is NewDatabase plus an open pool.
func NewPool(t testing.TB) *pgxpool.Pool { return NewPoolSize(t, 5) }

// NewPoolSize is NewPool with a chosen connection limit (for concurrency tests).
func NewPoolSize(t testing.TB, conns int32) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Connect(context.Background(), NewDatabase(t), conns, 10*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

package database_test

import (
	"context"
	"testing"
	"time"

	"hawkeye/internal/database"
	"hawkeye/internal/testutil"
	"hawkeye/migrations"
)

func TestMigrateUpDownAndConnect(t *testing.T) {
	url := testutil.NewDatabase(t)
	if err := database.MigrateDown(url, migrations.FS); err != nil {
		t.Fatalf("down from latest: %v", err)
	}

	if err := database.MigrateUp(url, migrations.FS); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := database.MigrateUp(url, migrations.FS); err != nil {
		t.Fatalf("second up should be a no-op: %v", err)
	}
	v, dirty, err := database.Version(url, migrations.FS)
	if err != nil || dirty || v == 0 {
		t.Fatalf("version = %d dirty=%v err=%v", v, dirty, err)
	}

	pool, err := database.Connect(context.Background(), url, 2, 5*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'set_updated_at')`).Scan(&exists); err != nil || !exists {
		t.Fatalf("set_updated_at missing: exists=%v err=%v", exists, err)
	}

	if err := database.MigrateDown(url, migrations.FS); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := database.MigrateUp(url, migrations.FS); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}

func TestConnectFailsFast(t *testing.T) {
	_, err := database.Connect(context.Background(), "postgres://u:p@127.0.0.1:1/none?sslmode=disable", 1, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
}

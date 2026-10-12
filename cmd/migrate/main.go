// Command migrate applies database migrations: `migrate up|down|version`.
package main

import (
	"fmt"
	"os"

	"hawkeye/internal/database"
	"hawkeye/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: migrate up|down|version")
	}
	switch args[0] {
	case "up":
		return database.MigrateUp(url, migrations.FS)
	case "down":
		return database.MigrateDown(url, migrations.FS) // reverts one step
	case "version":
		v, dirty, err := database.Version(url, migrations.FS)
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want up|down|version)", args[0])
	}
}

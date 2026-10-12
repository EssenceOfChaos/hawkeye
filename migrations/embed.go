// Package migrations embeds the SQL migration files so binaries can apply them without extra assets.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

// Package migrations embeds the SQL migration files so both the server and test
// tooling can apply them without touching the filesystem.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

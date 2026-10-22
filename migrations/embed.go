// Package migrations embeds the SQL schema migrations applied at startup.
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS

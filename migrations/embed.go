// Package migrations embeds the SQL migration files, so `server migrate`
// and the test harness apply exactly the same files.
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS

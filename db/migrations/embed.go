// Package migrations exposes the SQL migration set embedded in the application.
package migrations

import "embed"

// Files contains every versioned SQL migration.
//
//go:embed *.sql
var Files embed.FS

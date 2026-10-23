// Package db embeds the versioned SQL migrations so the binaries never depend on files at runtime.
package db

import "embed"

// Migrations holds the goose migration files under migrations/.
//
//go:embed migrations/*.sql
var Migrations embed.FS

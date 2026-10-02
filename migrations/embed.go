// Package migrations exposes the application schema to the desktop installer.
package migrations

import "embed"

// Files contains only forward migrations; setup never rolls back user data.
//
//go:embed *.up.sql
var Files embed.FS

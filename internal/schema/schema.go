// Package schema embeds and applies the SQL migrations under
// internal/schema/migrations at process startup, so a freshly copied
// checkout needs nothing beyond a reachable Postgres server - no separate
// migration tool or network fetch of one.
//
// Files are applied in filename order and are all written with
// IF NOT EXISTS, so re-running them against an already-migrated database
// (e.g. every time the orchestrator restarts) is a safe no-op. This is
// intentionally simple: for a system this size, a version-tracking
// migration table would add complexity without adding safety.
package schema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Apply runs every embedded migration, in filename order, against db.
func Apply(ctx context.Context, db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("schema: reading embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		sqlBytes, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("schema: reading %s: %w", name, err)
		}
		for _, stmt := range splitStatements(string(sqlBytes)) {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("schema: applying %s: %w", name, err)
			}
		}
	}
	return nil
}

// splitStatements breaks a migration file into individual SQL statements.
//
// This project's pgwire driver (internal/pgwire) only speaks Postgres's
// extended query protocol, which - unlike the simple query protocol -
// accepts exactly one statement per request. Migration files are plain,
// controlled DDL with no string literals containing a semicolon, so a
// straightforward split on statement-terminating ";" is safe here (this is
// not a general-purpose SQL parser and isn't used against user input).
func splitStatements(sqlText string) []string {
	// Strip comments across the whole file *before* splitting on ";" - a
	// semicolon can legitimately appear inside a "-- comment" (as in this
	// file's own prose comments), and splitting first would cut a
	// statement in half right after such a comment.
	cleaned := stripSQLComments(sqlText)
	var out []string
	for _, raw := range strings.Split(cleaned, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func stripSQLComments(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

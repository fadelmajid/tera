// Package store is the only package that talks to SQLite.
//
// No ORM. Queries are hand-written SQL in queries/, compiled to type-safe Go by
// sqlc (see sqlc.yaml); migrations are goose, forward-only, checked in. The
// FIFO and margin queries are the interesting part of this system and are meant
// to stay legible (ARCHITECTURE §7).
//
// Rupiah columns are INTEGER and map to int64 (INV-1). Nothing here is a float.
//
// Layout:
//
//	migrations/  goose migrations, forward-only, never edited after merge
//	queries/     sqlc source SQL
//	seed/        reference data — the effective-dated tax_rule rows and their legal_ref
//	gen/         sqlc output; generated, checked in, never hand-edited
//
// SQLite runs in WAL mode. Backup is a file copy, which is most of why it was
// chosen (ARCHITECTURE §7). Keep SQLite-only SQL out where the cost is trivial,
// so a future hosted tier stays a config change rather than a rewrite.
package store

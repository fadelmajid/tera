// Package seed holds reference data loaded at first run.
//
// Chiefly the effective-dated tax_rule rows with their legal_ref citations
// (TASKS 5.2). Seeding inserts rows; it never hardcodes a rate into Go (INV-4).
//
// A rate is never updated in place. To change one, insert a new row and close
// the previous row's valid_to (SPEC §2.1). Historical transactions carry their
// own tax snapshot and must not move when config changes (INV-3).
//
// The legal_ref column exists so the owner's konsultan pajak can check the
// seeded values line by line. Have them do that before this touches real books.
package seed

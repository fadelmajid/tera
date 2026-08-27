package store

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Dump describes one exported table or view.
type Dump struct {
	Name string
	// View is true for a derived object — a balance computed on read rather
	// than a row anybody wrote.
	View    bool
	Columns []string
	Rows    int64
}

// Objects lists every table and view in the database, alphabetically.
//
// Read from sqlite_master rather than from a list maintained by hand, and that
// is the whole point of this function. R14.3 makes the export the user's exit
// route if they ever leave this software; an exit route assembled from a
// hardcoded list is one that silently stops being complete the first time
// somebody adds a table and forgets. Here a new table is exported the moment it
// exists, and the test in export_test.go fails if this ever stops being true.
//
// sqlite_% internals are skipped: they are the file format's own bookkeeping,
// not the business's data. goose_db_version is kept deliberately — it says
// which schema version produced the export, which is the first thing anybody
// reading it later needs to know.
func (db *DB) Objects(ctx context.Context) ([]Dump, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name, type
		FROM sqlite_master
		WHERE type IN ('table', 'view')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name`)
	if err != nil {
		return nil, fmt.Errorf("store: list objects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Dump
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, fmt.Errorf("store: scan object: %w", err)
		}
		out = append(out, Dump{Name: name, View: kind == "view"})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list objects: %w", err)
	}
	return out, nil
}

// ExportCSV writes one table or view to w as CSV, header row first.
//
// # Encoding, stated here because the README in the export repeats it
//
// Rupiah are written as whole integers, exactly as stored (INV-1). Nothing in
// this schema is a floating-point column and nothing here formats one; a value
// that arrives as a float is written at full precision rather than rounded, so
// the fact would be visible in the output rather than lost in it.
//
// NULL is written as an empty field. The schema never stores an empty string
// where NULL would mean something different — the service layer converts blank
// input to NULL on the way in — so the two are not distinguishable and do not
// need to be. TestExportDistinguishesNullFromEmpty holds that.
//
// Blobs are hex-encoded. There are none today; ids are TEXT (D-003).
func (db *DB) ExportCSV(ctx context.Context, obj Dump, w io.Writer) (Dump, error) {
	cols, err := db.columns(ctx, obj.Name)
	if err != nil {
		return Dump{}, err
	}
	obj.Columns = cols

	// Names come from sqlite_master, not from a request, and are quoted anyway:
	// an identifier that needed escaping and did not get it is a corrupted
	// export rather than an error, which is the worse failure.
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}
	query := "SELECT " + strings.Join(quoted, ", ") + " FROM " + quoteIdent(obj.Name)
	if !obj.View {
		// Insertion order. A view has no rowid, so it keeps whatever order its
		// own definition gives it.
		query += " ORDER BY rowid"
	}

	rows, err := db.QueryContext(ctx, query) //nolint:gosec // identifiers come from sqlite_master and are quoted
	if err != nil {
		return Dump{}, fmt.Errorf("store: export %s: %w", obj.Name, err)
	}
	defer func() { _ = rows.Close() }()

	out := csv.NewWriter(w)
	if err := out.Write(cols); err != nil {
		return Dump{}, fmt.Errorf("store: export %s header: %w", obj.Name, err)
	}

	values := make([]any, len(cols))
	scan := make([]any, len(cols))
	for i := range values {
		scan[i] = &values[i]
	}
	record := make([]string, len(cols))

	for rows.Next() {
		if err := rows.Scan(scan...); err != nil {
			return Dump{}, fmt.Errorf("store: scan %s: %w", obj.Name, err)
		}
		for i, v := range values {
			record[i] = field(v)
		}
		if err := out.Write(record); err != nil {
			return Dump{}, fmt.Errorf("store: export %s row: %w", obj.Name, err)
		}
		obj.Rows++
	}
	if err := rows.Err(); err != nil {
		return Dump{}, fmt.Errorf("store: export %s: %w", obj.Name, err)
	}

	out.Flush()
	if err := out.Error(); err != nil {
		return Dump{}, fmt.Errorf("store: flush %s: %w", obj.Name, err)
	}
	return obj, nil
}

func (db *DB) columns(ctx context.Context, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", name)
	if err != nil {
		return nil, fmt.Errorf("store: columns of %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, fmt.Errorf("store: scan column of %s: %w", name, err)
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: columns of %s: %w", name, err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("store: %s has no columns", name)
	}
	return cols, nil
}

// field renders one value for CSV.
func field(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(t, 10)
	case string:
		return t
	case []byte:
		return hex.EncodeToString(t)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		// Nothing in this schema is REAL and TestNoColumnIsAFloat holds that
		// (INV-1). Written at full precision rather than rounded so that if one
		// ever appears, it is visible in the export rather than quietly
		// truncated in it.
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// quoteIdent wraps a SQLite identifier in double quotes, doubling any inside.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Describe fills in one object's columns and row count without writing it.
//
// For the screen that shows what an export would contain before anybody
// downloads it: a list of tables and row counts is the only way a person can
// tell a complete export from a plausible one.
func (db *DB) Describe(ctx context.Context, obj Dump) (Dump, error) {
	cols, err := db.columns(ctx, obj.Name)
	if err != nil {
		return Dump{}, err
	}
	rows, err := db.CountRows(ctx, obj.Name)
	if err != nil {
		return Dump{}, err
	}
	obj.Columns, obj.Rows = cols, rows
	return obj, nil
}

// CountRows is the row count of one table or view, for the export manifest to
// be checkable against the database it came from.
func (db *DB) CountRows(ctx context.Context, name string) (int64, error) {
	var n int64
	//nolint:gosec // identifier comes from sqlite_master and is quoted
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdent(name)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count %s: %w", name, err)
	}
	return n, nil
}

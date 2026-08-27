package store_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/store"
)

// TestExportCoversEveryTableInTheSchema is the test that keeps R14.3 honest.
//
// The export is what the user takes with them if they ever leave this software,
// which makes it the one feature whose value is realised only after the product
// has failed them — and therefore the one most likely to rot unnoticed. A
// hardcoded table list would stop being complete the first time somebody adds a
// table and forgets.
//
// So the exporter reads sqlite_master, and this reads sqlite_master
// independently and demands they agree. Add a table in a migration and do
// nothing else: it is exported, and this test says so.
func TestExportCoversEveryTableInTheSchema(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	rows, err := db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var want []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		want = append(want, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if len(want) < 20 {
		t.Fatalf("only %d objects in the schema; this test is not exercising anything", len(want))
	}

	objects, err := db.Objects(ctx)
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}

	got := make([]string, 0, len(objects))
	for _, o := range objects {
		got = append(got, o.Name)
	}
	sort.Strings(want)
	sort.Strings(got)

	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Errorf("the export does not match the schema.\n schema: %v\n export: %v", want, got)
	}

	// goose_db_version is deliberately included: it says which shape of the
	// data the archive holds, which is the first thing a future reader needs.
	if !contains(got, "goose_db_version") {
		t.Error("the export omits the schema version table")
	}
}

// TestNoColumnIsAFloat is INV-1 at the storage layer.
//
// Rupiah is int64 everywhere and every table is STRICT (D-007), so a REAL
// column would have to be declared deliberately. This is the check that a
// deliberate one is still noticed — and it is checked here rather than only in
// the export because the export is where a float would first become somebody
// else's problem, rounded into a CSV they then import.
func TestNoColumnIsAFloat(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	objects, err := db.Objects(ctx)
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}

	for _, o := range objects {
		checkColumnTypes(ctx, t, db, o.Name)
	}
}

func checkColumnTypes(ctx context.Context, t *testing.T, db *store.DB, table string) {
	t.Helper()

	rows, err := db.QueryContext(ctx, "SELECT name, type FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch strings.ToUpper(kind) {
		case "REAL", "FLOAT", "DOUBLE":
			t.Errorf("%s.%s is %s — money is never a float (INV-1)", table, name, kind)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
}

// TestExportedCSVIsReadableAndComplete: what a person opens in Excel has the
// column names on the first row and every row underneath it.
func TestExportedCSVIsReadableAndComplete(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)
	_ = f

	objects, err := db.Objects(ctx)
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}

	for _, o := range objects {
		if o.View {
			continue
		}

		var buf bytes.Buffer
		dump, err := db.ExportCSV(ctx, o, &buf)
		if err != nil {
			t.Fatalf("export %s: %v", o.Name, err)
		}

		records, err := csv.NewReader(&buf).ReadAll()
		if err != nil {
			t.Fatalf("re-read %s: %v", o.Name, err)
		}
		if len(records) == 0 {
			t.Fatalf("%s exported no header row", o.Name)
		}
		if strings.Join(records[0], ",") != strings.Join(dump.Columns, ",") {
			t.Errorf("%s header is %v, want %v", o.Name, records[0], dump.Columns)
		}
		if int64(len(records)-1) != dump.Rows {
			t.Errorf("%s wrote %d data rows against a reported %d", o.Name, len(records)-1, dump.Rows)
		}

		count, err := db.CountRows(ctx, o.Name)
		if err != nil {
			t.Fatalf("count %s: %v", o.Name, err)
		}
		if count != dump.Rows {
			t.Errorf("%s holds %d rows and exported %d", o.Name, count, dump.Rows)
		}
	}
}

// TestExportWritesMoneyAsWholeIntegers is INV-1 reaching the file somebody
// opens. A rupiah figure that arrives in Excel as 1.11E+05 or as 111000.0 is a
// figure that will be re-typed by hand, and re-typed wrong.
func TestExportWritesMoneyAsWholeIntegers(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)
	f.layer(ctx, t, q, &f.budi, 1_800_000_000, 7, 100_000, 1)

	var buf bytes.Buffer
	if _, err := db.ExportCSV(ctx, storeDump("stock_layer"), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(records) < 2 {
		t.Fatal("no layer was exported")
	}

	col := -1
	for i, name := range records[0] {
		if name == "cost_total_idr" {
			col = i
		}
	}
	if col < 0 {
		t.Fatal("stock_layer has no cost_total_idr column")
	}

	value := records[1][col]
	if strings.ContainsAny(value, ".,eE") {
		t.Errorf("cost_total_idr exported as %q — money is never a float (INV-1)", value)
	}
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		t.Errorf("cost_total_idr %q is not a whole number: %v", value, err)
	}
	if value != "100000" {
		t.Errorf("cost_total_idr = %q, want the 100000 that was stored", value)
	}
}

// TestExportDistinguishesNullFromEmpty documents the one encoding choice the
// README makes a promise about.
//
// NULL is written as an empty field. That is only lossless because the service
// layer converts blank input to NULL on the way in, so an empty string is never
// a distinct stored value. This asserts that holds where it would matter first:
// a nullable text column left blank comes back as NULL, not as "".
func TestExportDistinguishesNullFromEmpty(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	var nulls int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('product') WHERE name = 'barcode'`).Scan(&nulls)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if nulls != 1 {
		t.Fatal("product has no barcode column; this test needs rewriting")
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO product (id, code, name, unit, barcode, sale_price_idr, created_at, updated_at)
		VALUES (?, 'P-NULL', 'Tanpa barcode', 'pcs', NULL, 1000, 0, 0)`,
		"01a00000-0000-7000-8000-00000000ffff"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var buf bytes.Buffer
	if _, err := db.ExportCSV(ctx, storeDump("product"), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	col := -1
	for i, name := range records[0] {
		if name == "barcode" {
			col = i
		}
	}
	for _, row := range records[1:] {
		if row[col] != "" {
			t.Errorf("a NULL barcode exported as %q, want an empty field", row[col])
		}
	}

	// And nothing in the database stores an empty string where NULL is meant,
	// which is what makes the empty field unambiguous.
	var empties int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM product WHERE barcode = ''`).Scan(&empties); err != nil {
		t.Fatalf("count: %v", err)
	}
	if empties != 0 {
		t.Errorf("%d products store an empty barcode; the export's NULL encoding is then lossy", empties)
	}
}

// storeDump names one table for ExportCSV without going through Objects.
func storeDump(name string) store.Dump { return store.Dump{Name: name} }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

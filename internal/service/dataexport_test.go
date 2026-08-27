package service_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
)

// archive builds the export and unpacks it.
func archive(ctx context.Context, t *testing.T, w world) (manifest service.Manifest, files map[string]string) {
	t.Helper()

	exp := service.NewExport(w.db, func() time.Time { return fixedNow })

	var buf bytes.Buffer
	manifest, err := exp.Archive(ctx, &buf)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	files = make(map[string]string, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		files[f.Name] = string(body)
	}
	return manifest, files
}

// tradedWorld is a company with a purchase, a sale and a return in it, so the
// export has something of every shape to carry.
func tradedWorld(t *testing.T) (world, context.Context) {
	t.Helper()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 11_000, true)
	w.till(ctx, t)
	price := money.IDR(111_000)
	w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 2, UnitPriceIDR: &price})
	return w, ctx
}

// TestTheArchiveContainsEveryTable is R14.3's promise, checked end to end.
//
// The user's exit route has to be complete or it is worse than nothing: they
// find out it was not on the day they needed it, by which time they have
// already left.
func TestTheArchiveContainsEveryTable(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	manifest, files := archive(ctx, t, w)

	objects, err := w.db.Objects(ctx)
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}
	if len(objects) != len(manifest.Objects) {
		t.Errorf("the database holds %d objects and the manifest lists %d",
			len(objects), len(manifest.Objects))
	}

	for _, o := range manifest.Objects {
		if _, ok := files[o.File]; !ok {
			t.Errorf("%s is in the manifest but not in the archive", o.File)
		}
	}
	for _, want := range []string{"README.md", "manifest.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("the archive has no %s", want)
		}
	}
}

// TestTheManifestMatchesTheFilesBesideIt. The manifest is how anybody checks a
// complete export from a plausible one, so it has to be checkable itself.
func TestTheManifestMatchesTheFilesBesideIt(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	manifest, files := archive(ctx, t, w)

	var written service.Manifest
	if err := json.Unmarshal([]byte(files["manifest.json"]), &written); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if written.SchemaVersion != manifest.SchemaVersion || written.TotalRows != manifest.TotalRows {
		t.Errorf("manifest.json says version %d / %d rows, the call returned %d / %d",
			written.SchemaVersion, written.TotalRows, manifest.SchemaVersion, manifest.TotalRows)
	}
	if written.SchemaVersion == 0 {
		t.Error("the archive does not say which schema version produced it")
	}

	var total int64
	for _, o := range written.Objects {
		records, err := csv.NewReader(strings.NewReader(files[o.File])).ReadAll()
		if err != nil {
			t.Fatalf("read %s: %v", o.File, err)
		}
		if int64(len(records)-1) != o.Rows {
			t.Errorf("%s holds %d rows, the manifest says %d", o.File, len(records)-1, o.Rows)
		}
		if strings.Join(records[0], ",") != strings.Join(o.Columns, ",") {
			t.Errorf("%s columns disagree with the manifest", o.File)
		}
		total += o.Rows
	}
	if total != written.TotalRows {
		t.Errorf("the files hold %d rows against a stated total of %d", total, written.TotalRows)
	}
}

// TestTheArchiveCarriesTheTradeThatHappened. A complete file list proves
// nothing if the files are empty.
func TestTheArchiveCarriesTheTradeThatHappened(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	_, files := archive(ctx, t, w)

	for _, want := range []struct {
		file string
		rows int
	}{
		{"tables/sale.csv", 1},
		{"tables/sale_line.csv", 1},
		{"tables/purchase.csv", 1},
		{"tables/stock_layer.csv", 1},
		{"tables/stock_consumption.csv", 1},
		{"tables/tax_rule.csv", 1},
		{"tables/sale_tax.csv", 1},
	} {
		records, err := csv.NewReader(strings.NewReader(files[want.file])).ReadAll()
		if err != nil {
			t.Fatalf("read %s: %v", want.file, err)
		}
		if len(records)-1 < want.rows {
			t.Errorf("%s carries %d rows, want at least %d", want.file, len(records)-1, want.rows)
		}
	}

	// The derived balances travel too, so somebody with Excel does not have to
	// reimplement the FIFO arithmetic to see what is on the shelf.
	if _, ok := files["views/stock_layer_balance.csv"]; !ok {
		t.Error("the archive has no derived stock balance")
	}
}

// TestTheReadmeStatesEveryEncodingItPromises is what makes it readable without
// Tera.
//
// The archive is meant to be readable without Tera. That promise is only kept
// if the document beside the data says how the data is written — most of all
// that rupiah are whole integers, because a figure re-typed by hand out of a
// misread CSV is a figure typed wrong.
func TestTheReadmeStatesEveryEncodingItPromises(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	_, files := archive(ctx, t, w)

	readme := files["README.md"]
	for _, want := range []string{
		"bilangan bulat", // money is a whole integer
		"YYYY-MM-DD",     // dates
		"Unix",           // instants
		"NULL",           // empty means no value
		"UTF-8",
		"SQLite", // and where the byte-exact copy is
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not explain %q:\n%s", want, readme)
		}
	}

	// It also lists what is inside, so the reader can tell at a glance whether
	// they have everything.
	if !strings.Contains(readme, "tables/sale.csv") {
		t.Error("the README does not list the files in the archive")
	}
}

// TestAnExportThatWouldBeShortIsRefused is the guard against a quiet failure.
//
// The rows are counted while they are written and counted again from the
// database before the archive is handed over. An export that is quietly short
// is the failure mode that matters here, because nobody checks an archive on
// the day they download it.
func TestAnExportThatWouldBeShortIsRefused(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	exp := service.NewExport(w.db, func() time.Time { return fixedNow })

	// A cancelled context is the realistic way a scan returns short.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	var buf bytes.Buffer
	if _, err := exp.Archive(cancelled, &buf); err == nil {
		t.Fatal("an export against a cancelled context reported success")
	}
}

// TestTheManifestCanBeReadWithoutBuildingTheArchive: the screen shows what an
// export would contain before anybody downloads a file.
func TestTheManifestCanBeReadWithoutBuildingTheArchive(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	exp := service.NewExport(w.db, func() time.Time { return fixedNow })

	preview, err := exp.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	built, _ := archive(ctx, t, w)

	if len(preview.Objects) != len(built.Objects) || preview.TotalRows != built.TotalRows {
		t.Errorf("the preview says %d objects / %d rows, the archive holds %d / %d",
			len(preview.Objects), preview.TotalRows, len(built.Objects), built.TotalRows)
	}
	if preview.Filename() != built.Filename() {
		t.Errorf("filenames differ: %q and %q", preview.Filename(), built.Filename())
	}
}

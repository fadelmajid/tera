package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/store"
)

// ErrExportIncomplete is returned when the archive would not contain everything
// the database holds.
//
// Not a warning. An export that is silently missing a table is worse than no
// export at all, because the person relying on it does not find out until they
// need it (R14.3).
var ErrExportIncomplete = errors.New("service: ekspor tidak lengkap")

// Export bundles the whole database as an open-format archive.
// TASKS 6.6, R5.7, R14.3.
//
// # This is the exit route, and it is meant to work
//
// R14.3 says it plainly: the export is what the user takes with them if they
// ever leave this software. That makes it the one feature whose value is
// realised only when the product has failed the customer, which is exactly why
// it must not be the one that quietly rots. Two things follow.
//
// The table list is read from the database itself, never maintained by hand
// (see [store.DB.Objects]), so a table added next year is exported the moment it
// exists. And [Export.Archive] verifies that what it wrote matches what the
// database holds before returning, rather than trusting that it did.
//
// # Format
//
// CSV in a zip: one file per table, one per view, a manifest, and a README that
// says how to read them. CSV because the person most likely to open this has
// Excel and a question, not a database. The manifest carries row counts so the
// archive can be checked against the database it came from, and the schema
// version so a future reader knows which shape of the data they are holding.
//
// The byte-exact copy is the SQLite file itself, and the README says so. This
// archive is the interpretable form, not a replacement for that.
type Export struct {
	db  *store.DB
	now func() time.Time
}

// NewExport builds the service.
func NewExport(db *store.DB, now func() time.Time) *Export {
	if now == nil {
		now = time.Now
	}
	return &Export{db: db, now: now}
}

// ManifestObject is one table or view as recorded in the manifest.
type ManifestObject struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file"`
	// Rows is what was written, and it is compared against a COUNT(*) taken
	// from the database before the archive is handed over.
	Rows    int64    `json:"rows"`
	Columns []string `json:"columns"`
}

// Manifest describes the archive, and is written into it as manifest.json.
type Manifest struct {
	Application string `json:"application"`
	// GeneratedAt is RFC 3339 in UTC. The business dates inside the CSVs are
	// entity-local (D-005); this one is a fact about the file, not about trade.
	GeneratedAt string `json:"generated_at"`
	// SchemaVersion is the goose migration the database was on.
	SchemaVersion int64            `json:"schema_version"`
	Objects       []ManifestObject `json:"objects"`
	TotalRows     int64            `json:"total_rows"`
}

// Filename is what the archive should be saved as.
func (m Manifest) Filename() string {
	day := m.GeneratedAt
	if len(day) >= 10 {
		day = day[:10]
	}
	return "tera-ekspor-" + day + ".zip"
}

// Manifest reports what an export would contain, without building one.
//
// The same enumeration the archive uses, so the two cannot disagree about what
// "everything" means.
func (e *Export) Manifest(ctx context.Context) (Manifest, error) {
	objects, err := e.db.Objects(ctx)
	if err != nil {
		return Manifest{}, err
	}
	version, err := e.db.Version(ctx)
	if err != nil {
		return Manifest{}, err
	}

	out := Manifest{
		Application:   "Tera",
		GeneratedAt:   e.now().UTC().Format(time.RFC3339),
		SchemaVersion: version,
		Objects:       make([]ManifestObject, 0, len(objects)),
	}
	for _, obj := range objects {
		described, err := e.db.Describe(ctx, obj)
		if err != nil {
			return Manifest{}, err
		}
		out.Objects = append(out.Objects, toManifestObject(described))
		out.TotalRows += described.Rows
	}
	return out, nil
}

func toManifestObject(d store.Dump) ManifestObject {
	dir, kind := "tables", "table"
	if d.View {
		dir, kind = "views", "view"
	}
	return ManifestObject{
		Name: d.Name, Kind: kind, File: path.Join(dir, d.Name+".csv"),
		Rows: d.Rows, Columns: d.Columns,
	}
}

// Archive writes the whole database to w as a zip and returns what it wrote.
//
// Buffered rather than streamed, deliberately. A stream that fails halfway has
// already sent its headers and leaves the caller holding a truncated file; at
// a few megabytes for a year of trading, building it in memory costs nothing
// and means a failed export is an error message instead of a corrupt download.
func (e *Export) Archive(ctx context.Context, w io.Writer) (Manifest, error) {
	objects, err := e.db.Objects(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if len(objects) == 0 {
		return Manifest{}, fmt.Errorf("%w: tidak ada tabel untuk diekspor", ErrExportIncomplete)
	}

	version, err := e.db.Version(ctx)
	if err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		Application:   "Tera",
		GeneratedAt:   e.now().UTC().Format(time.RFC3339),
		SchemaVersion: version,
		Objects:       make([]ManifestObject, 0, len(objects)),
	}

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)

	for _, obj := range objects {
		entry := toManifestObject(obj)

		file, err := archive.Create(entry.File)
		if err != nil {
			return Manifest{}, fmt.Errorf("service: create %s: %w", entry.File, err)
		}
		written, err := e.db.ExportCSV(ctx, obj, file)
		if err != nil {
			return Manifest{}, err
		}

		entry.Rows, entry.Columns = written.Rows, written.Columns
		manifest.Objects = append(manifest.Objects, entry)
		manifest.TotalRows += written.Rows
	}

	if err := e.verify(ctx, manifest); err != nil {
		return Manifest{}, err
	}

	if err := writeFile(archive, "manifest.json", manifestJSON(manifest)); err != nil {
		return Manifest{}, err
	}
	if err := writeFile(archive, "README.md", readme(manifest)); err != nil {
		return Manifest{}, err
	}

	if err := archive.Close(); err != nil {
		return Manifest{}, fmt.Errorf("service: close archive: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return Manifest{}, fmt.Errorf("service: write archive: %w", err)
	}
	return manifest, nil
}

// verify re-counts every table against the database before the archive is
// handed over.
//
// The rows were counted while they were being written, so this is a second
// opinion rather than the same number twice: a query that quietly returned
// short — a context cancelled mid-scan, a driver error swallowed somewhere —
// produces a mismatch here rather than an archive that looks fine and is not.
//
// Views are skipped: they are derived on read, and counting one twice would
// only prove the view is deterministic, which is not what is at risk.
func (e *Export) verify(ctx context.Context, m Manifest) error {
	for _, obj := range m.Objects {
		if obj.Kind != "table" {
			continue
		}
		actual, err := e.db.CountRows(ctx, obj.Name)
		if err != nil {
			return err
		}
		if actual != obj.Rows {
			return fmt.Errorf("%w: tabel %s berisi %d baris tetapi %d yang tertulis",
				ErrExportIncomplete, obj.Name, actual, obj.Rows)
		}
	}
	return nil
}

func writeFile(archive *zip.Writer, name, body string) error {
	f, err := archive.Create(name)
	if err != nil {
		return fmt.Errorf("service: create %s: %w", name, err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		return fmt.Errorf("service: write %s: %w", name, err)
	}
	return nil
}

func manifestJSON(m Manifest) string {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		// Every field is a string, an int64 or a slice of them. Unreachable.
		return `{"error":"manifest could not be encoded"}`
	}
	return string(out) + "\n"
}

// readme is the document the person opening this archive reads first.
//
// In Bahasa Indonesia like the rest of the interface: the most likely reader is
// the owner, with Excel and a question. It states the encodings rather than
// leaving them to be inferred, because the whole promise of R14.3 is that this
// is readable without the software that produced it.
func readme(m Manifest) string {
	var b strings.Builder

	b.WriteString("# Ekspor data Tera\n\n")
	b.WriteString("Seluruh isi basis data, apa adanya. Dibuat " + m.GeneratedAt + " (UTC), ")
	fmt.Fprintf(&b, "versi skema %d, total %d baris.\n\n", m.SchemaVersion, m.TotalRows)

	b.WriteString("## Cara membaca\n\n")
	b.WriteString("Setiap berkas `.csv` bisa dibuka langsung di Excel, Google Sheets, atau LibreOffice.\n")
	b.WriteString("Baris pertama adalah nama kolom.\n\n")
	b.WriteString("- `tables/` — data asli, satu berkas per tabel.\n")
	b.WriteString("- `views/` — hitungan turunan (mis. sisa hutang setelah pembayaran).\n")
	b.WriteString("  Bukan data baru: semuanya bisa dihitung ulang dari `tables/`.\n")
	b.WriteString("- `manifest.json` — daftar berkas beserta jumlah barisnya, untuk mencocokkan\n")
	b.WriteString("  bahwa ekspor ini utuh.\n\n")

	b.WriteString("## Aturan penulisan nilai\n\n")
	b.WriteString("- **Rupiah ditulis sebagai bilangan bulat**, tanpa titik pemisah dan tanpa koma.\n")
	b.WriteString("  `111000` berarti Rp 111.000. Tidak ada nilai uang berupa pecahan desimal\n")
	b.WriteString("  di sistem ini, dan itu disengaja.\n")
	b.WriteString("- **Tanggal** ditulis `YYYY-MM-DD` menurut zona waktu perusahaan.\n")
	b.WriteString("- **Waktu** (`*_at`) ditulis sebagai detik Unix UTC.\n")
	b.WriteString("- **Kosong berarti tidak ada nilai** (NULL). Sistem ini tidak pernah menyimpan\n")
	b.WriteString("  teks kosong sebagai nilai yang berbeda dari tidak ada nilai.\n")
	b.WriteString("- **0 dan 1** pada kolom seperti `is_pkp` atau `faktur_received` berarti\n")
	b.WriteString("  tidak dan ya.\n")
	b.WriteString("- Berkas menggunakan UTF-8.\n\n")

	b.WriteString("## Salinan persis\n\n")
	b.WriteString("Ekspor ini dibuat agar bisa dibaca tanpa Tera. Jika yang dibutuhkan adalah\n")
	b.WriteString("salinan persis apa adanya, berkas basis data SQLite (`tera.db`) adalah\n")
	b.WriteString("berkas tunggal yang bisa disalin begitu saja dan dibuka dengan perkakas\n")
	b.WriteString("SQLite mana pun. Data ini milik Anda; tidak ada bagian yang terkunci di\n")
	b.WriteString("dalam aplikasi.\n\n")

	b.WriteString("## Isi\n\n")
	b.WriteString("| Berkas | Jenis | Baris |\n|---|---|---|\n")

	rows := make([]ManifestObject, len(m.Objects))
	copy(rows, m.Objects)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].File < rows[j].File })
	for _, o := range rows {
		kind := "tabel"
		if o.Kind == "view" {
			kind = "turunan"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d |\n", o.File, kind, o.Rows)
	}
	return b.String()
}

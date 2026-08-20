package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

func migrated(t *testing.T) (*store.DB, *gen.Queries, context.Context) {
	t.Helper()

	ctx := context.Background()
	db := openTemp(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, gen.New(db), ctx
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	first, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if first != 1 {
		t.Errorf("schema version %d after migrating, want 1", first)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	second, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if first != second {
		t.Errorf("schema version moved on a re-run: %d then %d", first, second)
	}
}

// INV-1 at the storage layer. Without STRICT, SQLite stores 1000.5 in a column
// declared INTEGER and says nothing (D-007).
//
// STRICT's exact rule, confirmed against SQLite 3.51: a REAL or TEXT value is
// accepted only when it converts to an integer losslessly. So 1000.0 and '1000'
// are stored — as the integer 1000, nothing lost — while 1000.5, '1000.5',
// 'abc' and a blob are refused outright.
//
// Stated precisely, that is the invariant: no fractional rupiah can exist in
// this database whatever a caller sends, and whatever is stored is an integer.
func TestStrictTablesRejectFractionalMoney(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	insert := func(code, literal string) error {
		_, err := db.ExecContext(ctx, `
            INSERT INTO product (id, code, name, unit, sale_price_idr, created_at, updated_at)
            VALUES (?, ?, 'Kasa Steril', 'pcs', `+literal+`, 0, 0)`,
			store.NewID(), code)
		return err
	}

	t.Run("refused", func(t *testing.T) {
		refused := []struct{ name, value string }{
			{"fractional rupiah", "1000.5"},
			{"fraction of a rupiah", "0.333"},
			{"fractional as text", "'1000.5'"},
			{"not a number", "'abc'"},
			{"blob", "x'01'"},
		}
		for i, tc := range refused {
			t.Run(tc.name, func(t *testing.T) {
				if err := insert(fmt.Sprintf("P-REJ-%d", i), tc.value); err == nil {
					t.Fatalf("stored %s in an INTEGER rupiah column; INV-1 is not enforced", tc.value)
				}
			})
		}
	})

	t.Run("coerced losslessly", func(t *testing.T) {
		accepted := []struct{ name, value string }{
			{"integer", "1000"},
			{"whole-valued real", "1000.0"},
			{"integer as text", "'1000'"},
		}
		for i, tc := range accepted {
			t.Run(tc.name, func(t *testing.T) {
				code := fmt.Sprintf("P-OK-%d", i)
				if err := insert(code, tc.value); err != nil {
					t.Fatalf("insert %s: %v", tc.value, err)
				}

				var typ string
				var val int64
				if err := db.QueryRowContext(ctx,
					`SELECT typeof(sale_price_idr), sale_price_idr FROM product WHERE code = ?`, code,
				).Scan(&typ, &val); err != nil {
					t.Fatalf("read back: %v", err)
				}
				if typ != "integer" {
					t.Errorf("stored type is %q, want integer — a non-integer reached the column", typ)
				}
				if val != 1000 {
					t.Errorf("stored value is %d, want 1000", val)
				}
			})
		}
	})
}

func TestIDConstraint(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"canonical uuidv7", store.NewID(), false},
		{"uppercase", strings.ToUpper(store.NewID()), true},
		{"too short", "abc", true},
		{"empty", "", true},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx,
				`INSERT INTO owner (id, code, name, created_at, updated_at) VALUES (?, ?, 'Budi', 0, 0)`,
				tc.id, "O-"+string(rune('A'+i)))
			if tc.wantErr && err == nil {
				t.Errorf("accepted id %q, want rejection", tc.id)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("rejected a canonical id: %v", err)
			}
		})
	}
}

// A product attributed to an owner who does not exist would put a sale into a
// margin bucket belonging to nobody.
func TestProductOwnerForeignKey(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	_, err := db.ExecContext(ctx, `
        INSERT INTO product (id, code, name, unit, owner_id, sale_price_idr, created_at, updated_at)
        VALUES (?, 'P-1', 'Masker', 'box', ?, 25000, 0, 0)`,
		store.NewID(), store.NewID())
	if err == nil {
		t.Fatal("attributed a product to a nonexistent owner")
	}
}

// owner_id is nullable by requirement, not oversight: NULL is the company
// bucket and reports as its own line (R2.2).
func TestProductOwnerIsNullableForTheCompanyBucket(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)

	p, err := q.CreateProduct(ctx, gen.CreateProductParams{
		ID: store.NewID(), Code: "P-COMPANY", Name: "Alkohol 70%", Unit: "botol",
		SalePriceIdr: 18_000, CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.OwnerID != nil {
		t.Errorf("owner_id = %v, want nil for the company bucket", *p.OwnerID)
	}

	bucket, err := q.ListCompanyBucketProducts(ctx)
	if err != nil {
		t.Fatalf("list company bucket: %v", err)
	}
	if len(bucket) != 1 {
		t.Fatalf("company bucket holds %d products, want 1", len(bucket))
	}
}

// A duplicate barcode means the scanner rings up the wrong item (R9.3). Many
// products legitimately have none, which is why the index is partial.
func TestBarcodeUniqueButOptional(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)

	bc := "8991234567890"
	mk := func(code string, barcode *string) error {
		_, err := q.CreateProduct(ctx, gen.CreateProductParams{
			ID: store.NewID(), Code: code, Barcode: barcode, Name: "Item " + code,
			Unit: "pcs", SalePriceIdr: 1_000, CreatedAt: 0, UpdatedAt: 0,
		})
		return err
	}

	if err := mk("P-1", &bc); err != nil {
		t.Fatalf("first barcode: %v", err)
	}
	if err := mk("P-2", &bc); err == nil {
		t.Error("accepted a duplicate barcode")
	}
	if err := mk("P-3", nil); err != nil {
		t.Fatalf("first product without a barcode: %v", err)
	}
	if err := mk("P-4", nil); err != nil {
		t.Fatalf("second product without a barcode rejected; the index is not partial: %v", err)
	}
}

func TestLegalEntityConstraints(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)

	e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "PKP", Name: "PT Sehat Sentosa", IsPkp: 1,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.IsPkp != 1 || e.Timezone != "Asia/Jakarta" {
		t.Errorf("round trip lost data: %+v", e)
	}

	// The book year need not start in January, but it must be a month.
	_, err = db.ExecContext(ctx, `
        INSERT INTO legal_entity (id, code, name, is_pkp, timezone, book_year_start_month, created_at, updated_at)
        VALUES (?, 'BAD', 'X', 1, 'Asia/Jakarta', 13, 0, 0)`, store.NewID())
	if err == nil {
		t.Error("accepted book_year_start_month = 13")
	}

	// is_pkp is a decision with tax consequences, not a free integer.
	_, err = db.ExecContext(ctx, `
        INSERT INTO legal_entity (id, code, name, is_pkp, timezone, book_year_start_month, created_at, updated_at)
        VALUES (?, 'BAD2', 'X', 2, 'Asia/Jakarta', 1, 0, 0)`, store.NewID())
	if err == nil {
		t.Error("accepted is_pkp = 2")
	}
}

// Whether a supplier normally issues a faktur is an expectation to default
// from, never the truth for a given purchase (INV-9 lives on the layer).
func TestSupplierAndCustomerRoundTrip(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)

	npwp := "01.234.567.8-901.000"
	s, err := q.CreateSupplier(ctx, gen.CreateSupplierParams{
		ID: store.NewID(), Code: "S-1", Name: "PT Medika Jaya", Npwp: &npwp,
		IssuesFaktur: 1, CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}
	if s.IssuesFaktur != 1 || s.Npwp == nil || *s.Npwp != npwp {
		t.Errorf("supplier round trip lost data: %+v", s)
	}

	c, err := q.CreateCustomer(ctx, gen.CreateCustomerParams{
		ID: store.NewID(), Code: "C-1", Name: "Klinik Harapan", CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if c.Npwp != nil || c.Nik != nil {
		t.Errorf("expected a walk-in customer to carry neither npwp nor nik: %+v", c)
	}
}

func TestProductOwnerAttribution(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)

	budi, err := q.CreateOwner(ctx, gen.CreateOwnerParams{
		ID: store.NewID(), Code: "BUDI", Name: "Budi", CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}

	if _, err := q.CreateProduct(ctx, gen.CreateProductParams{
		ID: store.NewID(), Code: "P-BUDI", Name: "Termometer", Unit: "pcs",
		OwnerID: &budi.ID, SalePriceIdr: 85_000, CreatedAt: 0, UpdatedAt: 0,
	}); err != nil {
		t.Fatalf("create product: %v", err)
	}

	got, err := q.ListProductsByOwner(ctx, &budi.ID)
	if err != nil {
		t.Fatalf("list by owner: %v", err)
	}
	if len(got) != 1 || got[0].Code != "P-BUDI" {
		t.Fatalf("got %d products for Budi, want 1", len(got))
	}
	if got[0].SalePriceIdr != 85_000 {
		t.Errorf("sale price = %d, want 85000", got[0].SalePriceIdr)
	}
}

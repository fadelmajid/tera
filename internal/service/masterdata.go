package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

var (
	// ErrValidation is returned when input is rejected before any write.
	ErrValidation = errors.New("service: data tidak valid")
	// ErrNotFound is returned when a record does not exist.
	ErrNotFound = errors.New("service: data tidak ditemukan")
	// ErrDuplicate is returned when a unique code or barcode is already taken.
	ErrDuplicate = errors.New("service: kode sudah digunakan")
)

// Actor is who is making a change and under which company.
//
// Carried explicitly rather than pulled from a context value, so that no
// mutation can be written without naming the person accountable for it — the
// audit trail is only as good as this field (INV-10, R13.5).
type Actor struct {
	UserID          string
	LegalEntityID   string
	ClientRequestID string
}

// MasterData handles the catalogue: entities, owners, products, suppliers,
// customers (R11).
//
// Every mutation here writes an audit row in the same transaction as the change
// (INV-10). That is not a convention these methods each remember — it is the
// shape of the only write path they have.
type MasterData struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewMasterData builds the service.
func NewMasterData(db *store.DB, aud *Auditor, now func() time.Time) *MasterData {
	if now == nil {
		now = time.Now
	}
	return &MasterData{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- legal entities ---------------------------------------------------------

// EntityInput is a company as submitted.
type EntityInput struct {
	Code               string
	Name               string
	IsPKP              bool
	NPWP               string
	Address            string
	Phone              string
	Timezone           string
	BookYearStartMonth int64
}

func (in *EntityInput) normalise() error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if in.Timezone == "" {
		in.Timezone = "Asia/Jakarta"
	}
	if in.BookYearStartMonth == 0 {
		in.BookYearStartMonth = 1
	}

	switch {
	case in.Code == "":
		return fmt.Errorf("%w: kode perusahaan wajib diisi", ErrValidation)
	case in.Name == "":
		return fmt.Errorf("%w: nama perusahaan wajib diisi", ErrValidation)
	case in.BookYearStartMonth < 1 || in.BookYearStartMonth > 12:
		return fmt.Errorf("%w: bulan awal tahun buku harus 1–12", ErrValidation)
	}

	// The timezone must resolve, or every book-year and business-day boundary
	// computed from it is wrong (INV-5). tzdata is embedded in the binary so
	// this works on a machine with no system zoneinfo.
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return fmt.Errorf("%w: zona waktu %q tidak dikenal", ErrValidation, in.Timezone)
	}
	return nil
}

// CountEntities reports how many companies exist, for first-run setup.
func (m *MasterData) CountEntities(ctx context.Context) (int, error) {
	rows, err := m.q.ListLegalEntities(ctx)
	if err != nil {
		return 0, fmt.Errorf("service: list entities: %w", err)
	}
	return len(rows), nil
}

// ListEntities returns every company.
func (m *MasterData) ListEntities(ctx context.Context) ([]gen.LegalEntity, error) {
	rows, err := m.q.ListLegalEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: list entities: %w", err)
	}
	return rows, nil
}

// CreateEntity adds a company and writes the audit row with it.
func (m *MasterData) CreateEntity(ctx context.Context, actor Actor, in EntityInput) (gen.LegalEntity, error) {
	if err := in.normalise(); err != nil {
		return gen.LegalEntity{}, err
	}

	var created gen.LegalEntity
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		now := m.now().Unix()
		row, err := gen.New(tx).CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
			ID: store.NewID(), Code: in.Code, Name: in.Name, IsPkp: boolToInt(in.IsPKP),
			Npwp: nilIfEmpty(in.NPWP), Address: nilIfEmpty(in.Address), Phone: nilIfEmpty(in.Phone),
			Timezone: in.Timezone, BookYearStartMonth: in.BookYearStartMonth,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: row.ID,
			RecordType: "legal_entity", RecordID: row.ID,
			Action: ActionCreate, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// --- owners -----------------------------------------------------------------

// OwnerInput is a family member as submitted.
//
// An owner is an attribution tag, not legal ownership (R2, CLAUDE.md), and is
// not scoped to a company: the same people own product lines in both, and a
// transfer carries attribution across the entity boundary unchanged (SPEC §3.4).
type OwnerInput struct {
	Code     string
	Name     string
	Note     string
	IsActive bool
}

func (in *OwnerInput) normalise() error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Code == "":
		return fmt.Errorf("%w: kode owner wajib diisi", ErrValidation)
	case in.Name == "":
		return fmt.Errorf("%w: nama owner wajib diisi", ErrValidation)
	}
	return nil
}

// ListOwners returns the family members products can be attributed to.
func (m *MasterData) ListOwners(ctx context.Context, includeInactive bool) ([]gen.Owner, error) {
	rows, err := m.q.ListOwners(ctx, boolToInt(includeInactive))
	if err != nil {
		return nil, fmt.Errorf("service: list owners: %w", err)
	}
	return rows, nil
}

// CreateOwner adds a family member.
func (m *MasterData) CreateOwner(ctx context.Context, actor Actor, in OwnerInput) (gen.Owner, error) {
	if err := in.normalise(); err != nil {
		return gen.Owner{}, err
	}

	var created gen.Owner
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		now := m.now().Unix()
		row, err := gen.New(tx).CreateOwner(ctx, gen.CreateOwnerParams{
			ID: store.NewID(), Code: in.Code, Name: in.Name,
			Note: nilIfEmpty(in.Note), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "owner", RecordID: row.ID,
			Action: ActionCreate, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// UpdateOwner changes a family member, recording both sides of the change.
func (m *MasterData) UpdateOwner(ctx context.Context, actor Actor, id string, in OwnerInput) (gen.Owner, error) {
	if err := in.normalise(); err != nil {
		return gen.Owner{}, err
	}

	var updated gen.Owner
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetOwner(ctx, id)
		if err != nil {
			return notFoundOr(err)
		}

		row, err := q.UpdateOwner(ctx, gen.UpdateOwnerParams{
			ID: id, Name: in.Name, Note: nilIfEmpty(in.Note),
			IsActive: boolToInt(in.IsActive), UpdatedAt: m.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		updated = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "owner", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return updated, err
}

// --- products ---------------------------------------------------------------

// ProductInput is a catalogue item as submitted.
type ProductInput struct {
	Code     string
	Barcode  string
	Name     string
	Unit     string
	Category string
	// OwnerID is empty for the company bucket (R2.2) — nullable by
	// requirement, not by oversight.
	OwnerID      string
	SalePriceIDR money.IDR
	IsActive     bool
}

func (in *ProductInput) normalise() error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.Barcode = strings.TrimSpace(in.Barcode)
	in.Category = strings.TrimSpace(in.Category)
	if strings.TrimSpace(in.Unit) == "" {
		in.Unit = "pcs"
	}

	switch {
	case in.Code == "":
		return fmt.Errorf("%w: kode produk wajib diisi", ErrValidation)
	case in.Name == "":
		return fmt.Errorf("%w: nama produk wajib diisi", ErrValidation)
	case in.SalePriceIDR.IsNegative():
		return fmt.Errorf("%w: harga jual tidak boleh negatif", ErrValidation)
	}
	return nil
}

// ListProducts returns the catalogue.
func (m *MasterData) ListProducts(ctx context.Context, includeInactive bool) ([]gen.Product, error) {
	rows, err := m.q.ListProducts(ctx, boolToInt(includeInactive))
	if err != nil {
		return nil, fmt.Errorf("service: list products: %w", err)
	}
	return rows, nil
}

// CreateProduct adds a catalogue item.
func (m *MasterData) CreateProduct(ctx context.Context, actor Actor, in ProductInput) (gen.Product, error) {
	if err := in.normalise(); err != nil {
		return gen.Product{}, err
	}

	var created gen.Product
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)
		if err := requireOwnerExists(ctx, q, in.OwnerID); err != nil {
			return err
		}

		now := m.now().Unix()
		row, err := q.CreateProduct(ctx, gen.CreateProductParams{
			ID: store.NewID(), Code: in.Code, Barcode: nilIfEmpty(in.Barcode),
			Name: in.Name, Unit: in.Unit, Category: nilIfEmpty(in.Category),
			OwnerID: nilIfEmpty(in.OwnerID), SalePriceIdr: int64(in.SalePriceIDR),
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "product", RecordID: row.ID,
			Action: ActionCreate, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// UpdateProduct changes a catalogue item.
//
// Owner attribution is among the fields that can change here, and it decides
// whose margin a future sale lands in (R2.3). The before/after snapshot is what
// makes such a change answerable months later, when a family member asks why
// their figure moved.
func (m *MasterData) UpdateProduct(ctx context.Context, actor Actor, id string, in ProductInput) (gen.Product, error) {
	if err := in.normalise(); err != nil {
		return gen.Product{}, err
	}

	var updated gen.Product
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetProduct(ctx, id)
		if err != nil {
			return notFoundOr(err)
		}
		if err := requireOwnerExists(ctx, q, in.OwnerID); err != nil {
			return err
		}

		row, err := q.UpdateProduct(ctx, gen.UpdateProductParams{
			ID: id, Code: in.Code, Barcode: nilIfEmpty(in.Barcode), Name: in.Name,
			Unit: in.Unit, Category: nilIfEmpty(in.Category), OwnerID: nilIfEmpty(in.OwnerID),
			SalePriceIdr: int64(in.SalePriceIDR), IsActive: boolToInt(in.IsActive),
			UpdatedAt: m.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		updated = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "product", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return updated, err
}

// --- suppliers --------------------------------------------------------------

// SupplierInput is a supplier as submitted.
type SupplierInput struct {
	Code    string
	Name    string
	NPWP    string
	Address string
	Phone   string
	// IssuesFaktur is whether this supplier NORMALLY issues a faktur pajak. It
	// defaults the purchasing screen and supports comparing suppliers on true
	// cost (R10.6). It is never the truth for a given purchase — that lives on
	// the stock layer and sets its cost basis (INV-9).
	IssuesFaktur bool
	IsActive     bool
}

func (in *SupplierInput) normalise() error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Code == "":
		return fmt.Errorf("%w: kode pemasok wajib diisi", ErrValidation)
	case in.Name == "":
		return fmt.Errorf("%w: nama pemasok wajib diisi", ErrValidation)
	}
	return nil
}

// ListSuppliers returns suppliers.
func (m *MasterData) ListSuppliers(ctx context.Context, includeInactive bool) ([]gen.Supplier, error) {
	rows, err := m.q.ListSuppliers(ctx, boolToInt(includeInactive))
	if err != nil {
		return nil, fmt.Errorf("service: list suppliers: %w", err)
	}
	return rows, nil
}

// CreateSupplier adds a supplier.
func (m *MasterData) CreateSupplier(ctx context.Context, actor Actor, in SupplierInput) (gen.Supplier, error) {
	if err := in.normalise(); err != nil {
		return gen.Supplier{}, err
	}

	var created gen.Supplier
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		now := m.now().Unix()
		row, err := gen.New(tx).CreateSupplier(ctx, gen.CreateSupplierParams{
			ID: store.NewID(), Code: in.Code, Name: in.Name, Npwp: nilIfEmpty(in.NPWP),
			Address: nilIfEmpty(in.Address), Phone: nilIfEmpty(in.Phone),
			IssuesFaktur: boolToInt(in.IssuesFaktur), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "supplier", RecordID: row.ID,
			Action: ActionCreate, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// UpdateSupplier changes a supplier.
func (m *MasterData) UpdateSupplier(ctx context.Context, actor Actor, id string, in SupplierInput) (gen.Supplier, error) {
	if err := in.normalise(); err != nil {
		return gen.Supplier{}, err
	}

	var updated gen.Supplier
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetSupplier(ctx, id)
		if err != nil {
			return notFoundOr(err)
		}

		row, err := q.UpdateSupplier(ctx, gen.UpdateSupplierParams{
			ID: id, Name: in.Name, Npwp: nilIfEmpty(in.NPWP), Address: nilIfEmpty(in.Address),
			Phone: nilIfEmpty(in.Phone), IssuesFaktur: boolToInt(in.IssuesFaktur),
			IsActive: boolToInt(in.IsActive), UpdatedAt: m.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		updated = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "supplier", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return updated, err
}

// --- customers --------------------------------------------------------------

// CustomerInput is a customer as submitted. NPWP for businesses, NIK for
// individuals — a buyer who wants a faktur must supply one (R11.3).
type CustomerInput struct {
	Code     string
	Name     string
	NPWP     string
	NIK      string
	Address  string
	Phone    string
	IsActive bool
}

func (in *CustomerInput) normalise() error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Code == "":
		return fmt.Errorf("%w: kode pelanggan wajib diisi", ErrValidation)
	case in.Name == "":
		return fmt.Errorf("%w: nama pelanggan wajib diisi", ErrValidation)
	}
	return nil
}

// ListCustomers returns customers.
func (m *MasterData) ListCustomers(ctx context.Context, includeInactive bool) ([]gen.Customer, error) {
	rows, err := m.q.ListCustomers(ctx, boolToInt(includeInactive))
	if err != nil {
		return nil, fmt.Errorf("service: list customers: %w", err)
	}
	return rows, nil
}

// CreateCustomer adds a customer.
func (m *MasterData) CreateCustomer(ctx context.Context, actor Actor, in CustomerInput) (gen.Customer, error) {
	if err := in.normalise(); err != nil {
		return gen.Customer{}, err
	}

	var created gen.Customer
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		now := m.now().Unix()
		row, err := gen.New(tx).CreateCustomer(ctx, gen.CreateCustomerParams{
			ID: store.NewID(), Code: in.Code, Name: in.Name, Npwp: nilIfEmpty(in.NPWP),
			Nik: nilIfEmpty(in.NIK), Address: nilIfEmpty(in.Address), Phone: nilIfEmpty(in.Phone),
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "customer", RecordID: row.ID,
			Action: ActionCreate, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// UpdateCustomer changes a customer.
func (m *MasterData) UpdateCustomer(ctx context.Context, actor Actor, id string, in CustomerInput) (gen.Customer, error) {
	if err := in.normalise(); err != nil {
		return gen.Customer{}, err
	}

	var updated gen.Customer
	err := m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetCustomer(ctx, id)
		if err != nil {
			return notFoundOr(err)
		}

		row, err := q.UpdateCustomer(ctx, gen.UpdateCustomerParams{
			ID: id, Name: in.Name, Npwp: nilIfEmpty(in.NPWP), Nik: nilIfEmpty(in.NIK),
			Address: nilIfEmpty(in.Address), Phone: nilIfEmpty(in.Phone),
			IsActive: boolToInt(in.IsActive), UpdatedAt: m.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		updated = row

		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "customer", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return updated, err
}

// --- shared -----------------------------------------------------------------

// requireOwnerExists rejects an attribution to an owner who is not there.
//
// The foreign key would catch it too, but as an opaque constraint error. A
// product attributed to nobody puts a future sale's margin in a bucket that
// does not exist (INV-8), so it is worth a clear message.
func requireOwnerExists(ctx context.Context, q *gen.Queries, ownerID string) error {
	if ownerID == "" {
		return nil // the company bucket (R2.2)
	}
	if _, err := q.GetOwner(ctx, ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: owner tidak ditemukan", ErrValidation)
		}
		return fmt.Errorf("service: lookup owner: %w", err)
	}
	return nil
}

func notFoundOr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("service: read: %w", err)
}

// wrapWrite turns a unique-constraint violation into something a user can act
// on. SQLite reports it as a generic constraint error with the index name.
func wrapWrite(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: unique") {
		switch {
		case strings.Contains(msg, "barcode"):
			return fmt.Errorf("%w: barcode sudah dipakai produk lain", ErrDuplicate)
		default:
			return fmt.Errorf("%w", ErrDuplicate)
		}
	}
	return fmt.Errorf("service: write: %w", err)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

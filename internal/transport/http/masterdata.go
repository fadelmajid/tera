package http

import (
	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- wire shapes ------------------------------------------------------------
//
// Deliberately not the sqlc row types. Those carry storage spellings
// (SalePriceIdr) and storage booleans (int64), and money must cross the wire as
// a branded integer the TypeScript side can trust (SPEC §1, INV-1).

type entityDTO struct {
	ID                 string  `json:"id"`
	Code               string  `json:"code"`
	Name               string  `json:"name"`
	IsPKP              bool    `json:"is_pkp"`
	NPWP               *string `json:"npwp"`
	Timezone           string  `json:"timezone"`
	BookYearStartMonth int64   `json:"book_year_start_month"`
	IsActive           bool    `json:"is_active"`
}

func toEntityDTO(e gen.LegalEntity) entityDTO {
	return entityDTO{
		ID: e.ID, Code: e.Code, Name: e.Name, IsPKP: e.IsPkp == 1, NPWP: e.Npwp,
		Timezone: e.Timezone, BookYearStartMonth: e.BookYearStartMonth, IsActive: e.IsActive == 1,
	}
}

type ownerDTO struct {
	ID       string  `json:"id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Note     *string `json:"note"`
	IsActive bool    `json:"is_active"`
}

func toOwnerDTO(o gen.Owner) ownerDTO {
	return ownerDTO{ID: o.ID, Code: o.Code, Name: o.Name, Note: o.Note, IsActive: o.IsActive == 1}
}

type productDTO struct {
	ID       string  `json:"id"`
	Code     string  `json:"code"`
	Barcode  *string `json:"barcode"`
	Name     string  `json:"name"`
	Unit     string  `json:"unit"`
	Category *string `json:"category"`
	// Null is the company bucket, and it is reported as its own line on the
	// margin report (R2.2). Explicitly null rather than omitted, so the
	// distinction survives the wire.
	OwnerID      *string   `json:"owner_id"`
	SalePriceIDR money.IDR `json:"sale_price_idr"`
	IsActive     bool      `json:"is_active"`
}

func toProductDTO(p gen.Product) productDTO {
	return productDTO{
		ID: p.ID, Code: p.Code, Barcode: p.Barcode, Name: p.Name, Unit: p.Unit,
		Category: p.Category, OwnerID: p.OwnerID,
		SalePriceIDR: money.IDR(p.SalePriceIdr), IsActive: p.IsActive == 1,
	}
}

type supplierDTO struct {
	ID           string  `json:"id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	NPWP         *string `json:"npwp"`
	Address      *string `json:"address"`
	Phone        *string `json:"phone"`
	IssuesFaktur bool    `json:"issues_faktur"`
	IsActive     bool    `json:"is_active"`
}

func toSupplierDTO(s gen.Supplier) supplierDTO {
	return supplierDTO{
		ID: s.ID, Code: s.Code, Name: s.Name, NPWP: s.Npwp, Address: s.Address,
		Phone: s.Phone, IssuesFaktur: s.IssuesFaktur == 1, IsActive: s.IsActive == 1,
	}
}

type customerDTO struct {
	ID       string  `json:"id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	NPWP     *string `json:"npwp"`
	NIK      *string `json:"nik"`
	Address  *string `json:"address"`
	Phone    *string `json:"phone"`
	IsActive bool    `json:"is_active"`
}

func toCustomerDTO(c gen.Customer) customerDTO {
	return customerDTO{
		ID: c.ID, Code: c.Code, Name: c.Name, NPWP: c.Npwp, NIK: c.Nik,
		Address: c.Address, Phone: c.Phone, IsActive: c.IsActive == 1,
	}
}

// --- routes -----------------------------------------------------------------

func mountMasterData(r chi.Router, cfg Config) {
	md, auth := cfg.Master, cfg.Auth
	if md == nil {
		return
	}

	// Listing companies needs only a session: the browser has to know which
	// companies exist before it can name one in X-Entity-Id.
	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Get("/entities", handleListEntities(md))
		r.Post("/setup/entity", handleSetupFirstEntity(md, auth))
	})

	// Everything else is scoped to a company and gated on the role held there.
	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanManageMasterData,
			"hanya pemilik dan manajer yang dapat mengubah data master"))

		r.Post("/owners", handleCreate[ownerRequest](md.CreateOwner, toOwnerDTO))
		r.Put("/owners/{id}", handleUpdate[ownerRequest](md.UpdateOwner, toOwnerDTO))
		r.Post("/products", handleCreate[productRequest](md.CreateProduct, toProductDTO))
		r.Put("/products/{id}", handleUpdate[productRequest](md.UpdateProduct, toProductDTO))
		r.Post("/suppliers", handleCreate[supplierRequest](md.CreateSupplier, toSupplierDTO))
		r.Put("/suppliers/{id}", handleUpdate[supplierRequest](md.UpdateSupplier, toSupplierDTO))
		r.Post("/customers", handleCreate[customerRequest](md.CreateCustomer, toCustomerDTO))
		r.Put("/customers/{id}", handleUpdate[customerRequest](md.UpdateCustomer, toCustomerDTO))
	})

	// Reading master data needs a role in the company, but any of the three:
	// a cashier has to see the products they are selling.
	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(anyRole, "tidak punya akses"))

		r.Get("/owners", handleList(md.ListOwners, toOwnerDTO))
		r.Get("/products", handleList(md.ListProducts, toProductDTO))
		r.Get("/suppliers", handleList(md.ListSuppliers, toSupplierDTO))
		r.Get("/customers", handleList(md.ListCustomers, toCustomerDTO))
	})
}

// anyRole admits all three roles. Spelled out rather than passed as a literal
// so the intent is greppable next to the capability predicates.
func anyRole(r service.Role) bool { return r.Valid() }

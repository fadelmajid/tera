package http

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
)

const maxJSONBody = 64 << 10

// --- request shapes ---------------------------------------------------------

type ownerRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Note     string `json:"note"`
	IsActive *bool  `json:"is_active"`
}

func (r ownerRequest) toInput() service.OwnerInput {
	return service.OwnerInput{Code: r.Code, Name: r.Name, Note: r.Note, IsActive: activeOrDefault(r.IsActive)}
}

type productRequest struct {
	Code     string `json:"code"`
	Barcode  string `json:"barcode"`
	Name     string `json:"name"`
	Unit     string `json:"unit"`
	Category string `json:"category"`
	// Empty or absent is the company bucket (R2.2).
	OwnerID string `json:"owner_id"`
	// money.IDR refuses a fractional or scientific JSON number outright, so a
	// client sending 27500.5 gets an error rather than a silent truncation
	// (INV-1).
	SalePriceIDR money.IDR `json:"sale_price_idr"`
	IsActive     *bool     `json:"is_active"`
}

func (r productRequest) toInput() service.ProductInput {
	return service.ProductInput{
		Code: r.Code, Barcode: r.Barcode, Name: r.Name, Unit: r.Unit,
		Category: r.Category, OwnerID: r.OwnerID, SalePriceIDR: r.SalePriceIDR,
		IsActive: activeOrDefault(r.IsActive),
	}
}

type supplierRequest struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	NPWP         string `json:"npwp"`
	Address      string `json:"address"`
	Phone        string `json:"phone"`
	IssuesFaktur bool   `json:"issues_faktur"`
	IsActive     *bool  `json:"is_active"`
}

func (r supplierRequest) toInput() service.SupplierInput {
	return service.SupplierInput{
		Code: r.Code, Name: r.Name, NPWP: r.NPWP, Address: r.Address, Phone: r.Phone,
		IssuesFaktur: r.IssuesFaktur, IsActive: activeOrDefault(r.IsActive),
	}
}

type customerRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	NPWP     string `json:"npwp"`
	NIK      string `json:"nik"`
	Address  string `json:"address"`
	Phone    string `json:"phone"`
	IsActive *bool  `json:"is_active"`
}

func (r customerRequest) toInput() service.CustomerInput {
	return service.CustomerInput{
		Code: r.Code, Name: r.Name, NPWP: r.NPWP, NIK: r.NIK,
		Address: r.Address, Phone: r.Phone, IsActive: activeOrDefault(r.IsActive),
	}
}

type entityRequest struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	IsPKP              bool   `json:"is_pkp"`
	NPWP               string `json:"npwp"`
	Address            string `json:"address"`
	Phone              string `json:"phone"`
	Timezone           string `json:"timezone"`
	BookYearStartMonth int64  `json:"book_year_start_month"`
}

func (r entityRequest) toInput() service.EntityInput {
	return service.EntityInput{
		Code: r.Code, Name: r.Name, IsPKP: r.IsPKP, NPWP: r.NPWP, Address: r.Address,
		Phone: r.Phone, Timezone: r.Timezone, BookYearStartMonth: r.BookYearStartMonth,
	}
}

// activeOrDefault treats an absent is_active as active. A create form that does
// not mention the field should not produce a deactivated record.
func activeOrDefault(v *bool) bool { return v == nil || *v }

// --- generic handlers -------------------------------------------------------

type toInputer[In any] interface{ toInput() In }

func handleCreate[Req toInputer[In], In, Row, DTO any](
	create func(context.Context, service.Actor, In) (Row, error),
	toDTO func(Row) DTO,
) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req Req
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := create(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, toDTO(row))
	}
}

func handleUpdate[Req toInputer[In], In, Row, DTO any](
	update func(context.Context, service.Actor, string, In) (Row, error),
	toDTO func(Row) DTO,
) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": "id tidak diberikan"})
			return
		}

		var req Req
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := update(r.Context(), actorFrom(r), id, req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, toDTO(row))
	}
}

func handleList[Row, DTO any](
	list func(context.Context, bool) ([]Row, error),
	toDTO func(Row) DTO,
) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		includeInactive := r.URL.Query().Get("include_inactive") == "1"

		rows, err := list(r.Context(), includeInactive)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Never null: an empty catalogue is [], so the client can map over it
		// without a guard.
		out := make([]DTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

// --- entity setup -----------------------------------------------------------

func handleListEntities(md *service.MasterData) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		rows, err := md.ListEntities(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]entityDTO, 0, len(rows))
		for _, e := range rows {
			out = append(out, toEntityDTO(e))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

// handleSetupFirstEntity creates the very first company and makes the caller
// its owner.
//
// Access is per entity (R13.4), which leaves a chicken and egg on a fresh
// install: the bootstrap admin holds no role anywhere, so no ordinary
// entity-scoped route will admit them. This route resolves it once and then
// closes: it refuses as soon as a company exists, after which creating another
// requires being an owner of one already.
// handleCreateEntity adds a second company.
//
// Found missing while building Phase 4: handleSetupFirstEntity refuses once one
// company exists and tells the user to "minta pemilik untuk menambahkan
// perusahaan lain", which was a route that did not exist. Every inter-company
// feature needs two companies (R1.1, R1.2) and the user has exactly two, so
// without this the transfer screen has nowhere to send anything.
//
// The creator is granted owner in the new company, for the same reason setup
// does it: a company nobody can reach is not a company, and the person who
// created it is the only candidate the system knows about.
func handleCreateEntity(md *service.MasterData, auth *service.Auth) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": "silakan masuk terlebih dahulu"})
			return
		}

		var req entityRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		entity, err := md.CreateEntity(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if err := auth.GrantRole(r.Context(), p.UserID, entity.ID, service.RoleOwner); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, toEntityDTO(entity))
	}
}

func handleSetupFirstEntity(md *service.MasterData, auth *service.Auth) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": "silakan masuk terlebih dahulu"})
			return
		}

		count, err := md.CountEntities(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if count > 0 {
			writeJSON(w, stdhttp.StatusConflict, map[string]any{
				"error": "perusahaan pertama sudah dibuat; minta pemilik untuk menambahkan perusahaan lain",
			})
			return
		}

		var req entityRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		actor := actorFrom(r)
		entity, err := md.CreateEntity(r.Context(), actor, req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}

		if err := auth.GrantRole(r.Context(), p.UserID, entity.ID, service.RoleOwner); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, toEntityDTO(entity))
	}
}

// --- shared -----------------------------------------------------------------

// actorFrom assembles who is accountable for a change.
//
// The audit trail is only as good as this (INV-10, R13.5), and the client
// request id ties the change back to the request that made it (INV-6).
func actorFrom(r *stdhttp.Request) service.Actor {
	var actor service.Actor
	if p, ok := PrincipalFrom(r.Context()); ok {
		actor.UserID = p.UserID
	}
	if id, ok := EntityFrom(r.Context()); ok {
		actor.LegalEntityID = id
	}
	actor.ClientRequestID = r.Header.Get(ClientRequestHeader)
	return actor
}

func decodeJSON(w stdhttp.ResponseWriter, r *stdhttp.Request, dst any) bool {
	dec := json.NewDecoder(stdhttp.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		// money.IDR refuses fractional values, and that refusal surfaces here.
		// Returning the message keeps "1000.5 is not rupiah" visible instead of
		// collapsing it into a generic parse failure.
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": decodeMessage(err)})
		return false
	}
	return true
}

func decodeMessage(err error) string {
	if errors.Is(err, money.ErrFractional) {
		return "nilai rupiah harus bilangan bulat"
	}
	return "permintaan tidak valid: " + err.Error()
}

func writeServiceError(w stdhttp.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": err.Error()})
	// R12.5: an adjustment or a void with no explanation is refused. It is bad
	// input, not a server fault, and the message names what is missing.
	case errors.Is(err, service.ErrReasonRequired):
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": err.Error()})
	case errors.Is(err, service.ErrNotFound):
		writeJSON(w, stdhttp.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, service.ErrDuplicate):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	// The request is well formed; the books are not in a state that admits it.
	// 409 rather than 400 so a client can tell "you typed it wrong" from
	// "someone else moved the stock while you were counting".
	case errors.Is(err, service.ErrAlreadyPosted):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	// INV-8 reaching the wire. Never a fallback to another owner's stock, and
	// never a 500 that reads like a bug: the message says whose stock is short.
	case errors.Is(err, service.ErrInsufficientStock):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	// The margin report refusing to compute rather than printing a figure it
	// cannot justify (D-011). 409 for the same reason as the two above: the
	// request is fine, the books are not, and the message names the sale and
	// product to look at.
	case errors.Is(err, service.ErrReportIntegrity):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	// The state of the till and of a finalised sale (R12.3). Every one of these
	// already says in Indonesian what the cashier should do instead — open a
	// session, use a return rather than a void — and falling through to a 500
	// would replace all of that with "kesalahan internal" at the moment someone
	// has a customer in front of them.
	case errors.Is(err, service.ErrNoOpenSession),
		errors.Is(err, service.ErrSessionClosed),
		errors.Is(err, service.ErrVoidWindowClosed),
		errors.Is(err, service.ErrVoidAfterReturn),
		errors.Is(err, service.ErrAlreadyVoid):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	// R4.5's blocking confirmation, refusing the write. The message is the
	// entire feature — it says the input PPN credit on this stock is destroyed
	// permanently and the transfer has not happened — so it must reach the
	// person, not be swallowed by a 500.
	case errors.Is(err, service.ErrCreditLossNotAcknowledged):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, service.ErrSameEntity):
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": err.Error()})
	// The tax engine refusing to price a sale (TASKS 5.4, 5.5). This is the one
	// that most needs to reach a person: the till has stopped because a PKP
	// company has no PPN rule in force, or because a company marked non-PKP
	// holds one that is. Both are somebody forgetting to configure something,
	// both are fixed in a minute on the tax settings screen, and the message
	// says which. Collapsed into "kesalahan internal" it is instead a cashier
	// with a customer in front of them and nothing to go on.
	case errors.Is(err, service.ErrTaxConfig):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, service.ErrRuleInForce), errors.Is(err, service.ErrRuleClosed):
		writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, stdhttp.StatusInternalServerError, map[string]any{"error": "kesalahan internal"})
	}
}

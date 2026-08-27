package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// ErrAlreadyPosted is returned when a posted opname is edited or posted again.
var ErrAlreadyPosted = errors.New("service: opname sudah diposting")

// Opname is the physical stock count. R12.4-5, TASKS 1.10.
//
// Required at go-live and periodically after, because the quantities in the
// system the business uses today are already drifting from reality: it records
// an inter-company transaction without moving the stock (REQUIREMENTS §9 item
// 2). The first count is how that drift gets measured instead of inherited.
//
// Counting and posting are separate steps on purpose. Counting a shop takes
// hours; posting is the moment stock actually moves. A half-finished count must
// not be half-applied to the books.
type Opname struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewOpname builds the service.
func NewOpname(db *store.DB, aud *Auditor, now func() time.Time) *Opname {
	if now == nil {
		now = time.Now
	}
	return &Opname{db: db, q: gen.New(db), aud: aud, now: now}
}

// CountSheet is what is currently on the books, grouped the way a count is
// taken: per product, per owner.
//
// Per owner is not a display choice. A variance has to land in exactly one
// person's bucket (INV-8), so Budi's 3 boxes and Sari's 40 of the same item are
// counted and adjusted separately even though they sit on the same shelf.
func (o *Opname) CountSheet(ctx context.Context, entityID string) ([]gen.ListStockOnHandByOwnerRow, error) {
	rows, err := o.q.ListStockOnHandByOwner(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: count sheet: %w", err)
	}
	return rows, nil
}

// StartInput opens a count.
type StartInput struct {
	CountDate string
	Note      string
}

// Start opens a draft count.
func (o *Opname) Start(ctx context.Context, actor Actor, in StartInput) (gen.StockOpname, error) {
	var created gen.StockOpname
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, countedAt, err := clock.resolveDate(strings.TrimSpace(in.CountDate), o.now())
		if err != nil {
			return err
		}

		row, err := gen.New(tx).CreateOpname(ctx, gen.CreateOpnameParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID,
			CountedAt: countedAt.Unix(), BusinessDate: day, Note: nilIfEmpty(in.Note),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "stock_opname", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// ReasonCodes are the explanations a variance may carry (R12.5).
//
// The same six the storage layer allows, held here so an unrecognised one is a
// refusal that names the alternatives rather than a constraint violation
// surfacing as "kesalahan internal" — which is what a person counting a shelf
// would otherwise be told.
var ReasonCodes = []string{
	"RUSAK", "HILANG", "KADALUARSA", "SALAH_CATAT", "RETUR_TIDAK_TERCATAT", "LAINNYA",
}

func validReasonCode(code string) bool {
	for _, c := range ReasonCodes {
		if c == code {
			return true
		}
	}
	return false
}

// LineInput is one counted shelf position.
type LineInput struct {
	ProductID  string
	OwnerID    string
	CountedQty int64
	ReasonCode string
	ReasonNote string
	// UnitCostIDR prices a surplus. Nil asks for the default: the most recent
	// layer of the same product and owner. Zero is a legitimate explicit answer
	// -- free samples do turn up -- which is why this is a pointer.
	UnitCostIDR *money.IDR
}

// SaveLine records a counted quantity and its variance.
//
// system_qty is snapshotted here, at the moment of counting, rather than read
// again at posting time. Re-reading it would silently absorb any sale rung
// between the count and the posting and report a variance of zero -- which is
// the one thing a stock count must never do.
func (o *Opname) SaveLine(ctx context.Context, actor Actor, opnameID string, in LineInput) (gen.StockOpnameLine, error) {
	in.ProductID = strings.TrimSpace(in.ProductID)
	in.OwnerID = strings.TrimSpace(in.OwnerID)
	switch {
	case in.ProductID == "":
		return gen.StockOpnameLine{}, fmt.Errorf("%w: produk wajib dipilih", ErrValidation)
	case in.CountedQty < 0:
		return gen.StockOpnameLine{}, fmt.Errorf("%w: jumlah hitung tidak boleh negatif", ErrValidation)
	}

	var saved gen.StockOpnameLine
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		header, err := q.GetOpname(ctx, opnameID)
		if err != nil || header.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: opname tidak ditemukan", ErrNotFound)
		}
		if header.Status == "POSTED" {
			return fmt.Errorf("%w: hitung ulang jika ada koreksi", ErrAlreadyPosted)
		}

		owner := nilIfEmpty(in.OwnerID)
		systemQty, err := q.GetStockOnHandForOwner(ctx, gen.GetStockOnHandForOwnerParams{
			EntityID: actor.LegalEntityID, ProductID: in.ProductID, OwnerID: owner,
		})
		if err != nil {
			return fmt.Errorf("service: stock on hand: %w", err)
		}

		variance := in.CountedQty - systemQty
		if variance != 0 && strings.TrimSpace(in.ReasonCode) == "" {
			return fmt.Errorf("%w: selisih %d butuh kode alasan (R12.5)", ErrReasonRequired, variance)
		}
		if in.ReasonCode != "" && !validReasonCode(in.ReasonCode) {
			return fmt.Errorf("%w: kode alasan %q tidak dikenal; pilih salah satu dari %s",
				ErrValidation, in.ReasonCode, strings.Join(ReasonCodes, ", "))
		}

		unitCost, err := o.resolveSurplusCost(ctx, tx, actor.LegalEntityID, in, owner, variance)
		if err != nil {
			return err
		}

		row, err := q.UpsertOpnameLine(ctx, gen.UpsertOpnameLineParams{
			ID: store.NewID(), OpnameID: opnameID, ProductID: in.ProductID, OwnerID: owner,
			SystemQty: systemQty, CountedQty: in.CountedQty, Variance: variance,
			ReasonCode: nilIfEmpty(in.ReasonCode), ReasonNote: nilIfEmpty(in.ReasonNote),
			UnitCostIdr: unitCost, CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		saved = row
		return nil
	})
	return saved, err
}

// resolveSurplusCost prices found stock.
//
// A surplus becomes a stock layer, and every layer must be able to say what it
// cost (SPEC §3.2). The default is the most recent layer of the same product
// and owner, because found stock is nearly always a miscounted recent delivery.
// Where there is no such layer there is nothing to infer from, and the caller
// is asked rather than given a zero that would inflate margin the moment those
// units sell.
func (o *Opname) resolveSurplusCost(
	ctx context.Context, tx *sql.Tx, entityID string, in LineInput, owner *string, variance int64,
) (*int64, error) {
	if variance <= 0 {
		return nil, nil // a shortfall costs whatever its layers cost; FIFO knows
	}
	if in.UnitCostIDR != nil {
		v := int64(*in.UnitCostIDR)
		if v < 0 {
			return nil, fmt.Errorf("%w: harga satuan tidak boleh negatif", ErrValidation)
		}
		return &v, nil
	}

	latest, err := gen.New(tx).LatestLayerUnitCost(ctx, gen.LatestLayerUnitCostParams{
		EntityID: entityID, ProductID: in.ProductID, OwnerID: owner,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: stok lebih %d unit belum punya harga acuan; isi harga satuan",
			ErrValidation, variance)
	}
	return &latest, nil
}

// Lines returns the variance report for a count (R12.4).
func (o *Opname) Lines(ctx context.Context, entityID, opnameID string) (gen.StockOpname, []gen.ListOpnameLinesRow, error) {
	header, err := o.q.GetOpname(ctx, opnameID)
	if err != nil || header.EntityID != entityID {
		return gen.StockOpname{}, nil, fmt.Errorf("%w: opname tidak ditemukan", ErrNotFound)
	}
	lines, err := o.q.ListOpnameLines(ctx, opnameID)
	if err != nil {
		return gen.StockOpname{}, nil, fmt.Errorf("service: opname lines: %w", err)
	}
	return header, lines, nil
}

// List returns the counts for a company, newest first.
func (o *Opname) List(ctx context.Context, entityID string) ([]gen.StockOpname, error) {
	rows, err := o.q.ListOpnames(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list opnames: %w", err)
	}
	return rows, nil
}

// PostResult summarises what posting moved.
type PostResult struct {
	Opname       gen.StockOpname
	LinesPosted  int
	SurplusUnits int64
	ShortUnits   int64
	CostDelta    money.IDR
}

// Post writes the adjustments the count implies. R12.4-5.
//
// A surplus creates a layer attributed to the same owner that was counted
// short or over; a shortfall draws that owner's layers down oldest-first
// through the same domain function a sale uses. Owner scoping holds either way
// (INV-8): a variance on Budi's shelf never touches Sari's stock, because
// posting one person's miscount against another's goods is a transfer of money
// between family members dressed up as a correction.
//
// Everything commits together. A count that adjusted half its lines would leave
// the books in a state nobody counted.
func (o *Opname) Post(ctx context.Context, actor Actor, opnameID, reason string) (PostResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return PostResult{}, fmt.Errorf("%w: alasan posting opname wajib diisi", ErrReasonRequired)
	}

	var out PostResult
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		header, err := q.GetOpname(ctx, opnameID)
		if err != nil || header.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: opname tidak ditemukan", ErrNotFound)
		}
		if header.Status == "POSTED" {
			return ErrAlreadyPosted
		}

		lines, err := q.ListOpnameLines(ctx, opnameID)
		if err != nil {
			return fmt.Errorf("service: opname lines: %w", err)
		}

		now := o.now().Unix()
		occurredAt := time.Unix(header.CountedAt, 0)

		for _, l := range lines {
			if l.Variance == 0 {
				continue
			}
			delta, err := o.postLine(ctx, tx, actor, header, l, occurredAt, now)
			if err != nil {
				return err
			}
			out.LinesPosted++
			out.CostDelta = out.CostDelta.Add(delta)
			if l.Variance > 0 {
				out.SurplusUnits += l.Variance
			} else {
				out.ShortUnits += -l.Variance
			}
		}

		posted, err := q.MarkOpnamePosted(ctx, gen.MarkOpnamePostedParams{
			ID: opnameID, PostedAt: &now, PostedBy: nilIfEmpty(actor.UserID),
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Opname = posted

		// ADJUST carries a reason by requirement (R12.5) and both sides by
		// invariant (INV-10).
		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "stock_opname", RecordID: opnameID,
			Action: ActionAdjust, Before: header, After: posted, Reason: reason,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return PostResult{}, err
	}
	return out, nil
}

// postLine turns one variance into stock movements.
func (o *Opname) postLine(
	ctx context.Context, tx *sql.Tx, actor Actor,
	header gen.StockOpname, l gen.ListOpnameLinesRow, occurredAt time.Time, now int64,
) (money.IDR, error) {
	q := gen.New(tx)

	if l.Variance > 0 {
		unitCost := money.Zero
		if l.UnitCostIdr != nil {
			unitCost = money.IDR(*l.UnitCostIdr)
		}
		total := unitCost.MulQty(l.Variance)

		layer, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
			ID: store.NewID(), EntityID: header.EntityID, ProductID: l.ProductID,
			OwnerID: l.OwnerID, AcquiredAt: occurredAt.Unix(), BusinessDate: header.BusinessDate,
			Source: "ADJUSTMENT", SourceDocID: &header.ID,
			QtyIn: l.Variance, CostTotalIdr: int64(total),
			// Found stock carries no faktur of its own. Whatever paperwork the
			// original delivery had is on that delivery's layer, not this one,
			// and inventing a credit here would claim input PPN twice (INV-9).
			FakturReceived: 0, PpnPaidIdr: 0, CreatedAt: now,
		})
		if err != nil {
			return 0, wrapWrite(err)
		}

		if _, err := q.CreateOpnamePosting(ctx, gen.CreateOpnamePostingParams{
			ID: store.NewID(), OpnameLineID: l.ID, StockLayerID: &layer.ID,
			Qty: l.Variance, CostIdr: int64(total), CreatedAt: now,
		}); err != nil {
			return 0, wrapWrite(err)
		}
		return total, nil
	}

	// Shortfall: draw the missing units out of that owner's layers, oldest
	// first, through the same function a sale uses.
	short := -l.Variance
	balances, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: header.EntityID, ProductID: l.ProductID,
	})
	if err != nil {
		return 0, fmt.Errorf("service: layers for consumption: %w", err)
	}

	layers := make([]fifo.Layer, 0, len(balances))
	for _, b := range balances {
		layers = append(layers, toFifoLayer(b))
	}

	drawn, err := fifo.Consume(fifo.Request{
		EntityID: header.EntityID, ProductID: l.ProductID, OwnerID: ownerIDOf(l.OwnerID),
		Qty: short, MovementID: header.ID, OccurredAt: occurredAt,
	}, layers)
	if err != nil {
		var insufficient *fifo.InsufficientStockError
		if errors.As(err, &insufficient) {
			return 0, fmt.Errorf("%w: %s kurang %d unit tetapi hanya %d tercatat atas nama pemilik ini",
				ErrValidation, l.ProductName, short, insufficient.Available)
		}
		return 0, err
	}

	for _, c := range drawn.Consumptions {
		consumption, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
			ID: store.NewID(), LayerID: c.LayerID, MovementID: header.ID,
			MovementType: "ADJUSTMENT", QtyOut: c.QtyOut, CostIdr: int64(c.Cost),
			OccurredAt: occurredAt.Unix(), BusinessDate: header.BusinessDate, CreatedAt: now,
		})
		if err != nil {
			return 0, wrapWrite(err)
		}
		if _, err := q.CreateOpnamePosting(ctx, gen.CreateOpnamePostingParams{
			ID: store.NewID(), OpnameLineID: l.ID, ConsumptionID: &consumption.ID,
			Qty: c.QtyOut, CostIdr: int64(c.Cost), CreatedAt: now,
		}); err != nil {
			return 0, wrapWrite(err)
		}
	}
	return drawn.COGS.Neg(), nil
}

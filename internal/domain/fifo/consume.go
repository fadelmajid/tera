package fifo

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Request is a demand for stock: this many units of this product, belonging to
// this owner, in this entity.
//
// The owner is part of the identity of what is being asked for, not a filter
// applied afterwards. "Three boxes of gloves" is not a well-formed request in
// this system; "three boxes of Budi's gloves" is.
type Request struct {
	EntityID  string
	ProductID string

	// OwnerID scopes the draw. Company is a valid, deliberate value; see
	// OwnerID on why it must never stand in for an unresolved owner.
	OwnerID OwnerID

	// Qty is positive. Returns are not negative consumption requests — they are
	// reversals against the specific draws they undo (D-010), which is a
	// separate operation with a separate shape.
	Qty int64

	// MovementID ties every consumption this produces back to the sale,
	// transfer, or adjustment that caused it. Without it a draw is
	// unattributable and the drill-down in SPEC §4.2 has nothing to walk.
	MovementID string

	// OccurredAt is passed in rather than read from the clock, so the function
	// stays pure and the tests stay deterministic.
	OccurredAt time.Time
}

// Consumption is one draw against one layer: an appended record, never an edit
// to the layer (INV-7).
//
// Cost is the exact slice of that layer's CostTotal this draw took — not a
// quantity times a rounded unit cost. That distinction is what makes a layer
// consume out to precisely what it cost.
type Consumption struct {
	LayerID    string
	MovementID string
	QtyOut     int64
	Cost       money.IDR
	OccurredAt time.Time

	// ReversesID names the draw this one undoes, and is empty on an ordinary
	// draw. A reversal carries a negative QtyOut and Cost; a row that puts
	// stock back without naming what it undoes is a stock increase disguised as
	// a correction (D-010). See Reverse.
	ReversesID string
}

// Result is what a draw produced: the rows to append, and their total.
type Result struct {
	// Consumptions are in the order they were drawn, oldest layer first. They
	// are ready to insert as stock_consumption rows; the store assigns ids.
	Consumptions []Consumption

	// COGS is Σ Consumptions[i].Cost — the cost of goods sold for this
	// movement, from the actual layers drawn rather than an average
	// (SPEC §4.1).
	COGS money.IDR
}

// Consume draws Qty units from an owner's layers, oldest first, and reports
// exactly what each layer gave up (SPEC §3.3).
//
// It is all-or-nothing. If the owner's layers cannot cover the request, nothing
// is consumed and an *InsufficientStockError comes back instead. There is no
// partial draw, and there is no fallback to another owner's stock — see below.
//
// # Owner scoping is enforced here, not assumed of the caller
//
// The layers slice may contain anything: other products, other entities, other
// owners. This function does the filtering itself and will only ever draw from
// layers matching the request on all three (INV-8). Callers are expected to
// pass every layer for the (entity, product) pair rather than a pre-filtered
// set — that is not laxness, it is what lets an insufficient-stock error say
// how much stock other owners hold, which is the difference between an error a
// shopkeeper can act on and one that looks like a bug.
//
// A draw across owners moves money between family members who settle monthly on
// these figures (R2.4). Making it structurally impossible here is cheaper than
// trusting every future caller to write the right WHERE clause.
//
// # Ordering
//
// Oldest AcquiredAt first, ties broken by id. Ids are UUIDv7 and therefore
// time-ordered (D-003), so the tiebreak resolves to insertion order — which is
// what FIFO means when a single purchase creates several layers in the same
// second. The sort is on a copy; the caller's slice is not touched.
//
// # Purity
//
// No clock, no database, no randomness. Same inputs, same outputs, always. That
// is what makes it testable, and this is the function everything downstream
// derives its cost from.
func Consume(req Request, layers []Layer) (Result, error) {
	if err := req.validate(); err != nil {
		return Result{}, err
	}

	candidates, otherOwners, err := scope(req, layers)
	if err != nil {
		return Result{}, err
	}

	var available int64
	for _, l := range candidates {
		available += l.Remaining()
	}
	if available < req.Qty {
		// Refuse. Never reach for otherOwners, however much of it there is.
		return Result{}, &InsufficientStockError{
			EntityID:             req.EntityID,
			ProductID:            req.ProductID,
			OwnerID:              req.OwnerID,
			Requested:            req.Qty,
			Available:            available,
			OtherOwnersAvailable: otherOwners,
		}
	}

	slices.SortStableFunc(candidates, byAcquisition)

	consumptions := make([]Consumption, 0, len(candidates))
	var (
		cogs      money.IDR
		remaining = req.Qty
	)
	for _, l := range candidates {
		if remaining == 0 {
			break
		}
		onHand := l.Remaining()
		if onHand <= 0 {
			continue // exhausted layer; it stays in the trail, it just has nothing left
		}

		take := min(onHand, remaining)

		// The slice of this layer's cost that these units represent: the
		// difference between two prefixes of the layer, never a rounded unit
		// cost multiplied out. See Layer.CostAt for why this is exact.
		cost := l.CostAt(l.QtyConsumed + take).Sub(l.CostConsumed())

		consumptions = append(consumptions, Consumption{
			LayerID:    l.ID,
			MovementID: req.MovementID,
			QtyOut:     take,
			Cost:       cost,
			OccurredAt: req.OccurredAt,
		})
		cogs = cogs.Add(cost)
		remaining -= take
	}

	return Result{Consumptions: consumptions, COGS: cogs}, nil
}

// scope splits the layers into the ones this request may draw from and a count
// of what other owners hold, validating everything it is given on the way past.
//
// Validation covers every layer, not only the matching ones: a malformed layer
// anywhere in the set means the caller's query is wrong, and finding that out
// on a draw that happened to miss it is worse than finding out now.
func scope(req Request, layers []Layer) (candidates []Layer, otherOwners int64, err error) {
	seen := make(map[string]struct{}, len(layers))
	candidates = make([]Layer, 0, len(layers))

	for _, l := range layers {
		if err := l.validate(); err != nil {
			return nil, 0, err
		}
		if _, dup := seen[l.ID]; dup {
			return nil, 0, fmt.Errorf(
				"%w: layer %s was passed twice, which would count the same stock twice",
				ErrInvalidLayer, l.ID,
			)
		}
		seen[l.ID] = struct{}{}

		if l.EntityID != req.EntityID || l.ProductID != req.ProductID {
			continue
		}
		if l.OwnerID != req.OwnerID {
			// Counted so the error can explain itself. Never a candidate.
			otherOwners += l.Remaining()
			continue
		}
		candidates = append(candidates, l)
	}

	return candidates, otherOwners, nil
}

// byAcquisition orders layers oldest first, ties broken by id (SPEC §3.3).
func byAcquisition(a, b Layer) int {
	if c := a.AcquiredAt.Compare(b.AcquiredAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func (r Request) validate() error {
	switch {
	case strings.TrimSpace(r.EntityID) == "":
		return fmt.Errorf("%w: entity id is empty", ErrInvalidRequest)
	case strings.TrimSpace(r.ProductID) == "":
		return fmt.Errorf("%w: product id is empty", ErrInvalidRequest)
	case strings.TrimSpace(r.MovementID) == "":
		return fmt.Errorf("%w: movement id is empty, so the draw could not be traced back to what caused it", ErrInvalidRequest)
	case r.Qty <= 0:
		return fmt.Errorf("%w: quantity must be positive, got %d", ErrInvalidRequest, r.Qty)
	case r.OccurredAt.IsZero():
		return fmt.Errorf("%w: occurred-at is zero", ErrInvalidRequest)
	}
	return nil
}

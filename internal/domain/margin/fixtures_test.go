package margin_test

import (
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// The cast, kept stable across every test in this package so the figures below
// can be read as a story rather than decoded each time.
//
// Budi and Sari are family members; the company bucket holds stock nobody has
// claimed (R2.2). Money moves between the three of them monthly on what this
// package computes.
const (
	budi = margin.OwnerID("owner-budi")
	sari = margin.OwnerID("owner-sari")
)

var names = map[margin.OwnerID]string{
	budi:           "Budi",
	sari:           "Sari",
	margin.Company: "Perusahaan",
}

var wib = time.FixedZone("WIB", 7*3600)

func acquired(day int) time.Time { return time.Date(2026, time.October, day, 9, 0, 0, 0, wib) }

// oct and nov build the settlement windows the tests report on.
var (
	oct = margin.Month(2026, time.October)
	nov = margin.Month(2026, time.November)
)

// The two answers SPEC §4.4 is a choice between. D-012 settled on
// atReturnDate; both stay exercised, because both stay switchable.
const (
	atReturnDate = margin.AtReturnDate
	atSaleDate   = margin.AtSaleDate
)

type drawOpt func(*margin.Draw)

func noFaktur(d *margin.Draw) { d.FakturReceived = false }

// draw builds one layer consumption: stock leaving, at what it cost.
func draw(id, layerID, productID string, owner margin.OwnerID, day int, qty int64, cost money.IDR, opts ...drawOpt) margin.Draw {
	d := margin.Draw{
		ID: id, LayerID: layerID, ProductID: productID, OwnerID: owner,
		QtyOut: qty, Cost: cost,
		LayerAcquiredAt: acquired(day), LayerSource: "PURCHASE",
		LayerQtyIn: qty, LayerCostTotal: cost, FakturReceived: true,
	}
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

// reversal builds the row a return appends: the same layer, giving stock back
// at exactly the cost it was taken at (D-010).
func reversal(id, reverses, layerID, productID string, owner margin.OwnerID, day int, qty int64, cost money.IDR) margin.Draw {
	d := draw(id, layerID, productID, owner, day, -qty, cost.Neg())
	d.ReversesID = reverses
	return d
}

func saleLine(id, productID, code, name string, owner margin.OwnerID, qty int64, revenue, cogs money.IDR) margin.SaleLine {
	return margin.SaleLine{
		ID: id, ProductID: productID, ProductCode: code, ProductName: name,
		OwnerID: owner, Qty: qty, Revenue: revenue, RecordedCOGS: cogs,
	}
}

func returnLine(id, saleLineID, productID, code, name string, owner margin.OwnerID, qty int64, refund, cogs money.IDR) margin.ReturnLine {
	return margin.ReturnLine{
		ID: id, SaleLineID: saleLineID, ProductID: productID, ProductCode: code,
		ProductName: name, OwnerID: owner, Qty: qty, Refund: refund, COGSReversed: cogs,
	}
}

// ownerLine finds one owner's line on a report, failing loudly when it is
// missing: an owner silently absent from a settlement report is the failure
// mode this whole package exists to prevent.
func ownerLine(t *testing.T, r margin.Report, id margin.OwnerID) margin.OwnerReport {
	t.Helper()
	for _, o := range r.Owners {
		if o.OwnerID == id {
			return o
		}
	}
	t.Fatalf("owner %s has no line on the report", id)
	return margin.OwnerReport{}
}

func compute(t *testing.T, in margin.Input) margin.Report {
	t.Helper()
	if in.OwnerNames == nil {
		in.OwnerNames = names
	}
	got, err := margin.Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	return got
}

func mustEqual(t *testing.T, what string, got, want money.IDR) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

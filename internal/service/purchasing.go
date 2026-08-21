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

// ErrInsufficientStock is returned when a draw asks for more than an owner's
// layers hold. It wraps the domain error so callers can match either.
var ErrInsufficientStock = fifo.ErrInsufficientStock

// Purchasing records what the business bought and turns it into stock.
//
// This is where INV-9 becomes real. A purchase carries one fact -- whether the
// supplier handed over a faktur pajak -- and that fact decides the cost basis
// of every layer the purchase creates (SPEC §3.2). Same supplier, same price,
// with and without: two different costs, and therefore two different margins on
// an identical sale. The business cannot see that today, which is item 3 of the
// pain they described.
type Purchasing struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewPurchasing builds the service.
func NewPurchasing(db *store.DB, aud *Auditor, now func() time.Time) *Purchasing {
	if now == nil {
		now = time.Now
	}
	return &Purchasing{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- input ------------------------------------------------------------------

// PurchaseLineInput is one line of a supplier's invoice.
//
// The PPN is typed in as the invoice states it, not computed from a rate. Two
// reasons: deriving it would mean hardcoding 11%, which INV-4 forbids and the
// effective-dated tax_rule table does not land until TASKS 5.1; and what the
// supplier actually charged is a fact about a piece of paper, not a
// calculation. When the tax engine arrives it can offer this figure as a
// default, but the paper still wins.
type PurchaseLineInput struct {
	ProductID    string
	Qty          int64
	UnitPriceIDR money.IDR
	PPNIDR       money.IDR
	ExpiryDate   string
}

// PurchaseInput is a supplier invoice as entered.
type PurchaseInput struct {
	SupplierID string
	InvoiceNo  string
	// PurchaseDate is 'YYYY-MM-DD' in the company's timezone. Empty means
	// today. It is the invoice's date, not the moment of typing: an invoice
	// entered three days late describes goods that arrived three days ago, and
	// FIFO has to agree with the shelf.
	PurchaseDate string

	// FakturReceived is the field this whole screen exists for (INV-9).
	FakturReceived bool
	FakturNo       string

	IsCredit bool
	DueDate  string
	Note     string

	Lines []PurchaseLineInput
}

// PurchaseResult is everything one purchase wrote.
type PurchaseResult struct {
	Purchase gen.Purchase
	Lines    []gen.PurchaseLine
	Layers   []gen.StockLayer
	// Payable is non-nil when the purchase was on credit (hutang, R5.5).
	Payable *gen.Payable
}

func (in *PurchaseInput) normalise() error {
	in.SupplierID = strings.TrimSpace(in.SupplierID)
	in.InvoiceNo = strings.TrimSpace(in.InvoiceNo)
	in.PurchaseDate = strings.TrimSpace(in.PurchaseDate)
	in.FakturNo = strings.TrimSpace(in.FakturNo)
	in.DueDate = strings.TrimSpace(in.DueDate)

	switch {
	case in.SupplierID == "":
		return fmt.Errorf("%w: pemasok wajib dipilih", ErrValidation)
	case len(in.Lines) == 0:
		return fmt.Errorf("%w: pembelian harus punya minimal satu baris", ErrValidation)
	// A faktur number without the faktur is the confusion this field exists to
	// prevent: the paper is what makes the PPN creditable, not the number.
	case in.FakturNo != "" && !in.FakturReceived:
		return fmt.Errorf("%w: nomor faktur diisi tetapi faktur ditandai belum diterima", ErrValidation)
	case in.DueDate != "" && !in.IsCredit:
		return fmt.Errorf("%w: jatuh tempo hanya berlaku untuk pembelian kredit", ErrValidation)
	}

	for i := range in.Lines {
		l := &in.Lines[i]
		l.ProductID = strings.TrimSpace(l.ProductID)
		l.ExpiryDate = strings.TrimSpace(l.ExpiryDate)

		switch {
		case l.ProductID == "":
			return fmt.Errorf("%w: baris %d belum memilih produk", ErrValidation, i+1)
		case l.Qty <= 0:
			return fmt.Errorf("%w: baris %d jumlah harus lebih dari nol", ErrValidation, i+1)
		case l.UnitPriceIDR.IsNegative():
			return fmt.Errorf("%w: baris %d harga tidak boleh negatif", ErrValidation, i+1)
		case l.PPNIDR.IsNegative():
			return fmt.Errorf("%w: baris %d PPN tidak boleh negatif", ErrValidation, i+1)
		}
	}
	return nil
}

// --- create -----------------------------------------------------------------

// CreatePurchase records an invoice and creates the stock layers it bought.
//
// Everything commits together or nothing does: the purchase, its lines, one
// stock layer per line, the hutang row if it was on credit, and the audit
// entry. A purchase whose layers did not land would be stock the business paid
// for and cannot sell; layers with no purchase behind them would be cost nobody
// can explain.
func (p *Purchasing) CreatePurchase(ctx context.Context, actor Actor, in PurchaseInput) (PurchaseResult, error) {
	if err := in.normalise(); err != nil {
		return PurchaseResult{}, err
	}

	var out PurchaseResult
	err := p.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}

		day, acquiredAt, err := clock.resolveDate(in.PurchaseDate, p.now())
		if err != nil {
			return err
		}
		if in.DueDate != "" {
			if _, err := parseBusinessDate(in.DueDate, clock.loc); err != nil {
				return err
			}
		}

		// Price the lines before writing anything: the header carries the
		// totals, and it has to exist before its lines can reference it.
		priced, err := p.priceLines(ctx, tx, clock, in)
		if err != nil {
			return err
		}

		now := p.now().Unix()
		purchaseID := store.NewID()

		purchase, err := q.CreatePurchase(ctx, gen.CreatePurchaseParams{
			ID: purchaseID, EntityID: actor.LegalEntityID, SupplierID: in.SupplierID,
			InvoiceNo: nilIfEmpty(in.InvoiceNo), OccurredAt: acquiredAt.Unix(), BusinessDate: day,
			FakturReceived: boolToInt(in.FakturReceived), FakturNo: nilIfEmpty(in.FakturNo),
			SubtotalIdr: int64(priced.subtotal), PpnIdr: int64(priced.ppn), TotalIdr: int64(priced.total),
			IsCredit: boolToInt(in.IsCredit), DueDate: nilIfEmpty(in.DueDate),
			Note: nilIfEmpty(in.Note), CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Purchase = purchase

		for _, pl := range priced.lines {
			layer, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
				ID: store.NewID(), EntityID: actor.LegalEntityID, ProductID: pl.in.ProductID,
				// Snapshotted from the product now, not joined at report time.
				// Re-tagging a product next year must not move whose margin
				// this year's stock belonged to (INV-8).
				OwnerID:      pl.ownerID,
				AcquiredAt:   acquiredAt.Unix(),
				BusinessDate: day,
				Source:       "PURCHASE", SourceDocID: &purchaseID,
				QtyIn: pl.in.Qty,
				// The cost basis decision, made in the domain (SPEC §3.2).
				CostTotalIdr:   int64(pl.basis.CostTotal),
				FakturReceived: boolToInt(in.FakturReceived),
				PpnPaidIdr:     int64(pl.basis.PPNPaid),
				ExpiryDate:     nilIfEmpty(pl.in.ExpiryDate),
				CreatedAt:      now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Layers = append(out.Layers, layer)

			line, err := q.CreatePurchaseLine(ctx, gen.CreatePurchaseLineParams{
				ID: store.NewID(), PurchaseID: purchaseID, ProductID: pl.in.ProductID,
				OwnerID: pl.ownerID, Qty: pl.in.Qty, UnitPriceIdr: int64(pl.in.UnitPriceIDR),
				SubtotalIdr: int64(pl.subtotal), PpnIdr: int64(pl.in.PPNIDR), GrossIdr: int64(pl.gross),
				CostTotalIdr: int64(pl.basis.CostTotal), CreditablePpnIdr: int64(pl.basis.CreditablePPN),
				StockLayerID: layer.ID, ExpiryDate: nilIfEmpty(pl.in.ExpiryDate), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Lines = append(out.Lines, line)
		}

		if in.IsCredit {
			payable, err := q.CreatePayable(ctx, gen.CreatePayableParams{
				ID: store.NewID(), EntityID: actor.LegalEntityID, SupplierID: in.SupplierID,
				Source: "PURCHASE", PurchaseID: &purchaseID, InvoiceNo: nilIfEmpty(in.InvoiceNo),
				AmountIdr: int64(priced.total), IncurredOn: day, DueDate: nilIfEmpty(in.DueDate),
				CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Payable = &payable
		}

		return p.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "purchase", RecordID: purchaseID,
			Action: ActionCreate, After: purchase,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return PurchaseResult{}, err
	}
	return out, nil
}

// pricedLine is one input line with its cost decision made.
type pricedLine struct {
	in       PurchaseLineInput
	ownerID  *string
	subtotal money.IDR
	gross    money.IDR
	basis    fifo.Basis
}

type pricedPurchase struct {
	lines    []pricedLine
	subtotal money.IDR
	ppn      money.IDR
	total    money.IDR
}

// priceLines resolves each line's owner and cost basis without writing.
//
// The cost decision itself is one call into the domain. Everything around it is
// lookup and arithmetic; the rule that matters lives in fifo.CostBasis, where
// it is tested in isolation.
func (p *Purchasing) priceLines(
	ctx context.Context, tx *sql.Tx, clock entityClock, in PurchaseInput,
) (pricedPurchase, error) {
	q := gen.New(tx)
	out := pricedPurchase{lines: make([]pricedLine, 0, len(in.Lines))}

	for i, l := range in.Lines {
		product, err := q.GetProduct(ctx, l.ProductID)
		if err != nil {
			return pricedPurchase{}, fmt.Errorf("%w: produk pada baris %d tidak ditemukan", ErrNotFound, i+1)
		}
		if l.ExpiryDate != "" {
			if _, err := parseBusinessDate(l.ExpiryDate, clock.loc); err != nil {
				return pricedPurchase{}, err
			}
		}

		subtotal := l.UnitPriceIDR.MulQty(l.Qty)
		gross := subtotal.Add(l.PPNIDR)

		basis, err := fifo.CostBasis(fifo.Acquisition{
			BuyerIsPKP:     clock.isPKP,
			FakturReceived: in.FakturReceived,
			GrossPaid:      gross,
			PPNPaid:        l.PPNIDR,
		})
		if err != nil {
			return pricedPurchase{}, fmt.Errorf("%w: baris %d: %w", ErrValidation, i+1, err)
		}

		out.lines = append(out.lines, pricedLine{
			in: l, ownerID: product.OwnerID, subtotal: subtotal, gross: gross, basis: basis,
		})
		out.subtotal = out.subtotal.Add(subtotal)
		out.ppn = out.ppn.Add(l.PPNIDR)
		out.total = out.total.Add(gross)
	}
	return out, nil
}

// --- reads ------------------------------------------------------------------

// GetPurchase returns one purchase with its lines.
func (p *Purchasing) GetPurchase(ctx context.Context, entityID, id string) (gen.Purchase, []gen.ListPurchaseLinesRow, error) {
	purchase, err := p.q.GetPurchase(ctx, id)
	if err != nil {
		return gen.Purchase{}, nil, fmt.Errorf("%w: pembelian tidak ditemukan", ErrNotFound)
	}
	// Scoping is checked here rather than in the query: a purchase belonging to
	// the other company must not be readable by naming its id (R13.4).
	if purchase.EntityID != entityID {
		return gen.Purchase{}, nil, fmt.Errorf("%w: pembelian tidak ditemukan", ErrNotFound)
	}

	lines, err := p.q.ListPurchaseLines(ctx, id)
	if err != nil {
		return gen.Purchase{}, nil, fmt.Errorf("service: list purchase lines: %w", err)
	}
	return purchase, lines, nil
}

// ListPurchases returns purchases in a date range, newest first.
func (p *Purchasing) ListPurchases(ctx context.Context, entityID, from, to string) ([]gen.Purchase, error) {
	if from == "" {
		from = "0000-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	rows, err := p.q.ListPurchases(ctx, gen.ListPurchasesParams{EntityID: entityID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, fmt.Errorf("service: list purchases: %w", err)
	}
	return rows, nil
}

// InputPPNPosition is the creditable input PPN for a period, net of returns.
//
// SPEC §2.4: purchases without a faktur contribute nothing -- their PPN went
// into the cost layer instead, and counting it here as well would claim the
// same rupiah twice.
type InputPPNPosition struct {
	Creditable money.IDR
	Reversed   money.IDR
	Net        money.IDR
}

// InputPPN reports creditable input PPN for a period.
func (p *Purchasing) InputPPN(ctx context.Context, entityID, from, to string) (InputPPNPosition, error) {
	creditable, err := p.q.SumCreditableInputPPN(ctx, gen.SumCreditableInputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return InputPPNPosition{}, fmt.Errorf("service: sum input ppn: %w", err)
	}
	reversed, err := p.q.SumReversedInputPPN(ctx, gen.SumReversedInputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return InputPPNPosition{}, fmt.Errorf("service: sum reversed input ppn: %w", err)
	}

	return InputPPNPosition{
		Creditable: money.IDR(creditable),
		Reversed:   money.IDR(reversed),
		Net:        money.IDR(creditable - reversed),
	}, nil
}

// --- purchase return --------------------------------------------------------

// PurchaseReturnLineInput sends part of one purchase line back to the supplier.
type PurchaseReturnLineInput struct {
	PurchaseLineID string
	Qty            int64
}

// PurchaseReturnInput is goods going back. R12.2.
type PurchaseReturnInput struct {
	PurchaseID string
	ReturnDate string
	// Required (R12.5). A correction nobody can explain later is what the audit
	// log exists to prevent.
	Reason string
	Lines  []PurchaseReturnLineInput
}

// PurchaseReturnResult is what the return wrote.
type PurchaseReturnResult struct {
	Return gen.PurchaseReturn
	Lines  []gen.PurchaseReturnLine
}

// CreatePurchaseReturn sends goods back to the supplier. R12.2.
//
// Stock leaves, so this appends a consumption against the original layer with a
// positive qty_out -- the same direction as a sale. It is not a sales return,
// which puts stock back and carries a negative qty_out (D-010); the two share a
// word and nothing else.
//
// Two refusals matter here:
//
//   - More than the line ever brought in. Returning four of three delivered is
//     data entry, not a correction.
//   - More than the layer still holds. Goods already sold cannot go back to the
//     supplier, and pretending otherwise would drive the layer negative and
//     corrupt every margin drawn from it.
//
// The input PPN claimed on the returned portion is reversed with it. Keeping
// credit for goods that went back is precisely the error R12.2 names.
func (p *Purchasing) CreatePurchaseReturn(ctx context.Context, actor Actor, in PurchaseReturnInput) (PurchaseReturnResult, error) {
	in.PurchaseID = strings.TrimSpace(in.PurchaseID)
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.PurchaseID == "":
		return PurchaseReturnResult{}, fmt.Errorf("%w: pembelian wajib dipilih", ErrValidation)
	case in.Reason == "":
		return PurchaseReturnResult{}, fmt.Errorf("%w: alasan retur wajib diisi", ErrReasonRequired)
	case len(in.Lines) == 0:
		return PurchaseReturnResult{}, fmt.Errorf("%w: retur harus punya minimal satu baris", ErrValidation)
	}

	var out PurchaseReturnResult
	err := p.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, occurredAt, err := clock.resolveDate(in.ReturnDate, p.now())
		if err != nil {
			return err
		}

		purchase, err := q.GetPurchase(ctx, in.PurchaseID)
		if err != nil || purchase.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: pembelian tidak ditemukan", ErrNotFound)
		}

		now := p.now().Unix()
		returnID := store.NewID()

		var totalCost, totalPPN money.IDR
		type pending struct {
			line          gen.PurchaseLine
			qty           int64
			cost          money.IDR
			ppn           money.IDR
			consumptionID string
		}
		pendings := make([]pending, 0, len(in.Lines))

		for i, rl := range in.Lines {
			if rl.Qty <= 0 {
				return fmt.Errorf("%w: baris %d jumlah retur harus lebih dari nol", ErrValidation, i+1)
			}

			line, err := q.GetPurchaseLine(ctx, strings.TrimSpace(rl.PurchaseLineID))
			if err != nil || line.PurchaseID != in.PurchaseID {
				return fmt.Errorf("%w: baris pembelian %d tidak ditemukan", ErrNotFound, i+1)
			}

			alreadyReturned, err := q.SumReturnedForPurchaseLine(ctx, line.ID)
			if err != nil {
				return fmt.Errorf("service: sum returned: %w", err)
			}
			if alreadyReturned+rl.Qty > line.Qty {
				return fmt.Errorf("%w: baris %d meretur %d dari %d yang dibeli (%d sudah diretur)",
					ErrValidation, i+1, rl.Qty, line.Qty, alreadyReturned)
			}

			balance, err := q.GetLayerBalance(ctx, line.StockLayerID)
			if err != nil {
				return fmt.Errorf("service: layer balance: %w", err)
			}

			// The cost of the returned units comes from the same domain
			// function a sale uses, against the one layer this line created. A
			// separate calculation here would be a second definition of what a
			// unit costs, and the two would eventually disagree.
			layer := toFifoLayer(balance)
			drawn, err := fifo.Consume(fifo.Request{
				EntityID: balance.EntityID, ProductID: balance.ProductID,
				OwnerID: ownerIDOf(balance.OwnerID), Qty: rl.Qty,
				MovementID: returnID, OccurredAt: occurredAt,
			}, []fifo.Layer{layer})
			if err != nil {
				var short *fifo.InsufficientStockError
				if errors.As(err, &short) {
					return fmt.Errorf("%w: baris %d hanya %d unit yang masih ada; sisanya sudah terjual dan tidak bisa diretur ke pemasok",
						ErrValidation, i+1, short.Available)
				}
				return err
			}

			// One layer in, so exactly one draw out.
			draw := drawn.Consumptions[0]

			consumption, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
				ID: store.NewID(), LayerID: layer.ID, MovementID: returnID,
				MovementType: "PURCHASE_RETURN", QtyOut: draw.QtyOut, CostIdr: int64(draw.Cost),
				OccurredAt: occurredAt.Unix(), BusinessDate: day, CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}

			// Reverse the claimed input PPN in proportion, by the same prefix
			// difference the cost uses, so repeated partial returns of one line
			// give back exactly what was claimed and not a rupiah more.
			credit := money.IDR(line.CreditablePpnIdr)
			ppnBack := credit.MulRatio(alreadyReturned+rl.Qty, line.Qty).
				Sub(credit.MulRatio(alreadyReturned, line.Qty))

			pendings = append(pendings, pending{
				line: line, qty: rl.Qty, cost: draw.Cost, ppn: ppnBack, consumptionID: consumption.ID,
			})
			totalCost = totalCost.Add(draw.Cost)
			totalPPN = totalPPN.Add(ppnBack)
		}

		ret, err := q.CreatePurchaseReturn(ctx, gen.CreatePurchaseReturnParams{
			ID: returnID, EntityID: actor.LegalEntityID, PurchaseID: in.PurchaseID,
			OccurredAt: occurredAt.Unix(), BusinessDate: day, Reason: in.Reason,
			CostIdr: int64(totalCost), PpnReversedIdr: int64(totalPPN),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Return = ret

		for _, pd := range pendings {
			row, err := q.CreatePurchaseReturnLine(ctx, gen.CreatePurchaseReturnLineParams{
				ID: store.NewID(), PurchaseReturnID: returnID, PurchaseLineID: pd.line.ID,
				StockLayerID: pd.line.StockLayerID, ConsumptionID: pd.consumptionID,
				Qty: pd.qty, CostIdr: int64(pd.cost), PpnReversedIdr: int64(pd.ppn), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Lines = append(out.Lines, row)
		}

		// A void carries both sides and a reason (INV-10, R12.5).
		return p.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "purchase", RecordID: in.PurchaseID,
			Action: ActionVoid, Before: purchase, After: ret, Reason: in.Reason,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return PurchaseReturnResult{}, err
	}
	return out, nil
}

// --- adapters ---------------------------------------------------------------

// toFifoLayer converts a stored balance row into the domain's value type.
//
// The domain knows nothing about sqlc rows, and this is the only place the two
// vocabularies meet (ARCHITECTURE §2).
func toFifoLayer(b gen.StockLayerBalance) fifo.Layer {
	return fifo.Layer{
		ID: b.ID, EntityID: b.EntityID, ProductID: b.ProductID,
		OwnerID:    ownerIDOf(b.OwnerID),
		AcquiredAt: time.Unix(b.AcquiredAt, 0).UTC(),
		QtyIn:      b.QtyIn, QtyConsumed: b.QtyConsumed,
		CostTotal:      money.IDR(b.CostTotalIdr),
		FakturReceived: b.FakturReceived == 1,
	}
}

// ownerIDOf maps a nullable owner column onto the domain's owner type. NULL is
// the company bucket, which is a real attribution and not a missing value
// (R2.2, SPEC §4.3).
func ownerIDOf(id *string) fifo.OwnerID {
	if id == nil {
		return fifo.Company
	}
	return fifo.OwnerID(*id)
}

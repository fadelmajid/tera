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
	"github.com/fadelmajid/tera/internal/domain/omzet"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

var (
	// ErrNoOpenSession is returned when a sale is rung with no till open.
	ErrNoOpenSession = errors.New("service: belum ada sesi kas yang dibuka")
	// ErrSessionClosed is returned when a closed session is acted on.
	ErrSessionClosed = errors.New("service: sesi kas sudah ditutup")
	// ErrVoidWindowClosed is returned when a sale is voided after its cash
	// session was counted. R12.3: after that a correction is a return.
	ErrVoidWindowClosed = errors.New("service: sesi kas sudah ditutup, gunakan retur bukan void")
	// ErrAlreadyVoid is returned when a voided sale is voided or returned again.
	ErrAlreadyVoid = errors.New("service: penjualan sudah dibatalkan")
	// ErrVoidAfterReturn is returned when a sale that has already had goods
	// come back is voided. R12.3 separates the two on whether the goods ever
	// left the shop, and a return is proof they did.
	ErrVoidAfterReturn = errors.New("service: penjualan sudah pernah diretur, gunakan retur bukan void")
)

// Sales rings transactions at the till. TASKS 2.2, 2.6-2.10.
//
// The whole point of this type is the transaction boundary. A sale writes its
// header, its lines, and one stock consumption per layer drawn, together or not
// at all (ARCHITECTURE §4). A partial commit corrupts stock and margin
// simultaneously: goods leave the shelf with no record of what they cost, or
// cost is recorded against goods that never left.
type Sales struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewSales builds the service.
func NewSales(db *store.DB, aud *Auditor, now func() time.Time) *Sales {
	if now == nil {
		now = time.Now
	}
	return &Sales{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- cash session (R9.8, TASKS 2.6) -----------------------------------------

// OpenSessionInput starts a till.
type OpenSessionInput struct {
	OpeningFloatIDR money.IDR
	BusinessDate    string
}

// OpenSession opens the till for the day.
func (s *Sales) OpenSession(ctx context.Context, actor Actor, in OpenSessionInput) (gen.CashSession, error) {
	if in.OpeningFloatIDR.IsNegative() {
		return gen.CashSession{}, fmt.Errorf("%w: modal awal tidak boleh negatif", ErrValidation)
	}

	var opened gen.CashSession
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, openedAt, err := clock.resolveDate(strings.TrimSpace(in.BusinessDate), s.now())
		if err != nil {
			return err
		}

		// The unique partial index enforces one open session per company; this
		// turns the constraint violation into a message a cashier can act on.
		if existing, err := q.GetOpenCashSession(ctx, actor.LegalEntityID); err == nil {
			return fmt.Errorf("%w: sesi kas %s masih terbuka, tutup dulu", ErrDuplicate, existing.BusinessDate)
		}

		row, err := q.OpenCashSession(ctx, gen.OpenCashSessionParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID, OpenedAt: openedAt.Unix(),
			BusinessDate: day, OpenedBy: nilIfEmpty(actor.UserID),
			OpeningFloatIdr: int64(in.OpeningFloatIDR), CreatedAt: s.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		opened = row

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "cash_session", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return opened, err
}

// SessionTotals is the Z-report (R9.8).
type SessionTotals struct {
	Session      gen.CashSession
	ByMethod     map[string]money.IDR
	CashRefunds  money.IDR
	ExpectedCash money.IDR
	TotalTakings money.IDR
	PaymentCount int64
}

// Totals reports what a session took in, per payment method.
//
// Expected cash is the opening float plus cash taken minus cash refunded.
// Non-cash methods are recorded but never expected in the drawer (R9.10) --
// counting a QRIS payment as cash is how a till "goes missing" money that was
// never there.
func (s *Sales) Totals(ctx context.Context, entityID, sessionID string) (SessionTotals, error) {
	session, err := s.q.GetCashSession(ctx, sessionID)
	if err != nil || session.EntityID != entityID {
		return SessionTotals{}, fmt.Errorf("%w: sesi kas tidak ditemukan", ErrNotFound)
	}
	return s.totals(ctx, s.q, session)
}

func (s *Sales) totals(ctx context.Context, q *gen.Queries, session gen.CashSession) (SessionTotals, error) {
	rows, err := q.SumSessionPayments(ctx, &session.ID)
	if err != nil {
		return SessionTotals{}, fmt.Errorf("service: session payments: %w", err)
	}
	refunds, err := q.SumSessionCashRefunds(ctx, &session.ID)
	if err != nil {
		return SessionTotals{}, fmt.Errorf("service: session refunds: %w", err)
	}

	out := SessionTotals{
		Session:     session,
		ByMethod:    make(map[string]money.IDR, len(rows)),
		CashRefunds: money.IDR(refunds),
	}
	for _, r := range rows {
		amount := money.IDR(r.AmountIdr)
		out.ByMethod[r.Method] = amount
		out.TotalTakings = out.TotalTakings.Add(amount)
		out.PaymentCount += r.PaymentCount
	}
	out.ExpectedCash = money.IDR(session.OpeningFloatIdr).
		Add(out.ByMethod["TUNAI"]).
		Sub(out.CashRefunds)
	return out, nil
}

// CloseSession counts the till and closes it.
//
// Closing is also what shuts the void window: after this, a mistake is
// corrected by a return rather than by pretending the sale never happened
// (R12.3).
func (s *Sales) CloseSession(ctx context.Context, actor Actor, sessionID string, counted money.IDR, note string) (SessionTotals, error) {
	if counted.IsNegative() {
		return SessionTotals{}, fmt.Errorf("%w: uang terhitung tidak boleh negatif", ErrValidation)
	}

	var out SessionTotals
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		session, err := q.GetCashSession(ctx, sessionID)
		if err != nil || session.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: sesi kas tidak ditemukan", ErrNotFound)
		}
		if session.Status == "CLOSED" {
			return ErrSessionClosed
		}

		totals, err := s.totals(ctx, q, session)
		if err != nil {
			return err
		}
		variance := counted.Sub(totals.ExpectedCash)

		closedAt := s.now().Unix()
		expected, count, vari := int64(totals.ExpectedCash), int64(counted), int64(variance)
		closed, err := q.CloseCashSession(ctx, gen.CloseCashSessionParams{
			ID: sessionID, ClosedAt: &closedAt, ClosedBy: nilIfEmpty(actor.UserID),
			CountedCashIdr: &count, ExpectedCashIdr: &expected, VarianceIdr: &vari,
			CloseNote: nilIfEmpty(note),
		})
		if err != nil {
			return wrapWrite(err)
		}
		totals.Session = closed
		out = totals

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "cash_session", RecordID: sessionID,
			Action: ActionUpdate, Before: session, After: closed,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return out, err
}

// OpenSessionFor returns the till currently open, if any.
func (s *Sales) OpenSessionFor(ctx context.Context, entityID string) (gen.CashSession, error) {
	row, err := s.q.GetOpenCashSession(ctx, entityID)
	if err != nil {
		return gen.CashSession{}, ErrNoOpenSession
	}
	return row, nil
}

// ListSessions returns the till history, newest first.
func (s *Sales) ListSessions(ctx context.Context, entityID string) ([]gen.CashSession, error) {
	rows, err := s.q.ListCashSessions(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list sessions: %w", err)
	}
	return rows, nil
}

// --- the sale (TASKS 2.2) ---------------------------------------------------

// SaleLineInput is one item in the cart.
type SaleLineInput struct {
	ProductID string
	Qty       int64
	// UnitPriceIDR overrides the catalogue price when non-nil. The price on the
	// day is a fact about the sale, snapshotted onto the line, not a lookup
	// that changes when the catalogue does.
	UnitPriceIDR *money.IDR
	// LineDiscountIDR is whole rupiah. Percentages are resolved in the UI and
	// never stored -- a stored percentage has to be re-multiplied to be read,
	// and that is a second place for the total to stop matching its parts.
	LineDiscountIDR money.IDR
}

// PaymentInputLine is one tender against the sale (R9.10).
type PaymentInputLine struct {
	Method    string
	AmountIDR money.IDR
	Reference string
}

// SaleInput is a cart ready to be rung.
type SaleInput struct {
	CustomerID string
	SaleDate   string
	// InvoiceDiscountIDR is allocated across the lines so the parts still sum
	// to the whole (SPEC §2.2's property).
	InvoiceDiscountIDR money.IDR
	// FakturIssued is independent of whether PPN is owed. A PKP owes output
	// PPN whether or not the buyer took a faktur (SPEC §2.3), and letting these
	// two collapse into one another is the most common way a newly-PKP
	// business loses margin silently.
	FakturIssued bool
	FakturNo     string
	IsCredit     bool
	DueDate      string
	Note         string
	Lines        []SaleLineInput
	Payments     []PaymentInputLine
}

// SaleResult is everything one sale wrote.
type SaleResult struct {
	Sale     gen.Sale
	Lines    []gen.SaleLine
	Payments []gen.SalePayment
	// TaxSnapshot is the rule as it stood, copied onto each line (INV-3). Empty
	// when no rule applied, which is every sale at the non-PKP entity.
	TaxSnapshot  []gen.SaleTax
	Consumptions int
	COGS         money.IDR
	// DPP, PPN and Total are the sale's three money figures, and DPP + PPN is
	// Total exactly (SPEC §2.2).
	DPP   money.IDR
	PPN   money.IDR
	Total money.IDR
	// AccruedWithoutFaktur is a PKP sale owing output PPN with no faktur issued
	// (SPEC §2.3). An ordinary walk-in looks like this; it is surfaced because
	// nothing else about the sale will ever mention the liability.
	AccruedWithoutFaktur bool
	// Margin is revenue less cost, where revenue is the DPP.
	Margin     money.IDR
	Receivable *gen.Receivable
}

func (in *SaleInput) normalise() error {
	in.CustomerID = strings.TrimSpace(in.CustomerID)
	in.SaleDate = strings.TrimSpace(in.SaleDate)
	in.FakturNo = strings.TrimSpace(in.FakturNo)
	in.DueDate = strings.TrimSpace(in.DueDate)

	switch {
	case len(in.Lines) == 0:
		return fmt.Errorf("%w: keranjang masih kosong", ErrValidation)
	case in.FakturNo != "" && !in.FakturIssued:
		return fmt.Errorf("%w: nomor faktur diisi tetapi faktur ditandai tidak diterbitkan", ErrValidation)
	case in.InvoiceDiscountIDR.IsNegative():
		return fmt.Errorf("%w: diskon tidak boleh negatif", ErrValidation)
	case in.IsCredit && in.CustomerID == "":
		// A debt owed by nobody cannot be chased (R5.6).
		return fmt.Errorf("%w: penjualan kredit harus memilih pelanggan", ErrValidation)
	case !in.IsCredit && in.DueDate != "":
		return fmt.Errorf("%w: jatuh tempo hanya berlaku untuk penjualan kredit", ErrValidation)
	}

	for i := range in.Lines {
		l := &in.Lines[i]
		l.ProductID = strings.TrimSpace(l.ProductID)
		switch {
		case l.ProductID == "":
			return fmt.Errorf("%w: baris %d belum memilih produk", ErrValidation, i+1)
		case l.Qty <= 0:
			return fmt.Errorf("%w: baris %d jumlah harus lebih dari nol", ErrValidation, i+1)
		case l.UnitPriceIDR != nil && l.UnitPriceIDR.IsNegative():
			return fmt.Errorf("%w: baris %d harga tidak boleh negatif", ErrValidation, i+1)
		case l.LineDiscountIDR.IsNegative():
			return fmt.Errorf("%w: baris %d diskon tidak boleh negatif", ErrValidation, i+1)
		}
	}

	for i := range in.Payments {
		p := &in.Payments[i]
		p.Method = strings.ToUpper(strings.TrimSpace(p.Method))
		if p.Method == "" {
			p.Method = "TUNAI"
		}
		if !p.AmountIDR.IsPositive() {
			return fmt.Errorf("%w: pembayaran %d harus lebih dari nol", ErrValidation, i+1)
		}
	}
	return nil
}

// Ring records a sale and consumes the stock it sold, in one transaction.
//
// This is TASKS 2.2 and the heart of the system. The order matters:
//
//  1. Resolve each line's product, price and owner, and price the cart.
//  2. Allocate any invoice-level discount across the lines so the parts still
//     sum to the whole.
//  3. For each line, draw FIFO layers through the domain -- owner-scoped, so a
//     sale of Budi's product never touches Sari's stock (INV-8).
//  4. Write the sale, its lines, its payments, and every consumption.
//
// All of it commits together or none of it does. A partial commit is the worst
// outcome available here: stock leaves the shelf with no record of what it
// cost, or cost is recorded against goods that never moved, and either way the
// margin report the family settles money on is quietly wrong.
//
// Running short is an error, not a partial sale. The cashier is told which
// owner is short and by how much, and nothing is written.
func (s *Sales) Ring(ctx context.Context, actor Actor, in SaleInput) (SaleResult, error) {
	if err := in.normalise(); err != nil {
		return SaleResult{}, err
	}

	var out SaleResult
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, occurredAt, err := clock.resolveDate(in.SaleDate, s.now())
		if err != nil {
			return err
		}
		if in.DueDate != "" {
			if _, err := parseBusinessDate(in.DueDate, clock.loc); err != nil {
				return err
			}
		}

		// A sale belongs to a till. Without one there is nothing to reconcile
		// at the end of the day and no window in which a void makes sense.
		session, err := q.GetOpenCashSession(ctx, actor.LegalEntityID)
		if err != nil {
			return ErrNoOpenSession
		}

		priced, err := s.priceCart(ctx, q, in)
		if err != nil {
			return err
		}

		// Tax, against the rules in force on the day (TASKS 5.3). Priced before
		// anything is written and before any stock is drawn, so a company with no
		// rule in force refuses the sale rather than half-ringing it.
		//
		// clock.isPKP is passed explicitly. A PKP owes output PPN on every taxable
		// delivery whether or not the buyer takes a faktur (SPEC §2.3), and
		// in.FakturIssued reaches none of the arithmetic -- domain/tax is not
		// given it.
		taxed, err := priceTax(ctx, q, actor.LegalEntityID, clock.isPKP, day, in.FakturIssued, priced)
		if err != nil {
			return err
		}

		now := s.now().Unix()
		saleID := store.NewID()

		invoiceNo, err := s.nextInvoiceNo(ctx, q, actor.LegalEntityID, day)
		if err != nil {
			return err
		}

		// Draw the stock first: if any line is short, nothing has been written
		// yet and the rollback has nothing to undo.
		type drawnLine struct {
			priced pricedSaleLine
			result fifo.Result
		}
		drawn := make([]drawnLine, 0, len(priced.lines))
		var totalCOGS money.IDR

		// What this cart has already taken off each layer, keyed by layer id.
		//
		// Nothing is written until every line has been drawn, so the balances
		// read from the database do not move as the loop runs. Two lines of the
		// same product -- an item scanned once and then again at a different
		// price -- would otherwise both see the full remainder and both draw it,
		// selling six units of a stock of five and costing them against a layer
		// that never held them (INV-7). The running total is what makes each
		// line see the shelf the previous line left behind.
		takenSoFar := make(map[string]int64, len(priced.lines))

		for i, pl := range priced.lines {
			balances, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
				EntityID: actor.LegalEntityID, ProductID: pl.productID,
			})
			if err != nil {
				return fmt.Errorf("service: layers for consumption: %w", err)
			}
			layers := make([]fifo.Layer, 0, len(balances))
			for _, b := range balances {
				layer := toFifoLayer(b)
				layer.QtyConsumed += takenSoFar[layer.ID]
				layers = append(layers, layer)
			}

			result, err := fifo.Consume(fifo.Request{
				EntityID: actor.LegalEntityID, ProductID: pl.productID,
				OwnerID: ownerIDOf(pl.ownerID), Qty: pl.qty,
				MovementID: saleID, OccurredAt: occurredAt,
			}, layers)
			if err != nil {
				var short *fifo.InsufficientStockError
				if errors.As(err, &short) {
					// Never a silent fallback to another owner's stock. The
					// message names whose stock is short and says what else is
					// on the shelf, so "out of stock" in front of a full shelf
					// is an answer rather than a bug report.
					if short.OtherOwnersAvailable > 0 {
						return fmt.Errorf(
							"%w: %s baris %d: stok %s tinggal %d, diminta %d (%d unit lagi ada tetapi milik pemilik lain dan tidak boleh dipakai)",
							ErrInsufficientStock, pl.productName, i+1, ownerLabel(pl.ownerName),
							short.Available, short.Requested, short.OtherOwnersAvailable)
					}
					return fmt.Errorf("%w: %s baris %d: stok %s tinggal %d, diminta %d",
						ErrInsufficientStock, pl.productName, i+1, ownerLabel(pl.ownerName),
						short.Available, short.Requested)
				}
				return err
			}

			for _, c := range result.Consumptions {
				takenSoFar[c.LayerID] += c.QtyOut
			}
			drawn = append(drawn, drawnLine{priced: pl, result: result})
			totalCOGS = totalCOGS.Add(result.COGS)
		}

		sale, err := q.CreateSale(ctx, gen.CreateSaleParams{
			ID: saleID, EntityID: actor.LegalEntityID, CashSessionID: &session.ID,
			CustomerID: nilIfEmpty(in.CustomerID), InvoiceNo: invoiceNo,
			OccurredAt: occurredAt.Unix(), BusinessDate: day,
			FakturIssued: boolToInt(in.FakturIssued), FakturNo: nilIfEmpty(in.FakturNo),
			GrossIdr: int64(priced.gross), DiscountIdr: int64(priced.discount),
			// DPP + PPN = total, enforced by the table (SPEC §2.2). Under
			// inclusive pricing the total is unchanged and the tax is inside
			// it; under exclusive the tax is added, so the customer pays more
			// than the sum of the labels. ppn_inclusive is snapshotted so a
			// receipt reprinted next year breaks down the same way (INV-3).
			DppIdr: int64(taxed.DPP), PpnIdr: int64(taxed.Tax),
			PpnInclusive: boolToInt(taxed.Rule.Inclusive),
			TotalIdr:     int64(taxed.GrandTotal), CogsIdr: int64(totalCOGS),
			IsCredit: boolToInt(in.IsCredit), DueDate: nilIfEmpty(in.DueDate),
			Note: nilIfEmpty(in.Note), CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Sale = sale

		for i, d := range drawn {
			lineTax := taxed.Lines[i]
			line, err := q.CreateSaleLine(ctx, gen.CreateSaleLineParams{
				ID: store.NewID(), SaleID: saleID, ProductID: d.priced.productID,
				OwnerID: d.priced.ownerID, Qty: d.priced.qty,
				UnitPriceIdr: int64(d.priced.unitPrice), GrossIdr: int64(d.priced.gross),
				LineDiscountIdr:  int64(d.priced.lineDiscount),
				AllocDiscountIdr: int64(d.priced.allocDiscount),
				NetIdr:           int64(d.priced.net),
				// Revenue for the margin report is the DPP, never the net: COGS
				// is already net of creditable PPN (SPEC §3.2), so revenue has to
				// be net of it too or the subtraction compares unlike things.
				DppIdr: int64(lineTax.DPP), PpnIdr: int64(lineTax.Tax),
				CogsIdr: int64(d.result.COGS), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Lines = append(out.Lines, line)

			// INV-3: the rule is copied onto the sale, not referenced. Changing a
			// rate next year must not move a figure already settled on.
			if taxed.Rule.ID != "" {
				ruleID := taxed.Rule.ID
				snapshot, err := q.CreateSaleTax(ctx, gen.CreateSaleTaxParams{
					ID: store.NewID(), SaleID: saleID, SaleLineID: line.ID,
					TaxType: string(taxed.Rule.Type), RateBp: taxed.Rule.RateBP,
					DppFactorNum: taxed.Rule.DPPNum, DppFactorDen: taxed.Rule.DPPDen,
					IsInclusive:      boolToInt(taxed.Rule.Inclusive),
					CalculationLevel: string(taxed.Rule.Level),
					RoundingMode:     string(taxed.Rule.Rounding),
					RoundingUnit:     taxed.Rule.RoundingUnit,
					LegalRef:         taxed.Rule.LegalRef,
					TaxRuleID:        &ruleID,
					IsExempt:         boolToInt(!lineTax.Taxed),
					DppIdr:           int64(lineTax.DPP),
					PpnIdr:           int64(lineTax.Tax),
					CreatedAt:        now,
				})
				if err != nil {
					return wrapWrite(err)
				}
				out.TaxSnapshot = append(out.TaxSnapshot, snapshot)
			}

			for _, c := range d.result.Consumptions {
				if _, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
					ID: store.NewID(), LayerID: c.LayerID, MovementID: saleID,
					MovementType: "SALE", QtyOut: c.QtyOut, CostIdr: int64(c.Cost),
					OccurredAt: occurredAt.Unix(), BusinessDate: day, CreatedAt: now,
				}); err != nil {
					return wrapWrite(err)
				}
				out.Consumptions++
			}
		}

		payments := in.Payments
		if len(payments) == 0 && !in.IsCredit {
			// The taxed total, not the sum of the labels: the same figure under
			// inclusive pricing, larger by the PPN under exclusive.
			payments = []PaymentInputLine{{Method: "TUNAI", AmountIDR: taxed.GrandTotal}}
		}
		for _, p := range payments {
			row, err := q.CreateSalePayment(ctx, gen.CreateSalePaymentParams{
				ID: store.NewID(), SaleID: saleID, Method: p.Method,
				AmountIdr: int64(p.AmountIDR), Reference: nilIfEmpty(p.Reference), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Payments = append(out.Payments, row)
		}

		// R9.11: credit sales raise piutang from the sales screen, not from a
		// separate invoice flow.
		if in.IsCredit {
			receivable, err := q.CreateReceivable(ctx, gen.CreateReceivableParams{
				ID: store.NewID(), EntityID: actor.LegalEntityID, CustomerID: in.CustomerID,
				Source: "SALE", SaleID: &saleID, InvoiceNo: &invoiceNo,
				AmountIdr: int64(taxed.GrandTotal), IncurredOn: day, DueDate: nilIfEmpty(in.DueDate),
				CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Receivable = &receivable
		}

		// TASKS 7.4: the turnover reaches the omzet clock in the same transaction
		// that reached the books. Turnover recorded in one and not the other would
		// leave the business measuring itself against the Rp 4,8 miliar threshold
		// on incomplete figures, and the error would surface a year later as a
		// registration deadline that was wrong all along.
		base, err := omzetBase(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := appendOmzet(ctx, tx, clock, actor.LegalEntityID, omzet.Sale, day,
			omzetAmountForSale(base, taxed.DPP, taxed.GrandTotal), saleID, actor, now); err != nil {
			return err
		}

		out.COGS = totalCOGS
		out.DPP = taxed.DPP
		out.PPN = taxed.Tax
		out.Total = taxed.GrandTotal
		out.AccruedWithoutFaktur = taxed.AccruedWithoutFaktur
		// Revenue less cost, where revenue is the DPP. PPN is collected on behalf
		// of the state and was never anybody's margin.
		out.Margin = taxed.DPP.Sub(totalCOGS)

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "sale", RecordID: saleID,
			Action: ActionCreate, After: sale, ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return SaleResult{}, err
	}
	return out, nil
}

type pricedSaleLine struct {
	productID     string
	productName   string
	ownerID       *string
	ownerName     *string
	qty           int64
	unitPrice     money.IDR
	gross         money.IDR
	lineDiscount  money.IDR
	allocDiscount money.IDR
	net           money.IDR
}

type pricedCart struct {
	lines    []pricedSaleLine
	gross    money.IDR
	discount money.IDR
	total    money.IDR
}

// priceCart resolves prices and owners and allocates the invoice discount.
//
// The invoice-level discount is spread across the lines by weight, using the
// same allocator the tax engine will use, so the line amounts still sum to the
// invoice total exactly. Integer division loses rupiah -- three ways on Rp 100
// gives 99 -- and a till whose lines do not add up to its total is a till the
// cashier stops trusting.
func (s *Sales) priceCart(ctx context.Context, q *gen.Queries, in SaleInput) (pricedCart, error) {
	out := pricedCart{lines: make([]pricedSaleLine, 0, len(in.Lines))}
	weights := make([]int64, 0, len(in.Lines))

	for i, l := range in.Lines {
		product, err := q.GetProduct(ctx, l.ProductID)
		if err != nil {
			return pricedCart{}, fmt.Errorf("%w: produk pada baris %d tidak ditemukan", ErrNotFound, i+1)
		}

		unitPrice := money.IDR(product.SalePriceIdr)
		if l.UnitPriceIDR != nil {
			unitPrice = *l.UnitPriceIDR
		}
		gross := unitPrice.MulQty(l.Qty)
		if l.LineDiscountIDR > gross {
			return pricedCart{}, fmt.Errorf("%w: diskon baris %d melebihi nilai barisnya", ErrValidation, i+1)
		}
		afterLine := gross.Sub(l.LineDiscountIDR)

		var ownerName *string
		if product.OwnerID != nil {
			owner, err := q.GetOwner(ctx, *product.OwnerID)
			if err == nil {
				ownerName = &owner.Name
			}
		}

		out.lines = append(out.lines, pricedSaleLine{
			productID: product.ID, productName: product.Name,
			// Snapshotted, like the purchase side: which owner's margin this
			// lands in is decided now, not when the report runs (INV-8).
			ownerID: product.OwnerID, ownerName: ownerName,
			qty: l.Qty, unitPrice: unitPrice, gross: gross,
			lineDiscount: l.LineDiscountIDR, net: afterLine,
		})
		out.gross = out.gross.Add(gross)
		out.discount = out.discount.Add(l.LineDiscountIDR)
		weights = append(weights, int64(afterLine))
	}

	if in.InvoiceDiscountIDR.IsPositive() {
		subtotal := out.gross.Sub(out.discount)
		if in.InvoiceDiscountIDR > subtotal {
			return pricedCart{}, fmt.Errorf("%w: diskon nota melebihi total penjualan", ErrValidation)
		}
		shares, err := money.Allocate(in.InvoiceDiscountIDR, weights)
		if err != nil {
			// Every line already discounted to zero: nothing to weight against.
			return pricedCart{}, fmt.Errorf("%w: diskon nota tidak dapat dibagi ke baris", ErrValidation)
		}
		for i := range out.lines {
			out.lines[i].allocDiscount = shares[i]
			out.lines[i].net = out.lines[i].net.Sub(shares[i])
		}
		out.discount = out.discount.Add(in.InvoiceDiscountIDR)
	}

	out.total = out.gross.Sub(out.discount)
	return out, nil
}

// nextInvoiceNo builds a per-company, per-day sequential number.
//
// Shaped so a person can read it off a receipt and find the sale: the business
// date and a counter, not a UUID.
func (s *Sales) nextInvoiceNo(ctx context.Context, q *gen.Queries, entityID, day string) (string, error) {
	n, err := q.CountSalesOnDate(ctx, gen.CountSalesOnDateParams{EntityID: entityID, BusinessDate: day})
	if err != nil {
		return "", fmt.Errorf("service: count sales: %w", err)
	}
	return fmt.Sprintf("%s-%04d", strings.ReplaceAll(day, "-", ""), n+1), nil
}

// prorate takes one slice out of a whole by the prefix-difference method: what
// the first already+qty units are worth, less what the first already units were
// worth.
//
// Returning a line in pieces then adds back up to exactly what it took in, with
// no rupiah left over on the last piece. Multiplying a rounded per-unit figure
// would lose one on most draws, which over a month of partial returns is a
// discrepancy nobody can trace to a cause.
func prorate(whole money.IDR, already, qty, total int64) money.IDR {
	return whole.MulRatio(already+qty, total).Sub(whole.MulRatio(already, total))
}

func ownerLabel(name *string) string {
	if name == nil {
		return "perusahaan"
	}
	return *name
}

// --- reads ------------------------------------------------------------------

// GetSale returns one sale with its lines and payments.
func (s *Sales) GetSale(ctx context.Context, entityID, id string) (gen.Sale, []gen.ListSaleLinesRow, []gen.SalePayment, error) {
	sale, err := s.q.GetSale(ctx, id)
	if err != nil || sale.EntityID != entityID {
		return gen.Sale{}, nil, nil, fmt.Errorf("%w: penjualan tidak ditemukan", ErrNotFound)
	}
	lines, err := s.q.ListSaleLines(ctx, id)
	if err != nil {
		return gen.Sale{}, nil, nil, fmt.Errorf("service: sale lines: %w", err)
	}
	payments, err := s.q.ListSalePayments(ctx, id)
	if err != nil {
		return gen.Sale{}, nil, nil, fmt.Errorf("service: sale payments: %w", err)
	}
	return sale, lines, payments, nil
}

// ListSales returns sales in a date range, newest first.
func (s *Sales) ListSales(ctx context.Context, entityID, from, to string) ([]gen.Sale, error) {
	if from == "" {
		from = "0000-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	rows, err := s.q.ListSales(ctx, gen.ListSalesParams{EntityID: entityID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, fmt.Errorf("service: list sales: %w", err)
	}
	return rows, nil
}

// SaleDrillDown is SPEC §4.2's first level: which layers a sale drew from and
// what each cost. Every figure on the margin report must expand to this.
func (s *Sales) SaleDrillDown(ctx context.Context, entityID, saleID string) ([]gen.ListSaleConsumptionsRow, error) {
	sale, err := s.q.GetSale(ctx, saleID)
	if err != nil || sale.EntityID != entityID {
		return nil, fmt.Errorf("%w: penjualan tidak ditemukan", ErrNotFound)
	}
	rows, err := s.q.ListSaleConsumptions(ctx, saleID)
	if err != nil {
		return nil, fmt.Errorf("service: sale consumptions: %w", err)
	}
	return rows, nil
}

// --- void (TASKS 2.10, R12.3) -----------------------------------------------

// Void undoes a sale rung by mistake, while the till is still open.
//
// A void says the sale did not happen. It gives the stock back to the exact
// layers it came from, at the exact cost taken, so nothing about the day's
// margin remembers it. That is only honest while the goods never left the shop,
// which is why the window closes when the cash session is counted: after that
// the goods are with the customer, and getting them back is a return.
//
// For the same reason a sale that has already had goods returned cannot be
// voided at all, however open the till still is — the return is the proof they
// left.
func (s *Sales) Void(ctx context.Context, actor Actor, saleID, reason string) (gen.Sale, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return gen.Sale{}, fmt.Errorf("%w: alasan pembatalan wajib diisi", ErrReasonRequired)
	}

	var voided gen.Sale
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		sale, err := q.GetSale(ctx, saleID)
		if err != nil || sale.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: penjualan tidak ditemukan", ErrNotFound)
		}
		if sale.Status == "VOID" {
			return ErrAlreadyVoid
		}

		// A return is proof the goods left the shop and came back, which is
		// precisely what a void says never happened. The two corrections are
		// separated on that fact (R12.3), so they cannot both apply to one
		// sale.
		//
		// The damage if this is allowed is in the till, not the stock: giving
		// back "everything" is already careful to reverse only what a return
		// has not (D-010), so quantities stay right. But a void withdraws the
		// sale's takings from the session while the cash refund already paid
		// against it still stands, and the Z-report then tells whoever counts
		// the drawer that it is over by the whole sale. Money that is really
		// there gets recorded as a surplus, and the correction for that is
		// somebody guessing.
		//
		// The honest correction for a sale that has partly come back is to
		// return the rest.
		returns, err := q.ListSaleReturns(ctx, saleID)
		if err != nil {
			return fmt.Errorf("service: sale returns: %w", err)
		}
		if len(returns) > 0 {
			return fmt.Errorf("%w: sudah ada retur pada %s sebesar %s",
				ErrVoidAfterReturn, returns[0].BusinessDate, money.IDR(returns[0].RefundIdr))
		}

		// The window: open till only (R12.3, and the rule recorded in
		// migration 009).
		if sale.CashSessionID != nil {
			session, err := q.GetCashSession(ctx, *sale.CashSessionID)
			if err != nil {
				return fmt.Errorf("service: cash session: %w", err)
			}
			if session.Status == "CLOSED" {
				return ErrVoidWindowClosed
			}
		}

		now := s.now().Unix()
		if err := s.giveBackEverything(ctx, q, sale, now); err != nil {
			return err
		}

		// TASKS 7.5, SPEC §5.4. The compensating row carries the ORIGINAL sale's
		// business date, not today's. A December sale voided in January has to
		// come off December's counter: the threshold resets between the two book
		// years, so booking it today would leave the closed year overstated and
		// open the new one already short.
		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := reverseOmzetForSale(ctx, tx, clock, sale, omzet.Void,
			sale.BusinessDate, money.IDR(sale.TotalIdr), actor, now); err != nil {
			return err
		}

		row, err := q.VoidSale(ctx, gen.VoidSaleParams{
			ID: saleID, VoidedAt: &now, VoidedBy: nilIfEmpty(actor.UserID), VoidReason: &reason,
		})
		if err != nil {
			return wrapWrite(err)
		}
		voided = row

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "sale", RecordID: saleID,
			Action: ActionVoid, Before: sale, After: row, Reason: reason,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return voided, err
}

// giveBackEverything reverses every draw a sale made, in full.
//
// Used by Void. Each reversal is an appended consumption naming the draw it
// undoes (D-010) -- the layers are never edited (INV-7).
func (s *Sales) giveBackEverything(ctx context.Context, q *gen.Queries, sale gen.Sale, now int64) error {
	lines, err := q.ListSaleLines(ctx, sale.ID)
	if err != nil {
		return fmt.Errorf("service: sale lines: %w", err)
	}

	for _, line := range lines {
		draws, err := q.ListDrawsForMovement(ctx, gen.ListDrawsForMovementParams{
			MovementID: sale.ID, ProductID: line.ProductID,
		})
		if err != nil {
			return fmt.Errorf("service: draws: %w", err)
		}
		for _, d := range draws {
			remaining := d.QtyOut - d.QtyReversed
			if remaining <= 0 {
				continue
			}
			if _, err := s.reverseDraw(ctx, q, d, remaining, sale.ID, sale.OccurredAt, sale.BusinessDate, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// reverseDraw appends one reversal through the domain and stores it.
func (s *Sales) reverseDraw(
	ctx context.Context, q *gen.Queries, d gen.ListDrawsForMovementRow,
	qty int64, movementID string, occurredAt int64, day string, now int64,
) (money.IDR, error) {
	reversal, err := fifo.Reverse(
		fifo.ReverseRequest{Qty: qty, MovementID: movementID, OccurredAt: time.Unix(occurredAt, 0)},
		fifo.Draw{
			ID: d.ID, LayerID: d.LayerID, QtyOut: d.QtyOut,
			Cost: money.IDR(d.CostIdr), QtyReversed: d.QtyReversed,
		},
	)
	if err != nil {
		var over *fifo.OverReversalError
		if errors.As(err, &over) {
			return 0, fmt.Errorf("%w: hanya %d unit yang masih bisa dikembalikan", ErrValidation, over.Remaining())
		}
		return 0, err
	}

	reversesID := reversal.ReversesID
	if _, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: reversal.LayerID, MovementID: reversal.MovementID,
		MovementType: "SALES_RETURN", QtyOut: reversal.QtyOut, CostIdr: int64(reversal.Cost),
		OccurredAt: occurredAt, BusinessDate: day, ReversesID: &reversesID, CreatedAt: now,
	}); err != nil {
		return 0, wrapWrite(err)
	}
	return reversal.Cost, nil
}

// --- sales return (TASKS 2.9, R12.1, D-010) ---------------------------------

// SaleReturnLineInput brings part of one sold line back.
type SaleReturnLineInput struct {
	SaleLineID string
	Qty        int64
}

// SaleReturnInput is goods coming back from the customer.
type SaleReturnInput struct {
	SaleID string
	// ReturnDate is when the goods came back. The sale's own date is copied
	// onto the record alongside it, because which period a cross-boundary
	// return belongs to is still undecided (SPEC §4.4) and both dates have to
	// survive until it is answered.
	ReturnDate   string
	Reason       string
	RefundMethod string
	Lines        []SaleReturnLineInput
}

// SaleReturnResult is what the return wrote.
type SaleReturnResult struct {
	Return gen.SaleReturn
	Lines  []gen.SaleReturnLine
	// Refund and COGSReversed are both positive magnitudes: this much money
	// went back to the customer, and this much cost came off the books. The
	// signed records live on the stock_consumption rows, which carry negative
	// quantities and costs (D-010); a report reading these two would otherwise
	// have to remember which way each one points.
	Refund money.IDR
	// PPNReversed is the output PPN inside Refund. It nets against output PPN
	// in the position report (SPEC §2.4), and it comes off the refund before
	// the margin reversal: tax collected for the state was never margin.
	PPNReversed  money.IDR
	COGSReversed money.IDR
}

// CreateReturn takes goods back and reverses the margin exactly.
//
// D-010: the stock goes back to the layer it was drawn from, not to a new layer
// at today's cost. That is what makes the margin reverse exactly -- a new layer
// would reverse revenue in full while reversing cost at a different figure,
// quietly inventing margin in the one report family members settle money on. It
// also keeps owner attribution automatic: the original layer belongs to
// someone, so the reversal lands in that person's bucket without anyone
// deciding whose it is (INV-8).
//
// Draws are given back newest-first, so the layer history reads in the order
// things actually happened.
func (s *Sales) CreateReturn(ctx context.Context, actor Actor, in SaleReturnInput) (SaleReturnResult, error) {
	in.SaleID = strings.TrimSpace(in.SaleID)
	in.Reason = strings.TrimSpace(in.Reason)
	in.RefundMethod = strings.ToUpper(strings.TrimSpace(in.RefundMethod))
	if in.RefundMethod == "" {
		in.RefundMethod = "TUNAI"
	}
	switch {
	case in.SaleID == "":
		return SaleReturnResult{}, fmt.Errorf("%w: penjualan wajib dipilih", ErrValidation)
	case in.Reason == "":
		return SaleReturnResult{}, fmt.Errorf("%w: alasan retur wajib diisi", ErrReasonRequired)
	case len(in.Lines) == 0:
		return SaleReturnResult{}, fmt.Errorf("%w: retur harus punya minimal satu baris", ErrValidation)
	}

	var out SaleReturnResult
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, occurredAt, err := clock.resolveDate(in.ReturnDate, s.now())
		if err != nil {
			return err
		}

		sale, err := q.GetSale(ctx, in.SaleID)
		if err != nil || sale.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: penjualan tidak ditemukan", ErrNotFound)
		}
		if sale.Status == "VOID" {
			return fmt.Errorf("%w: tidak ada barang untuk diretur", ErrAlreadyVoid)
		}

		now := s.now().Unix()
		returnID := store.NewID()

		var totalRefund, totalPPN, totalCOGS money.IDR
		type pendingLine struct {
			line   gen.SaleLine
			qty    int64
			refund money.IDR
			ppn    money.IDR
			cogs   money.IDR
		}
		pending := make([]pendingLine, 0, len(in.Lines))

		for i, rl := range in.Lines {
			if rl.Qty <= 0 {
				return fmt.Errorf("%w: baris %d jumlah retur harus lebih dari nol", ErrValidation, i+1)
			}

			line, err := q.GetSaleLine(ctx, strings.TrimSpace(rl.SaleLineID))
			if err != nil || line.SaleID != in.SaleID {
				return fmt.Errorf("%w: baris penjualan %d tidak ditemukan", ErrNotFound, i+1)
			}

			already, err := q.SumReturnedForSaleLine(ctx, line.ID)
			if err != nil {
				return fmt.Errorf("service: sum returned: %w", err)
			}
			if already+rl.Qty > line.Qty {
				return fmt.Errorf("%w: baris %d meretur %d dari %d yang terjual (%d sudah diretur)",
					ErrValidation, i+1, rl.Qty, line.Qty, already)
			}

			// The refund is this slice of what the customer actually paid for
			// the line -- net of both discounts, plus the PPN they were charged
			// on it -- by the same prefix difference used for cost, so returning
			// a line in pieces refunds exactly what it took in.
			//
			// dpp + ppn rather than net: under inclusive pricing the PPN is
			// already inside the net, but under exclusive it was added on top,
			// and refunding the net alone would keep the customer's tax.
			paid := money.IDR(line.DppIdr + line.PpnIdr)
			refund := prorate(paid, already, rl.Qty, line.Qty)
			// The output PPN handed back with it, prorated from the snapshot on
			// the original sale rather than recomputed against today's rate
			// (INV-3). Zero on a sale rung before the tax engine landed.
			ppnBack := prorate(money.IDR(line.PpnIdr), already, rl.Qty, line.Qty)

			// Give the stock back, newest draw first.
			draws, err := q.ListDrawsForMovement(ctx, gen.ListDrawsForMovementParams{
				MovementID: in.SaleID, ProductID: line.ProductID,
			})
			if err != nil {
				return fmt.Errorf("service: draws: %w", err)
			}

			left := rl.Qty
			var lineCOGS money.IDR
			for _, d := range draws {
				if left == 0 {
					break
				}
				available := d.QtyOut - d.QtyReversed
				if available <= 0 {
					continue
				}
				take := min(available, left)

				cost, err := s.reverseDraw(ctx, q, d, take, returnID, occurredAt.Unix(), day, now)
				if err != nil {
					return err
				}
				lineCOGS = lineCOGS.Add(cost)
				left -= take
			}
			if left > 0 {
				return fmt.Errorf("%w: baris %d hanya %d unit yang masih bisa diretur",
					ErrValidation, i+1, rl.Qty-left)
			}

			pending = append(pending, pendingLine{
				line: line, qty: rl.Qty, refund: refund, ppn: ppnBack, cogs: lineCOGS.Neg(),
			})
			totalRefund = totalRefund.Add(refund)
			totalPPN = totalPPN.Add(ppnBack)
			totalCOGS = totalCOGS.Add(lineCOGS.Neg())
		}

		ret, err := q.CreateSaleReturn(ctx, gen.CreateSaleReturnParams{
			ID: returnID, EntityID: actor.LegalEntityID, SaleID: in.SaleID,
			OccurredAt: occurredAt.Unix(), BusinessDate: day,
			// Both dates, so SPEC §4.4 can be answered later without a
			// migration against money that has already been split.
			SaleBusinessDate: sale.BusinessDate,
			Reason:           in.Reason, RefundIdr: int64(totalRefund),
			CogsReversedIdr: int64(totalCOGS), PpnReversedIdr: int64(totalPPN),
			RefundMethod: in.RefundMethod,
			CreatedBy:    nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Return = ret
		out.Refund = totalRefund
		out.PPNReversed = totalPPN
		out.COGSReversed = totalCOGS

		// The turnover did happen and is being reduced now, so the reversal
		// carries the day the goods came back — not the sale's. That is the same
		// distinction D-012 draws for margin, and it is why a return and a void
		// are separate event types: one reduces a year that traded, the other
		// unwinds a sale that did not.
		//
		// Whether returns reduce peredaran bruto at all is the third open question
		// in testdata/worked_examples/omzet_unverified.json.
		if err := reverseOmzetForSale(ctx, tx, clock, sale, omzet.Return,
			day, totalRefund, actor, now); err != nil {
			return err
		}

		for _, p := range pending {
			row, err := q.CreateSaleReturnLine(ctx, gen.CreateSaleReturnLineParams{
				ID: store.NewID(), SaleReturnID: returnID, SaleLineID: p.line.ID,
				Qty: p.qty, RefundIdr: int64(p.refund), CogsReversedIdr: int64(p.cogs),
				PpnReversedIdr: int64(p.ppn), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Lines = append(out.Lines, row)
		}

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "sale", RecordID: in.SaleID,
			Action: ActionAdjust, Before: sale, After: ret, Reason: in.Reason,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return SaleReturnResult{}, err
	}
	return out, nil
}

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

var (
	// ErrSameEntity is returned when a transfer names one company twice.
	ErrSameEntity = errors.New("service: perusahaan asal dan tujuan tidak boleh sama")

	// ErrCreditLossNotAcknowledged is returned when a non-PKP -> PKP transfer
	// is submitted without the caller confirming they were shown what it costs.
	//
	// R4.5, and the reason this is an error rather than a warning header: the
	// input PPN credit on this stock is destroyed permanently at the moment of
	// commit, and afterwards there is nothing to act on. A toast that arrives
	// with the success message is not a warning, it is a receipt.
	ErrCreditLossNotAcknowledged = errors.New(
		"service: transfer dari non-PKP ke PKP menghapus kredit PPN masukan secara permanen dan harus dikonfirmasi lebih dulu")
)

// Transfers move stock between the two companies. TASKS 4.1–4.6, SPEC §3.4.
//
// The transaction boundary is the entire point. Olsera records the transaction
// and does not move the stock, which is why the user's quantities are drifting
// from reality today (REQUIREMENTS §2) — so here the source consumption and the
// destination layer commit together or neither does.
type Transfers struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewTransfers builds the service.
func NewTransfers(db *store.DB, aud *Auditor, now func() time.Time) *Transfers {
	if now == nil {
		now = time.Now
	}
	return &Transfers{db: db, q: gen.New(db), aud: aud, now: now}
}

// TransferLineInput is one product crossing the boundary.
type TransferLineInput struct {
	ProductID string
	Qty       int64
	// PPNIDR is the PPN charged on this line's delivery. Only a PKP sender may
	// charge it (SPEC §2.3). Supplied rather than computed: no rate is
	// hardcoded anywhere (INV-4) and the tax engine is a later phase, so this
	// works the same way the purchasing screen already does.
	PPNIDR money.IDR
}

// TransferInput is one movement. The source company is the actor's own; naming
// only the destination keeps a transfer from being written on behalf of a
// company the user is not currently working in (R13.4).
type TransferInput struct {
	ToEntityID   string
	TransferDate string

	FakturIssued bool
	FakturNo     string
	Note         string

	// AcknowledgeCreditLoss is R4.5's blocking confirmation, carried as data.
	// Required for a non-PKP -> PKP transfer and refused on any other, so it
	// cannot become a flag clients set once and forget.
	AcknowledgeCreditLoss bool

	Lines []TransferLineInput
}

// TransferResult is everything one transfer wrote.
type TransferResult struct {
	Transfer gen.Transfer
	Lines    []gen.TransferLine
	// Consumptions is how many source layers were drawn. The destination side
	// is one new layer per line.
	Consumptions int
	Cost         money.IDR
	// ForfeitedPPN is the input PPN destroyed by this movement (R4.5). Zero
	// except non-PKP -> PKP.
	ForfeitedPPN money.IDR
}

func (in *TransferInput) normalise() error {
	in.ToEntityID = strings.TrimSpace(in.ToEntityID)
	in.TransferDate = strings.TrimSpace(in.TransferDate)
	in.FakturNo = strings.TrimSpace(in.FakturNo)

	switch {
	case in.ToEntityID == "":
		return fmt.Errorf("%w: perusahaan tujuan wajib dipilih", ErrValidation)
	case len(in.Lines) == 0:
		return fmt.Errorf("%w: transfer harus punya minimal satu baris", ErrValidation)
	case in.FakturNo != "" && !in.FakturIssued:
		return fmt.Errorf("%w: nomor faktur diisi tetapi faktur ditandai tidak diterbitkan", ErrValidation)
	}

	for i := range in.Lines {
		l := &in.Lines[i]
		l.ProductID = strings.TrimSpace(l.ProductID)
		switch {
		case l.ProductID == "":
			return fmt.Errorf("%w: baris %d belum memilih produk", ErrValidation, i+1)
		case l.Qty <= 0:
			return fmt.Errorf("%w: baris %d jumlah harus lebih dari nol", ErrValidation, i+1)
		case l.PPNIDR.IsNegative():
			return fmt.Errorf("%w: baris %d PPN tidak boleh negatif", ErrValidation, i+1)
		}
	}
	return nil
}

// --- planning ---------------------------------------------------------------

// plannedLine is one line's consumption worked out but not yet written.
type plannedLine struct {
	in          TransferLineInput
	productName string
	productCode string
	ownerID     *string
	ownerName   *string
	result      fifo.Result
	// forfeited is the input PPN embedded in the layers this line would draw,
	// prorated to the slice taken.
	forfeited money.IDR
}

// plan works out what a transfer would consume, without writing anything.
//
// Shared by Preview and Create so the figure quoted in the warning is the
// figure that gets committed, computed by the same code rather than by two
// implementations that have to agree (R4.5).
func (s *Transfers) plan(
	ctx context.Context, q *gen.Queries, fromEntityID string, occurredAt time.Time,
	movementID string, in TransferInput,
) ([]plannedLine, error) {
	planned := make([]plannedLine, 0, len(in.Lines))

	// What this document has already taken off each layer. Nothing is written
	// until every line is planned, so two lines of the same product would
	// otherwise both see the full remainder and both draw it — the same trap
	// the till had (INV-7).
	takenSoFar := make(map[string]int64, len(in.Lines))

	for i, l := range in.Lines {
		product, err := q.GetProduct(ctx, l.ProductID)
		if err != nil {
			return nil, fmt.Errorf("%w: produk pada baris %d tidak ditemukan", ErrNotFound, i+1)
		}

		var ownerName *string
		if product.OwnerID != nil {
			if owner, err := q.GetOwner(ctx, *product.OwnerID); err == nil {
				ownerName = &owner.Name
			}
		}

		balances, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
			EntityID: fromEntityID, ProductID: l.ProductID,
		})
		if err != nil {
			return nil, fmt.Errorf("service: layers for consumption: %w", err)
		}
		layers := make([]fifo.Layer, 0, len(balances))
		for _, b := range balances {
			layer := toFifoLayer(b)
			layer.QtyConsumed += takenSoFar[layer.ID]
			layers = append(layers, layer)
		}

		result, err := fifo.Consume(fifo.Request{
			EntityID: fromEntityID, ProductID: l.ProductID,
			OwnerID: ownerIDOf(product.OwnerID), Qty: l.Qty,
			MovementID: movementID, OccurredAt: occurredAt,
		}, layers)
		if err != nil {
			var short *fifo.InsufficientStockError
			if errors.As(err, &short) {
				// Owner-scoped, exactly as at the till (INV-8). A transfer of
				// Budi's goods never quietly draws Sari's, and the message says
				// so rather than reading like a bug with a full shelf in view.
				if short.OtherOwnersAvailable > 0 {
					return nil, fmt.Errorf(
						"%w: %s baris %d: stok %s tinggal %d, diminta %d (%d unit lagi ada tetapi milik pemilik lain dan tidak boleh dipakai)",
						ErrInsufficientStock, product.Name, i+1, ownerLabel(ownerName),
						short.Available, short.Requested, short.OtherOwnersAvailable)
				}
				return nil, fmt.Errorf("%w: %s baris %d: stok %s tinggal %d, diminta %d",
					ErrInsufficientStock, product.Name, i+1, ownerLabel(ownerName),
					short.Available, short.Requested)
			}
			return nil, err
		}

		forfeited, err := s.forfeitedPPN(ctx, q, fromEntityID, l.ProductID, result)
		if err != nil {
			return nil, err
		}

		for _, c := range result.Consumptions {
			takenSoFar[c.LayerID] += c.QtyOut
		}
		planned = append(planned, plannedLine{
			in: l, productName: product.Name, productCode: product.Code,
			ownerID: product.OwnerID, ownerName: ownerName,
			result: result, forfeited: forfeited,
		})
	}
	return planned, nil
}

// forfeitedPPN is the input PPN already paid on the stock being moved, prorated
// to the slice each draw takes.
//
// This is what R4.5's warning quotes. It is the real figure rather than 11% of
// something: layers bought from a supplier who issued no faktur, or from a
// non-PKP supplier, carry no input PPN and forfeit nothing, and a warning that
// invented a number for those would be teaching the user to ignore it.
func (s *Transfers) forfeitedPPN(
	ctx context.Context, q *gen.Queries, entityID, productID string, result fifo.Result,
) (money.IDR, error) {
	if len(result.Consumptions) == 0 {
		return money.Zero, nil
	}

	rows, err := q.ListLayerPPNForProduct(ctx, gen.ListLayerPPNForProductParams{
		EntityID: entityID, ProductID: productID,
	})
	if err != nil {
		return 0, fmt.Errorf("service: layer ppn: %w", err)
	}
	type layerPPN struct {
		qtyIn int64
		ppn   money.IDR
	}
	byLayer := make(map[string]layerPPN, len(rows))
	for _, r := range rows {
		byLayer[r.ID] = layerPPN{qtyIn: r.QtyIn, ppn: money.IDR(r.PpnPaidIdr)}
	}

	var total money.IDR
	for _, c := range result.Consumptions {
		l, ok := byLayer[c.LayerID]
		if !ok || l.qtyIn == 0 || l.ppn.IsZero() {
			continue
		}
		total = total.Add(l.ppn.MulRatio(c.QtyOut, l.qtyIn))
	}
	return total, nil
}

// direction loads both companies and snapshots what decides the tax treatment.
func (s *Transfers) direction(
	ctx context.Context, tx *sql.Tx, fromEntityID, toEntityID string,
) (from, to entityClock, dir fifo.Direction, err error) {
	if fromEntityID == toEntityID {
		return from, to, dir, ErrSameEntity
	}
	if from, err = loadEntityClock(ctx, tx, fromEntityID); err != nil {
		return from, to, dir, err
	}
	if to, err = loadEntityClock(ctx, tx, toEntityID); err != nil {
		return from, to, dir, fmt.Errorf("%w: perusahaan tujuan tidak ditemukan", ErrNotFound)
	}
	return from, to, fifo.Direction{FromIsPKP: from.isPKP, ToIsPKP: to.isPKP}, nil
}

// --- preview (R4.5) ---------------------------------------------------------

// TransferPreviewLine is one line as it would be committed.
type TransferPreviewLine struct {
	ProductID    string
	ProductCode  string
	ProductName  string
	OwnerID      *string
	OwnerName    *string
	Qty          int64
	Cost         money.IDR
	PPN          money.IDR
	ForfeitedPPN money.IDR
	Layers       []fifo.Consumption
}

// TransferPreview is what a transfer would do, before it does it.
type TransferPreview struct {
	Direction fifo.Direction
	// DestroysInputCredit is R4.5. When true the client must show a blocking
	// confirmation and send AcknowledgeCreditLoss, or the write is refused.
	DestroysInputCredit bool
	TaxableDelivery     bool

	Cost         money.IDR
	PPN          money.IDR
	Amount       money.IDR
	ForfeitedPPN money.IDR

	Lines []TransferPreviewLine
}

// Preview works out what a transfer would move and what it would cost, writing
// nothing.
//
// It exists so R4.5's confirmation can quote a figure instead of a warning
// nobody reads: this many units, this much cost, and this much input PPN that
// no longer exists afterwards. A blocking dialog that cannot say what is at
// stake trains people to click through it.
func (s *Transfers) Preview(ctx context.Context, actor Actor, in TransferInput) (TransferPreview, error) {
	if err := in.normalise(); err != nil {
		return TransferPreview{}, err
	}

	var out TransferPreview
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		from, _, dir, err := s.direction(ctx, tx, actor.LegalEntityID, in.ToEntityID)
		if err != nil {
			return err
		}
		if err := checkDeliveryIsLegal(dir, in); err != nil {
			return err
		}

		_, occurredAt, err := from.resolveDate(in.TransferDate, s.now())
		if err != nil {
			return err
		}

		// A preview is a read, but it plans against live balances, so it runs
		// in the same transaction shape as the write. Nothing is written.
		planned, err := s.plan(ctx, q, actor.LegalEntityID, occurredAt, store.NewID(), in)
		if err != nil {
			return err
		}

		out.Direction = dir
		out.DestroysInputCredit = dir.DestroysInputCredit()
		out.TaxableDelivery = dir.TaxableDelivery()

		for _, p := range planned {
			line := TransferPreviewLine{
				ProductID: p.in.ProductID, ProductCode: p.productCode, ProductName: p.productName,
				OwnerID: p.ownerID, OwnerName: p.ownerName, Qty: p.in.Qty,
				Cost: p.result.COGS, PPN: p.in.PPNIDR, Layers: p.result.Consumptions,
			}
			if dir.DestroysInputCredit() {
				line.ForfeitedPPN = p.forfeited
			}
			out.Lines = append(out.Lines, line)
			out.Cost = out.Cost.Add(line.Cost)
			out.PPN = out.PPN.Add(line.PPN)
			out.ForfeitedPPN = out.ForfeitedPPN.Add(line.ForfeitedPPN)
		}
		out.Amount = out.Cost.Add(out.PPN)
		return nil
	})
	if err != nil {
		return TransferPreview{}, err
	}
	return out, nil
}

// checkDeliveryIsLegal enforces SPEC §2.3 before anything else looks at money.
func checkDeliveryIsLegal(dir fifo.Direction, in TransferInput) error {
	if dir.CanIssueFaktur() {
		return nil
	}
	if in.FakturIssued {
		return fmt.Errorf("%w: perusahaan non-PKP tidak dapat menerbitkan faktur pajak", ErrValidation)
	}
	for i, l := range in.Lines {
		if l.PPNIDR.IsPositive() {
			return fmt.Errorf("%w: baris %d: perusahaan non-PKP tidak dapat memungut PPN", ErrValidation, i+1)
		}
	}
	return nil
}

// --- create -----------------------------------------------------------------

// Create moves stock across the company boundary, atomically.
//
// TASKS 4.2, SPEC §3.4. The order matters and the boundary matters more:
//
//  1. Resolve both companies and snapshot the direction.
//  2. Refuse a non-PKP -> PKP transfer that has not been acknowledged (R4.5).
//  3. Consume the source layers through the domain — owner-scoped, so a
//     transfer of Budi's goods never draws Sari's (INV-8).
//  4. Create one layer per line in the destination, carrying the owner across
//     and setting the cost basis from the direction (SPEC §3.2, §3.4).
//  5. Write the transfer and its lines.
//
// All of it commits together or none of it does. A partial commit here is the
// exact bug being replaced: stock leaves one company and never arrives in the
// other, or arrives without having left, and the quantities drift from reality
// with nothing in the system saying so.
func (s *Transfers) Create(ctx context.Context, actor Actor, in TransferInput) (TransferResult, error) {
	if err := in.normalise(); err != nil {
		return TransferResult{}, err
	}

	var out TransferResult
	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		from, to, dir, err := s.direction(ctx, tx, actor.LegalEntityID, in.ToEntityID)
		if err != nil {
			return err
		}
		if err := checkDeliveryIsLegal(dir, in); err != nil {
			return err
		}

		// R4.5, before anything is written. Afterwards the credit is gone and
		// there is nothing to act on, which is why this is a precondition and
		// not a message attached to the result.
		if dir.DestroysInputCredit() && !in.AcknowledgeCreditLoss {
			return ErrCreditLossNotAcknowledged
		}
		if !dir.DestroysInputCredit() && in.AcknowledgeCreditLoss {
			// Refused rather than ignored: a client that sets this flag on
			// every transfer has stopped asking the question, and the next
			// non-PKP -> PKP movement would go through silently.
			return fmt.Errorf("%w: transfer %s tidak menghapus kredit PPN, konfirmasi tidak berlaku",
				ErrValidation, dir)
		}

		day, occurredAt, err := from.resolveDate(in.TransferDate, s.now())
		if err != nil {
			return err
		}
		// The receiving company counts it on its own day (D-005, INV-5). The
		// same zone today; the column exists for the day it is not.
		toDay := businessDate(occurredAt, to.loc)

		now := s.now().Unix()
		transferID := store.NewID()

		transferNo, err := s.nextTransferNo(ctx, q, actor.LegalEntityID, day)
		if err != nil {
			return err
		}

		planned, err := s.plan(ctx, q, actor.LegalEntityID, occurredAt, transferID, in)
		if err != nil {
			return err
		}

		var totalCost, totalPPN, totalForfeited money.IDR
		for _, p := range planned {
			totalCost = totalCost.Add(p.result.COGS)
			totalPPN = totalPPN.Add(p.in.PPNIDR)
			if dir.DestroysInputCredit() {
				totalForfeited = totalForfeited.Add(p.forfeited)
			}
		}

		transfer, err := q.CreateTransfer(ctx, gen.CreateTransferParams{
			ID: transferID, FromEntityID: actor.LegalEntityID, ToEntityID: in.ToEntityID,
			TransferNo: transferNo, OccurredAt: occurredAt.Unix(),
			BusinessDate: day, ToBusinessDate: toDay,
			FromIsPkp: boolToInt(dir.FromIsPKP), ToIsPkp: boolToInt(dir.ToIsPKP),
			CreditLossAck:   boolToInt(dir.DestroysInputCredit()),
			ForfeitedPpnIdr: int64(totalForfeited),
			CostTotalIdr:    int64(totalCost), PpnIdr: int64(totalPPN),
			AmountIdr:    int64(totalCost.Add(totalPPN)),
			FakturIssued: boolToInt(in.FakturIssued), FakturNo: nilIfEmpty(in.FakturNo),
			Note: nilIfEmpty(in.Note), CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		out.Transfer = transfer

		for _, p := range planned {
			// The source side: one consumption per layer drawn, appended,
			// never an edit to the layer (INV-7).
			for _, c := range p.result.Consumptions {
				if _, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
					ID: store.NewID(), LayerID: c.LayerID, MovementID: transferID,
					MovementType: "TRANSFER_OUT", QtyOut: c.QtyOut, CostIdr: int64(c.Cost),
					OccurredAt: occurredAt.Unix(), BusinessDate: day, CreatedAt: now,
				}); err != nil {
					return wrapWrite(err)
				}
				out.Consumptions++
			}

			// The destination side: one layer, at the cost the source gave up,
			// with the owner carried across unchanged (SPEC §3.4, INV-8).
			basis, err := fifo.Transfer{
				Direction: dir, Cost: p.result.COGS, PPN: p.in.PPNIDR,
				FakturIssued: in.FakturIssued, ForfeitedInputPPN: p.forfeited,
			}.DestinationBasis()
			if err != nil {
				return fmt.Errorf("%w: %w", ErrValidation, err)
			}

			layer, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
				ID: store.NewID(), EntityID: in.ToEntityID, ProductID: p.in.ProductID,
				// The family member owning the goods does not change because
				// the goods crossed a company line.
				OwnerID:    p.ownerID,
				AcquiredAt: occurredAt.Unix(), BusinessDate: toDay,
				Source: "TRANSFER_IN", SourceDocID: &transferID,
				QtyIn: p.in.Qty, CostTotalIdr: int64(basis.CostTotal),
				FakturReceived: boolToInt(in.FakturIssued && dir.ToIsPKP),
				PpnPaidIdr:     int64(basis.PPNPaid), CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}

			forfeited := money.Zero
			if dir.DestroysInputCredit() {
				forfeited = p.forfeited
			}
			line, err := q.CreateTransferLine(ctx, gen.CreateTransferLineParams{
				ID: store.NewID(), TransferID: transferID, ProductID: p.in.ProductID,
				OwnerID: p.ownerID, Qty: p.in.Qty,
				CostTotalIdr: int64(p.result.COGS), PpnIdr: int64(p.in.PPNIDR),
				ForfeitedPpnIdr: int64(forfeited), DestLayerID: layer.ID, CreatedAt: now,
			})
			if err != nil {
				return wrapWrite(err)
			}
			out.Lines = append(out.Lines, line)
		}

		out.Cost = totalCost
		out.ForfeitedPPN = totalForfeited

		return s.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "transfer", RecordID: transferID,
			Action: ActionCreate, After: transfer, ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return TransferResult{}, err
	}
	return out, nil
}

// nextTransferNo builds a per-company, per-day sequential number, shaped so a
// person can read it off a delivery note and find the movement.
func (s *Transfers) nextTransferNo(ctx context.Context, q *gen.Queries, entityID, day string) (string, error) {
	n, err := q.CountTransfersOnDate(ctx, gen.CountTransfersOnDateParams{
		EntityID: entityID, BusinessDate: day,
	})
	if err != nil {
		return "", fmt.Errorf("service: count transfers: %w", err)
	}
	return fmt.Sprintf("TRF-%s-%04d", strings.ReplaceAll(day, "-", ""), n+1), nil
}

// --- reads ------------------------------------------------------------------

// List returns transfers touching a company, in either direction. A transfer is
// one document belonging to two companies.
func (s *Transfers) List(ctx context.Context, entityID string) ([]gen.ListTransfersRow, error) {
	rows, err := s.q.ListTransfers(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list transfers: %w", err)
	}
	return rows, nil
}

// Get returns one transfer with its lines, readable from either side.
func (s *Transfers) Get(ctx context.Context, entityID, id string) (gen.Transfer, []gen.ListTransferLinesRow, error) {
	transfer, err := s.q.GetTransfer(ctx, id)
	if err != nil {
		return gen.Transfer{}, nil, fmt.Errorf("%w: transfer tidak ditemukan", ErrNotFound)
	}
	if transfer.FromEntityID != entityID && transfer.ToEntityID != entityID {
		return gen.Transfer{}, nil, fmt.Errorf("%w: transfer tidak ditemukan", ErrNotFound)
	}
	lines, err := s.q.ListTransferLines(ctx, id)
	if err != nil {
		return gen.Transfer{}, nil, fmt.Errorf("service: transfer lines: %w", err)
	}
	return transfer, lines, nil
}

// CounterpartyPosition is what one company owes another, netted.
type CounterpartyPosition struct {
	CounterpartyID   string
	CounterpartyName string
	// Out is what this company has sent, In what it has received, both at cost
	// plus any PPN charged on the delivery.
	Out, In           money.IDR
	OutCount, InCount int64
	// Net is positive when the counterparty owes this company.
	Net money.IDR
}

// Position is D-014: what the two companies owe each other, at cost, netted.
//
// Deliberately not hutang or piutang. Those reports are about suppliers and
// customers (R5.5, R5.6), and putting a family's internal bookkeeping into a
// supplier aging report makes both harder to read. Neither company appears in
// the other's aging as a result.
func (s *Transfers) Position(ctx context.Context, entityID string) ([]CounterpartyPosition, error) {
	rows, err := s.q.InterCompanyPosition(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: inter-company position: %w", err)
	}

	byCounterparty := make(map[string]*CounterpartyPosition, len(rows))
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		p, ok := byCounterparty[r.CounterpartyID]
		if !ok {
			p = &CounterpartyPosition{CounterpartyID: r.CounterpartyID}
			byCounterparty[r.CounterpartyID] = p
			order = append(order, r.CounterpartyID)
		}
		if r.Leg == "OUT" {
			p.Out, p.OutCount = money.IDR(r.AmountIdr), r.TransferCount
		} else {
			p.In, p.InCount = money.IDR(r.AmountIdr), r.TransferCount
		}
	}

	out := make([]CounterpartyPosition, 0, len(order))
	for _, id := range order {
		p := byCounterparty[id]
		p.Net = p.Out.Sub(p.In)
		if entity, err := s.q.GetLegalEntity(ctx, id); err == nil {
			p.CounterpartyName = entity.Name
		}
		out = append(out, *p)
	}
	return out, nil
}

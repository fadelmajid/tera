package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// Opening carries the business's position at go-live into this system. R11.5,
// TASKS 1.11.
//
// The shop is switching systems mid-life, so day one is not day zero: there is
// stock on the shelves, money owed to suppliers, and money owed by customers,
// none of which has a document in this database. Those balances get typed in
// once, marked as what they are, and never confused with transactions that
// actually happened here.
//
// Every opening record is tagged OPENING rather than dressed up as a purchase
// or a sale nobody made. That distinction matters twice over: a synthetic
// purchase would pollute the purchases report and the PPN position (SPEC §2.4),
// and a synthetic sale would inflate the omzet clock against the Rp 4.8B
// threshold with turnover that belongs to a previous set of books.
type Opening struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewOpening builds the service.
func NewOpening(db *store.DB, aud *Auditor, now func() time.Time) *Opening {
	if now == nil {
		now = time.Now
	}
	return &Opening{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- opening stock ----------------------------------------------------------

// OpeningStockLine is one shelf position as it stands at go-live.
type OpeningStockLine struct {
	ProductID string
	// Empty is the company bucket (R2.2). Attribution is required thinking, not
	// a default: every unit carried in belongs to exactly one bucket (INV-8),
	// and getting it wrong at go-live means every margin figure afterwards
	// starts from the wrong place.
	OwnerID string
	Qty     int64
	// CostTotalIDR is what these units cost in total, not per unit. The layer
	// stores the total and derives per-unit (SPEC §1), and a business carrying
	// in 7 units that cost Rp 100.000 should not have to type Rp 14.285,71.
	CostTotalIDR money.IDR
	// FakturReceived is almost always false for carried-in stock and defaults
	// that way. Input PPN on goods bought under the old books was either
	// already claimed there or lost; claiming it again here would be claiming
	// the same rupiah twice (INV-9, SPEC §2.4). Set it only where the faktur
	// genuinely has not been used.
	FakturReceived bool
	PPNPaidIDR     money.IDR
	ExpiryDate     string
}

// OpeningStockInput is the go-live stock count.
type OpeningStockInput struct {
	// AsOfDate is 'YYYY-MM-DD' in the company's timezone: the day the business
	// started using this system. It becomes the layers' acquired_at, so
	// everything bought afterwards sorts behind it in FIFO order.
	AsOfDate string
	Note     string
	Lines    []OpeningStockLine
}

// OpeningStockResult is the layers that were carried in.
type OpeningStockResult struct {
	Layers    []gen.StockLayer
	TotalCost money.IDR
	TotalQty  int64
}

// CarryInStock creates the opening stock layers.
func (o *Opening) CarryInStock(ctx context.Context, actor Actor, in OpeningStockInput) (OpeningStockResult, error) {
	if len(in.Lines) == 0 {
		return OpeningStockResult{}, fmt.Errorf("%w: saldo awal stok harus punya minimal satu baris", ErrValidation)
	}
	for i, l := range in.Lines {
		switch {
		case strings.TrimSpace(l.ProductID) == "":
			return OpeningStockResult{}, fmt.Errorf("%w: baris %d belum memilih produk", ErrValidation, i+1)
		case l.Qty <= 0:
			return OpeningStockResult{}, fmt.Errorf("%w: baris %d jumlah harus lebih dari nol", ErrValidation, i+1)
		case l.CostTotalIDR.IsNegative():
			return OpeningStockResult{}, fmt.Errorf("%w: baris %d nilai persediaan tidak boleh negatif", ErrValidation, i+1)
		case l.PPNPaidIDR.IsNegative():
			return OpeningStockResult{}, fmt.Errorf("%w: baris %d PPN tidak boleh negatif", ErrValidation, i+1)
		}
	}

	var out OpeningStockResult
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		day, asOf, err := clock.resolveDate(strings.TrimSpace(in.AsOfDate), o.now())
		if err != nil {
			return err
		}

		now := o.now().Unix()
		batchID := store.NewID()

		for i, l := range in.Lines {
			if l.ExpiryDate != "" {
				if _, err := parseBusinessDate(l.ExpiryDate, clock.loc); err != nil {
					return err
				}
			}

			layer, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
				ID: store.NewID(), EntityID: actor.LegalEntityID, ProductID: strings.TrimSpace(l.ProductID),
				OwnerID: nilIfEmpty(strings.TrimSpace(l.OwnerID)),
				// Everything bought after go-live sorts behind these, which is
				// what makes the first months of FIFO agree with the shelf.
				AcquiredAt: asOf.Unix(), BusinessDate: day,
				Source: "OPENING", SourceDocID: &batchID,
				QtyIn: l.Qty, CostTotalIdr: int64(l.CostTotalIDR),
				FakturReceived: boolToInt(l.FakturReceived), PpnPaidIdr: int64(l.PPNPaidIDR),
				ExpiryDate: nilIfEmpty(strings.TrimSpace(l.ExpiryDate)), CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("baris %d: %w", i+1, wrapWrite(err))
			}
			out.Layers = append(out.Layers, layer)
			out.TotalCost = out.TotalCost.Add(l.CostTotalIDR)
			out.TotalQty += l.Qty
		}

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "opening_stock", RecordID: batchID,
			Action: ActionCreate, After: out.Layers, ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return OpeningStockResult{}, err
	}
	return out, nil
}

// --- opening hutang and piutang ---------------------------------------------

// OpeningDebtInput is one balance owed at go-live, in either direction.
type OpeningDebtInput struct {
	// PartyID is the supplier for hutang, the customer for piutang.
	PartyID    string
	InvoiceNo  string
	AmountIDR  money.IDR
	IncurredOn string
	DueDate    string
	Note       string
}

func (in *OpeningDebtInput) normalise(clock entityClock, fallback time.Time) error {
	in.PartyID = strings.TrimSpace(in.PartyID)
	in.InvoiceNo = strings.TrimSpace(in.InvoiceNo)
	in.IncurredOn = strings.TrimSpace(in.IncurredOn)
	in.DueDate = strings.TrimSpace(in.DueDate)

	switch {
	case in.PartyID == "":
		return fmt.Errorf("%w: pihak wajib dipilih", ErrValidation)
	case !in.AmountIDR.IsPositive():
		return fmt.Errorf("%w: jumlah harus lebih dari nol", ErrValidation)
	}

	day, _, err := clock.resolveDate(in.IncurredOn, fallback)
	if err != nil {
		return err
	}
	in.IncurredOn = day

	if in.DueDate != "" {
		if _, err := parseBusinessDate(in.DueDate, clock.loc); err != nil {
			return err
		}
	}
	return nil
}

// CarryInPayable records hutang outstanding at go-live (R5.5, R11.5).
func (o *Opening) CarryInPayable(ctx context.Context, actor Actor, in OpeningDebtInput) (gen.Payable, error) {
	var created gen.Payable
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := in.normalise(clock, o.now()); err != nil {
			return err
		}

		row, err := gen.New(tx).CreatePayable(ctx, gen.CreatePayableParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID, SupplierID: in.PartyID,
			// OPENING, not a synthetic purchase. A purchase that never happened
			// would appear in the purchases report and, worse, in the input PPN
			// position (SPEC §2.4).
			Source: "OPENING", PurchaseID: nil,
			InvoiceNo: nilIfEmpty(in.InvoiceNo), AmountIdr: int64(in.AmountIDR),
			IncurredOn: in.IncurredOn, DueDate: nilIfEmpty(in.DueDate),
			Note: nilIfEmpty(in.Note), CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "payable", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// CarryInReceivable records piutang outstanding at go-live (R5.6, R11.5).
func (o *Opening) CarryInReceivable(ctx context.Context, actor Actor, in OpeningDebtInput) (gen.Receivable, error) {
	var created gen.Receivable
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := in.normalise(clock, o.now()); err != nil {
			return err
		}

		row, err := gen.New(tx).CreateReceivable(ctx, gen.CreateReceivableParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID, CustomerID: in.PartyID,
			// OPENING, not a synthetic sale. A sale that never happened would
			// count towards the Rp 4.8B omzet threshold with turnover that
			// belongs to the previous books (SPEC §5).
			Source: "OPENING", SaleID: nil,
			InvoiceNo: nilIfEmpty(in.InvoiceNo), AmountIdr: int64(in.AmountIDR),
			IncurredOn: in.IncurredOn, DueDate: nilIfEmpty(in.DueDate),
			Note: nilIfEmpty(in.Note), CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "receivable", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// --- payments and reads -----------------------------------------------------

// PaymentInput is money moving against a hutang or piutang. R5.8.
type PaymentInput struct {
	AmountIDR money.IDR
	PaidOn    string
	Method    string
	Note      string
}

func (in *PaymentInput) normalise(clock entityClock, fallback time.Time) error {
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	if in.Method == "" {
		in.Method = "TUNAI"
	}
	if in.AmountIDR.IsZero() {
		return fmt.Errorf("%w: jumlah pembayaran tidak boleh nol", ErrValidation)
	}

	day, _, err := clock.resolveDate(strings.TrimSpace(in.PaidOn), fallback)
	if err != nil {
		return err
	}
	in.PaidOn = day
	return nil
}

// PayPayable records a payment against hutang. Partial payments are ordinary
// (R5.8); the outstanding balance is derived from these rows, never stored.
func (o *Opening) PayPayable(ctx context.Context, actor Actor, payableID string, in PaymentInput) (gen.PayablePayment, error) {
	var created gen.PayablePayment
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := in.normalise(clock, o.now()); err != nil {
			return err
		}

		balance, err := q.GetPayable(ctx, payableID)
		if err != nil || balance.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: hutang tidak ditemukan", ErrNotFound)
		}
		// Overpaying is a data-entry error, not a credit note. Refusing it here
		// keeps the aging report from showing a supplier a negative balance
		// nobody can explain.
		if in.AmountIDR.IsPositive() && int64(in.AmountIDR) > balance.OutstandingIdr {
			return fmt.Errorf("%w: pembayaran %s melebihi sisa hutang %s",
				ErrValidation, in.AmountIDR, money.IDR(balance.OutstandingIdr))
		}

		row, err := q.RecordPayablePayment(ctx, gen.RecordPayablePaymentParams{
			ID: store.NewID(), PayableID: payableID, AmountIdr: int64(in.AmountIDR),
			PaidOn: in.PaidOn, Method: in.Method, Note: nilIfEmpty(in.Note),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "payable_payment", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// PayReceivable records a payment against piutang.
func (o *Opening) PayReceivable(ctx context.Context, actor Actor, receivableID string, in PaymentInput) (gen.ReceivablePayment, error) {
	var created gen.ReceivablePayment
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if err := in.normalise(clock, o.now()); err != nil {
			return err
		}

		balance, err := q.GetReceivable(ctx, receivableID)
		if err != nil || balance.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: piutang tidak ditemukan", ErrNotFound)
		}
		if in.AmountIDR.IsPositive() && int64(in.AmountIDR) > balance.OutstandingIdr {
			return fmt.Errorf("%w: pembayaran %s melebihi sisa piutang %s",
				ErrValidation, in.AmountIDR, money.IDR(balance.OutstandingIdr))
		}

		row, err := q.RecordReceivablePayment(ctx, gen.RecordReceivablePaymentParams{
			ID: store.NewID(), ReceivableID: receivableID, AmountIdr: int64(in.AmountIDR),
			PaidOn: in.PaidOn, Method: in.Method, Note: nilIfEmpty(in.Note),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: o.now().Unix(),
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "receivable_payment", RecordID: row.ID,
			Action: ActionCreate, After: row, ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// ListPayables returns hutang. outstandingOnly hides what is settled.
func (o *Opening) ListPayables(ctx context.Context, entityID string, outstandingOnly bool) ([]gen.PayableBalance, error) {
	if outstandingOnly {
		rows, err := o.q.ListOutstandingPayables(ctx, entityID)
		if err != nil {
			return nil, fmt.Errorf("service: list payables: %w", err)
		}
		return rows, nil
	}
	rows, err := o.q.ListPayables(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list payables: %w", err)
	}
	return rows, nil
}

// ListReceivables returns piutang.
func (o *Opening) ListReceivables(ctx context.Context, entityID string, outstandingOnly bool) ([]gen.ReceivableBalance, error) {
	if outstandingOnly {
		rows, err := o.q.ListOutstandingReceivables(ctx, entityID)
		if err != nil {
			return nil, fmt.Errorf("service: list receivables: %w", err)
		}
		return rows, nil
	}
	rows, err := o.q.ListReceivables(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list receivables: %w", err)
	}
	return rows, nil
}

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// ErrReportIntegrity is returned when the margin report cannot be produced
// because the rows behind it disagree with each other.
//
// Deliberately not ErrValidation: the request was fine, the books are not. The
// message carries the domain's own wording, which names the sale and the
// product to go and look at — a caller told only "something is wrong" has
// nowhere to start, and this is the report a family settles money on.
var ErrReportIntegrity = errors.New("service: laporan margin tidak dapat dihitung")

// CompanyBucketName is what unowned stock is called on screen (R2.2).
//
// The domain holds no UI text, so the label is supplied here. It is a real line
// on the report beside the family members, not a residual: some stock genuinely
// belongs to the company and settles differently.
const CompanyBucketName = "Perusahaan"

// DefaultReturnPeriodRule is what a company's margin report runs on until
// somebody chooses otherwise.
//
// SPEC §4.4, resolved as [margin.AtReturnDate] (D-012): a cross-boundary return
// reduces the month the goods came back. It never restates money that has
// already been divided, it puts the margin reduction in the same month as the
// refund that left the till, and it is the recoverable direction if this family
// turns out to settle differently.
//
// The alternative stays implemented and switchable per company through
// [Margin.SetReturnRule], because which one is right is a fact about how a
// family settles rather than about accounting.
const DefaultReturnPeriodRule = margin.AtReturnDate

// Margin reports gross margin per owner. TASKS 3.1–3.6.
//
// The highest-stakes output in the system: family members settle money between
// themselves monthly on these figures (R2.4). Everything here is a read — no
// transaction boundary to hold — except the one setting the report depends on,
// which is audited like any other change to how history reads (INV-10).
//
// Who may call this is decided in transport by [Role.CanSeeAllOwnerMargin]
// (D-009): owners and managers, never staff, and per entity. A margin figure
// that reaches the browser has already left the building, so the endpoint
// refuses rather than the screen hiding a table.
type Margin struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewMargin builds the service.
func NewMargin(db *store.DB, aud *Auditor, now func() time.Time) *Margin {
	if now == nil {
		now = time.Now
	}
	return &Margin{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- the returns-period rule (TASKS 3.4, SPEC §4.4) -------------------------

// ReturnRule is the rule in force for one company.
//
// Chosen is false while the company is running on [DefaultReturnPeriodRule] —
// no row, nobody has touched it. It is the settings screen's business, not the
// report's: the report states the rule it ran under either way.
type ReturnRule struct {
	Rule      margin.ReturnPeriodRule
	Chosen    bool
	Note      string
	UpdatedAt time.Time
}

// ReturnRuleFor reads the rule for a company, falling back to
// [DefaultReturnPeriodRule] when nobody has set one.
//
// The absent row is not an error: a company that has never had a cross-boundary
// return has had no reason to think about it, and the default is a decision
// (D-012), not a gap.
func (m *Margin) ReturnRuleFor(ctx context.Context, entityID string) (ReturnRule, error) {
	row, err := m.q.GetMarginSetting(ctx, entityID)
	if errors.Is(err, sql.ErrNoRows) {
		return ReturnRule{Rule: DefaultReturnPeriodRule, Chosen: false}, nil
	}
	if err != nil {
		return ReturnRule{}, fmt.Errorf("service: margin setting: %w", err)
	}

	rule, err := margin.ParseReturnPeriodRule(row.ReturnPeriodRule)
	if err != nil {
		// A stored rule outside the two means something wrote it that should
		// not have. Refusing beats reporting under a rule nobody recognises.
		return ReturnRule{}, fmt.Errorf("%w: aturan periode retur tersimpan tidak dikenal: %q",
			ErrValidation, row.ReturnPeriodRule)
	}
	out := ReturnRule{
		Rule: rule, Chosen: true, UpdatedAt: time.Unix(row.UpdatedAt, 0).UTC(),
	}
	if row.Note != nil {
		out.Note = *row.Note
	}
	return out, nil
}

// SetReturnRule changes which period this company's returns count in.
//
// Audited, because it changes what every past report says — the user declined
// period locking (R7.3), so this log is the only trace that October's figures
// were read one way in November and another way in December. Writing a row is
// also what makes [ReturnRule.Chosen] true: no row means the default.
func (m *Margin) SetReturnRule(ctx context.Context, actor Actor, rule, note string) (ReturnRule, error) {
	parsed, err := margin.ParseReturnPeriodRule(strings.ToUpper(strings.TrimSpace(rule)))
	if err != nil {
		return ReturnRule{}, fmt.Errorf("%w: aturan periode retur harus RETURN_DATE atau SALE_DATE", ErrValidation)
	}
	if actor.LegalEntityID == "" {
		return ReturnRule{}, fmt.Errorf("%w: perusahaan belum dipilih", ErrValidation)
	}

	// Read the current rule before opening the transaction. The pool holds a
	// single connection (SQLite has one writer anyway), so a read issued from
	// inside InTx would wait on the connection the transaction is holding.
	before, err := m.ReturnRuleFor(ctx, actor.LegalEntityID)
	if err != nil {
		return ReturnRule{}, err
	}

	var out ReturnRule
	err = m.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		now := m.now().Unix()
		row, err := q.UpsertMarginSetting(ctx, gen.UpsertMarginSettingParams{
			EntityID: actor.LegalEntityID, ReturnPeriodRule: string(parsed),
			Note:      nilIfEmpty(strings.TrimSpace(note)),
			UpdatedBy: nilIfEmpty(actor.UserID), UpdatedAt: now, CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}

		out = ReturnRule{
			Rule: parsed, Chosen: true, Note: strings.TrimSpace(note),
			UpdatedAt: time.Unix(row.UpdatedAt, 0).UTC(),
		}
		return m.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "margin_setting", RecordID: actor.LegalEntityID,
			Action: ActionUpdate, Before: before, After: out,
			Reason:          strings.TrimSpace(note),
			ClientRequestID: actor.ClientRequestID,
		})
	})
	if err != nil {
		return ReturnRule{}, err
	}
	return out, nil
}

// --- the report (TASKS 3.1–3.3) ---------------------------------------------

// Report builds Laporan Margin per Owner for one company and one window.
//
// Never Laba Rugi: shared costs — listrik, gaji, sewa — are out of scope and
// settled outside the application (REQUIREMENTS §5), so this figure has not
// paid rent.
//
// An empty from or to means the current month in the company's own timezone
// (INV-5), which is the window money is actually settled on.
func (m *Margin) Report(ctx context.Context, entityID, from, to string) (margin.Report, error) {
	rule, err := m.ReturnRuleFor(ctx, entityID)
	if err != nil {
		return margin.Report{}, err
	}

	period, err := m.resolvePeriod(ctx, entityID, from, to)
	if err != nil {
		return margin.Report{}, err
	}

	input, err := m.load(ctx, entityID, period)
	if err != nil {
		return margin.Report{}, err
	}
	input.ReturnPeriod = rule.Rule

	report, err := margin.Compute(input)
	if err != nil {
		// The domain refuses rather than printing a figure it cannot justify.
		// Surfaced as-is: whoever reads this needs the sale and the product it
		// names, not a generic failure.
		return margin.Report{}, fmt.Errorf("%w: %w", ErrReportIntegrity, err)
	}
	return report, nil
}

// resolvePeriod turns an optional window into business dates, defaulting to the
// current month in the entity's own timezone.
//
// Never the server's zone. A report run at 06:00 WIB on 1 November from a
// machine set to UTC would otherwise still be reporting October, and the
// figure it produced would be the one the family settled on (INV-5, D-005).
func (m *Margin) resolvePeriod(ctx context.Context, entityID, from, to string) (margin.Period, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)

	if from == "" || to == "" {
		entity, err := m.q.GetLegalEntity(ctx, entityID)
		if err != nil {
			return margin.Period{}, fmt.Errorf("%w: perusahaan tidak ditemukan", ErrNotFound)
		}
		loc, err := time.LoadLocation(entity.Timezone)
		if err != nil {
			return margin.Period{}, fmt.Errorf("%w: zona waktu perusahaan %q tidak dikenal",
				ErrValidation, entity.Timezone)
		}
		today := m.now().In(loc)
		current := margin.Month(today.Year(), today.Month())
		if from == "" {
			from = current.From
		}
		if to == "" {
			to = current.To
		}
	}

	period := margin.Period{From: from, To: to}
	if err := period.Validate(); err != nil {
		return margin.Period{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return period, nil
}

// load reads the whole window in six queries and assembles the domain input.
//
// Reads only, and outside a transaction deliberately: a report is a snapshot of
// a database whose history is append-only, so there is nothing here that a
// concurrent sale can tear. What it must never do is aggregate — every figure
// on screen is summed in Go from these rows, so the owner total and the
// drill-down are the same numbers added twice rather than two queries obliged
// to agree (SPEC §4.2).
func (m *Margin) load(ctx context.Context, entityID string, period margin.Period) (margin.Input, error) {
	names, err := m.ownerNames(ctx)
	if err != nil {
		return margin.Input{}, err
	}

	from, to := period.From, period.To

	saleRows, err := m.q.ListSalesForMargin(ctx, gen.ListSalesForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: sales for margin: %w", err)
	}
	lineRows, err := m.q.ListSaleLinesForMargin(ctx, gen.ListSaleLinesForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: sale lines for margin: %w", err)
	}
	drawRows, err := m.q.ListSaleDrawsForMargin(ctx, gen.ListSaleDrawsForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: sale draws for margin: %w", err)
	}

	linesBySale := make(map[string][]margin.SaleLine, len(saleRows))
	for _, l := range lineRows {
		linesBySale[l.SaleID] = append(linesBySale[l.SaleID], margin.SaleLine{
			ID: l.ID, ProductID: l.ProductID, ProductCode: l.ProductCode,
			ProductName: l.ProductName, OwnerID: ownerIDOf(l.OwnerID), Qty: l.Qty,
			// Revenue is the DPP, not what the customer handed over. COGS comes
			// off a stock layer already net of creditable PPN (SPEC §3.2), so
			// revenue has to be net of PPN too -- otherwise every owner's margin
			// is overstated by the tax rate, in the report the family settles
			// real money on. On an untaxed sale the two are the same figure, and
			// the PPN travels alongside so the drill-down can explain the gap.
			Revenue: money.IDR(l.DppIdr), PPN: money.IDR(l.PpnIdr),
			RecordedCOGS: money.IDR(l.CogsIdr),
		})
	}
	drawsBySale := make(map[string][]margin.Draw, len(saleRows))
	for _, d := range drawRows {
		drawsBySale[d.SaleID] = append(drawsBySale[d.SaleID], margin.Draw{
			ID: d.ID, LayerID: d.LayerID, ProductID: d.ProductID,
			OwnerID: ownerIDOf(d.OwnerID), QtyOut: d.QtyOut, Cost: money.IDR(d.CostIdr),
			LayerAcquiredAt: time.Unix(d.LayerAcquiredAt, 0).UTC(),
			LayerQtyIn:      d.LayerQtyIn, LayerCostTotal: money.IDR(d.LayerCostTotalIdr),
			LayerSource: d.LayerSource, FakturReceived: d.LayerFakturReceived == 1,
			ReversesID: derefString(d.ReversesID),
		})
	}

	sales := make([]margin.Sale, 0, len(saleRows))
	for _, s := range saleRows {
		sales = append(sales, margin.Sale{
			ID: s.ID, InvoiceNo: s.InvoiceNo, BusinessDate: s.BusinessDate,
			OccurredAt: time.Unix(s.OccurredAt, 0).UTC(),
			// Walk-in customers are the norm at a till, so no name is not a
			// missing value.
			CustomerName: derefString(s.CustomerName),
			Lines:        linesBySale[s.ID], Draws: drawsBySale[s.ID],
		})
	}

	returnRows, err := m.q.ListReturnsForMargin(ctx, gen.ListReturnsForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: returns for margin: %w", err)
	}
	returnLineRows, err := m.q.ListReturnLinesForMargin(ctx, gen.ListReturnLinesForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: return lines for margin: %w", err)
	}
	returnDrawRows, err := m.q.ListReturnDrawsForMargin(ctx, gen.ListReturnDrawsForMarginParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return margin.Input{}, fmt.Errorf("service: return draws for margin: %w", err)
	}

	linesByReturn := make(map[string][]margin.ReturnLine, len(returnRows))
	for _, l := range returnLineRows {
		linesByReturn[l.SaleReturnID] = append(linesByReturn[l.SaleReturnID], margin.ReturnLine{
			ID: l.ID, SaleLineID: l.SaleLineID, ProductID: l.ProductID,
			ProductCode: l.ProductCode, ProductName: l.ProductName,
			OwnerID: ownerIDOf(l.OwnerID), Qty: l.Qty,
			// The whole sum handed back, and the PPN inside it. The margin
			// reversal is the difference: tax given back goes to the state's
			// column, not the owner's (SPEC §2.4).
			Refund:       money.IDR(l.RefundIdr),
			PPNReversed:  money.IDR(l.PpnReversedIdr),
			COGSReversed: money.IDR(l.CogsReversedIdr),
		})
	}
	drawsByReturn := make(map[string][]margin.Draw, len(returnRows))
	for _, d := range returnDrawRows {
		drawsByReturn[d.SaleReturnID] = append(drawsByReturn[d.SaleReturnID], margin.Draw{
			ID: d.ID, LayerID: d.LayerID, ProductID: d.ProductID,
			OwnerID: ownerIDOf(d.OwnerID), QtyOut: d.QtyOut, Cost: money.IDR(d.CostIdr),
			LayerAcquiredAt: time.Unix(d.LayerAcquiredAt, 0).UTC(),
			LayerQtyIn:      d.LayerQtyIn, LayerCostTotal: money.IDR(d.LayerCostTotalIdr),
			LayerSource: d.LayerSource, FakturReceived: d.LayerFakturReceived == 1,
			ReversesID: derefString(d.ReversesID),
		})
	}

	returns := make([]margin.Return, 0, len(returnRows))
	for _, r := range returnRows {
		returns = append(returns, margin.Return{
			ID: r.ID, SaleID: r.SaleID, SaleInvoiceNo: r.SaleInvoiceNo,
			BusinessDate: r.BusinessDate, SaleBusinessDate: r.SaleBusinessDate,
			Reason: r.Reason,
			Lines:  linesByReturn[r.ID], Draws: drawsByReturn[r.ID],
		})
	}

	return margin.Input{Period: period, OwnerNames: names, Sales: sales, Returns: returns}, nil
}

// ownerNames names every owner, inactive ones included.
//
// An owner who has left the business still has margin in the months they were
// here, and a settlement report is exactly where that history is read. Omitting
// them would make Compute refuse — which is the right failure, but the wrong
// question to be asking.
func (m *Margin) ownerNames(ctx context.Context) (map[margin.OwnerID]string, error) {
	rows, err := m.q.ListOwners(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("service: owners: %w", err)
	}
	names := make(map[margin.OwnerID]string, len(rows)+1)
	for _, o := range rows {
		names[margin.OwnerID(o.ID)] = o.Name
	}
	names[margin.Company] = CompanyBucketName
	return names, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

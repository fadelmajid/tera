package aging

import (
	"fmt"
	"sort"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// DateFormat is the business-date form every dated table stores: 'YYYY-MM-DD'
// in the entity's timezone (D-005).
const DateFormat = "2006-01-02"

// Kind is which ledger is being aged.
//
// The arithmetic is identical for both, which is exactly why the two reports
// carry a label: a screen showing what the shop owes and a screen showing what
// it is owed must not be confusable, and they otherwise differ only in whose
// name is in the counterparty column.
type Kind string

// The two ledgers.
const (
	Hutang  Kind = "HUTANG"
	Piutang Kind = "PIUTANG"
)

// Bucket is how late a document is, in the conventional ladder.
type Bucket string

// The buckets, in the order a report reads them.
//
// Thirty-day steps are the universal convention and nobody has asked for
// another ladder, so it is fixed here rather than made configurable. What is
// not conventional is NoDueDate: most systems fold undated invoices into the
// current bucket, which quietly reports them as fine.
const (
	// NotYetDue is a document with a due date that has not arrived.
	NotYetDue Bucket = "NOT_YET_DUE"
	// Days1To30 through Over90 are overdue, counted from the due date.
	Days1To30  Bucket = "1_30"
	Days31To60 Bucket = "31_60"
	Days61To90 Bucket = "61_90"
	Over90     Bucket = "OVER_90"
	// NoDueDate is a document nobody recorded a term for. Not aged, because
	// ageing it would mean inventing the term.
	NoDueDate Bucket = "NO_DUE_DATE"
)

// Order is the buckets in reading order, for a caller building columns.
func Order() []Bucket {
	return []Bucket{NotYetDue, Days1To30, Days31To60, Days61To90, Over90, NoDueDate}
}

// Item is one outstanding document: an unpaid purchase invoice, a credit sale,
// or a balance carried in at go-live (R11.5).
type Item struct {
	ID string

	// CounterpartyID and CounterpartyName are the supplier on hutang and the
	// customer on piutang.
	CounterpartyID   string
	CounterpartyName string

	// DocumentNo is the invoice number, where there is one. An opening balance
	// often has none.
	DocumentNo string
	// Source is PURCHASE/SALE or OPENING, echoed so the report can say which
	// figures came from the go-live carry-in rather than from trading here.
	Source string

	IncurredOn string
	// DueDate is empty when no term was recorded. That is a real state, not a
	// missing value: a supplier may give no term at all.
	DueDate string

	Amount      money.IDR
	Paid        money.IDR
	Outstanding money.IDR
}

// Input is one ledger to age, as of one day.
type Input struct {
	Kind Kind
	// AsOf is the business date to age against, in the entity's timezone.
	AsOf string
	// Items are the outstanding documents. Settled ones may be included and
	// are skipped; the caller does not have to filter first.
	Items []Item
}

// Aged is one document with its age worked out.
type Aged struct {
	Item
	Bucket Bucket
	// DaysOverdue is whole days past the due date, zero when the document is
	// not yet due and zero when no term was recorded. Read it with Bucket, not
	// on its own: zero means three different things and only Bucket separates
	// them.
	DaysOverdue int
}

// Buckets is money by lateness.
type Buckets struct {
	NotYetDue  money.IDR
	Days1To30  money.IDR
	Days31To60 money.IDR
	Days61To90 money.IDR
	Over90     money.IDR
	NoDueDate  money.IDR
}

// Total is everything outstanding, aged or not.
func (b Buckets) Total() money.IDR {
	return money.Sum(b.NotYetDue, b.Days1To30, b.Days31To60, b.Days61To90, b.Over90, b.NoDueDate)
}

// Overdue is what is demonstrably late.
//
// NoDueDate is deliberately excluded. Those documents may well be months late —
// nobody knows, because nobody recorded a term — and counting them as overdue
// would state as fact something this system cannot know. The report shows them
// as their own figure instead, which is the question rather than the answer.
func (b Buckets) Overdue() money.IDR {
	return money.Sum(b.Days1To30, b.Days31To60, b.Days61To90, b.Over90)
}

// Get returns one bucket's figure, for a caller iterating Order().
func (b Buckets) Get(bucket Bucket) money.IDR {
	switch bucket {
	case NotYetDue:
		return b.NotYetDue
	case Days1To30:
		return b.Days1To30
	case Days31To60:
		return b.Days31To60
	case Days61To90:
		return b.Days61To90
	case Over90:
		return b.Over90
	case NoDueDate:
		return b.NoDueDate
	default:
		return money.Zero
	}
}

func (b *Buckets) add(bucket Bucket, v money.IDR) {
	switch bucket {
	case NotYetDue:
		b.NotYetDue = b.NotYetDue.Add(v)
	case Days1To30:
		b.Days1To30 = b.Days1To30.Add(v)
	case Days31To60:
		b.Days31To60 = b.Days31To60.Add(v)
	case Days61To90:
		b.Days61To90 = b.Days61To90.Add(v)
	case Over90:
		b.Over90 = b.Over90.Add(v)
	case NoDueDate:
		b.NoDueDate = b.NoDueDate.Add(v)
	}
}

// Counterparty is one supplier's or customer's line on the report, with the
// documents behind it.
type Counterparty struct {
	ID   string
	Name string

	Buckets Buckets
	// OldestDays is how long the oldest overdue document has been overdue.
	// Zero when nothing is demonstrably late. This is what the report sorts on:
	// the person to ring this morning is the one at the top.
	OldestDays int
	// WithoutDueDate is how many of this counterparty's documents carry no
	// term.
	WithoutDueDate int

	Items []Aged
}

// Total is everything outstanding with this counterparty.
func (c Counterparty) Total() money.IDR { return c.Buckets.Total() }

// Report is one aged ledger.
type Report struct {
	Kind Kind
	AsOf string

	Buckets        Buckets
	Counterparties []Counterparty

	// WithoutDueDate is how many documents could not be aged because no term
	// was recorded.
	//
	// Surfaced as a count rather than left to be inferred from a bucket total,
	// because the fix is data entry and the number is the nudge. Ageing depends
	// on a credit-terms answer this business has not given yet
	// (REQUIREMENTS §11).
	WithoutDueDate int

	// CreditBalance is money paid beyond what was owed, as a positive
	// magnitude, and CreditItems are the documents it sits on.
	//
	// Not aged, because a credit is not a debt, and not dropped either: an
	// invoice paid twice is a real thing that a ledger showing only positive
	// balances would silently swallow.
	CreditBalance money.IDR
	CreditItems   []Item
}

// Age works out how late everything is, as of a day (R5.8).
//
// Documents are grouped by counterparty and both are ordered worst-first: the
// counterparty whose oldest debt has been outstanding longest comes first, and
// within them the latest document does. Alphabetical order is what a system
// produces when nobody thought about who reads it.
func Age(in Input) (Report, error) {
	asOf, err := time.Parse(DateFormat, in.AsOf)
	if err != nil {
		return Report{}, fmt.Errorf("%w: as-of date %q is not a YYYY-MM-DD date", ErrInvalidInput, in.AsOf)
	}
	if in.Kind != Hutang && in.Kind != Piutang {
		return Report{}, fmt.Errorf("%w: ledger is %q, want %s or %s", ErrInvalidInput, in.Kind, Hutang, Piutang)
	}

	out := Report{Kind: in.Kind, AsOf: in.AsOf}
	byParty := make(map[string]*Counterparty, len(in.Items))
	order := make([]string, 0, len(in.Items))

	for _, it := range in.Items {
		if err := it.validate(); err != nil {
			return Report{}, err
		}

		switch {
		case it.Outstanding.IsZero():
			// Settled. Not an error and not a row: a paid invoice on an aging
			// report is noise on a screen meant to be scanned in a hurry.
			continue
		case it.Outstanding.IsNegative():
			out.CreditBalance = out.CreditBalance.Add(it.Outstanding.Abs())
			out.CreditItems = append(out.CreditItems, it)
			continue
		}

		bucket, days, err := classify(it, asOf)
		if err != nil {
			return Report{}, err
		}

		party, seen := byParty[it.CounterpartyID]
		if !seen {
			party = &Counterparty{ID: it.CounterpartyID, Name: it.CounterpartyName}
			byParty[it.CounterpartyID] = party
			order = append(order, it.CounterpartyID)
		}

		party.Items = append(party.Items, Aged{Item: it, Bucket: bucket, DaysOverdue: days})
		party.Buckets.add(bucket, it.Outstanding)
		out.Buckets.add(bucket, it.Outstanding)

		if days > party.OldestDays {
			party.OldestDays = days
		}
		if bucket == NoDueDate {
			party.WithoutDueDate++
			out.WithoutDueDate++
		}
	}

	out.Counterparties = make([]Counterparty, 0, len(order))
	for _, id := range order {
		party := byParty[id]
		sortItems(party.Items)
		out.Counterparties = append(out.Counterparties, *party)
	}
	sortCounterparties(out.Counterparties)
	sortCredits(out.CreditItems)

	return out, nil
}

// classify places one document in a bucket.
func classify(it Item, asOf time.Time) (Bucket, int, error) {
	if it.DueDate == "" {
		return NoDueDate, 0, nil
	}

	due, err := time.Parse(DateFormat, it.DueDate)
	if err != nil {
		return "", 0, fmt.Errorf("%w %s: due date %q is not a YYYY-MM-DD date",
			ErrInvalidItem, it.ID, it.DueDate)
	}

	// Whole days. Both dates are midnight UTC by construction — they are
	// calendar days that were resolved in the entity's zone when they were
	// written — so this subtraction is exact and never straddles an hour.
	days := int(asOf.Sub(due).Hours() / 24)
	if days <= 0 {
		// Due today is not late. The distinction matters on the one day
		// somebody looks at a specific invoice and expects it not to be
		// flagged yet.
		return NotYetDue, 0, nil
	}

	switch {
	case days <= 30:
		return Days1To30, days, nil
	case days <= 60:
		return Days31To60, days, nil
	case days <= 90:
		return Days61To90, days, nil
	default:
		return Over90, days, nil
	}
}

func (it Item) validate() error {
	if it.CounterpartyID == "" {
		return fmt.Errorf("%w %s: no counterparty", ErrInvalidItem, it.ID)
	}
	if _, err := time.Parse(DateFormat, it.IncurredOn); err != nil {
		return fmt.Errorf("%w %s: incurred-on %q is not a YYYY-MM-DD date",
			ErrInvalidItem, it.ID, it.IncurredOn)
	}
	if it.Amount.Sub(it.Paid) != it.Outstanding {
		return &InconsistentBalanceError{
			ItemID: it.ID, DocumentNo: it.DocumentNo,
			Amount: int64(it.Amount), Paid: int64(it.Paid), Outstanding: int64(it.Outstanding),
		}
	}
	return nil
}

// sortCounterparties orders the report worst-first: longest overdue, then
// largest, then by name so the order is stable when nothing is late.
func sortCounterparties(rows []Counterparty) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.OldestDays != b.OldestDays {
			return a.OldestDays > b.OldestDays
		}
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// sortItems orders one counterparty's documents latest-first, with the undated
// ones last: they are a question, not a debt to chase today.
func sortItems(rows []Aged) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Bucket == NoDueDate) != (b.Bucket == NoDueDate) {
			return b.Bucket == NoDueDate
		}
		if a.DaysOverdue != b.DaysOverdue {
			return a.DaysOverdue > b.DaysOverdue
		}
		if a.DueDate != b.DueDate {
			return a.DueDate < b.DueDate
		}
		if a.IncurredOn != b.IncurredOn {
			return a.IncurredOn < b.IncurredOn
		}
		return a.ID < b.ID
	})
}

func sortCredits(rows []Item) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Outstanding != b.Outstanding {
			return a.Outstanding < b.Outstanding // most over-paid first
		}
		return a.ID < b.ID
	})
}

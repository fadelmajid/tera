package aging_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/aging"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// item builds one outstanding document, paid nothing unless said otherwise.
func item(id, party, due string, amount money.IDR) aging.Item {
	return aging.Item{
		ID: id, CounterpartyID: party, CounterpartyName: party,
		DocumentNo: "INV-" + id, Source: "PURCHASE",
		IncurredOn: "2026-08-01", DueDate: due,
		Amount: amount, Outstanding: amount,
	}
}

func age(t *testing.T, asOf string, items ...aging.Item) aging.Report {
	t.Helper()

	got, err := aging.Age(aging.Input{Kind: aging.Hutang, AsOf: asOf, Items: items})
	if err != nil {
		t.Fatalf("Age: %v", err)
	}
	return got
}

// TestBucketBoundaries pins every edge of the ladder.
//
// Off-by-one here is not cosmetic: it is the difference between an invoice
// appearing in the column somebody chases this week and the column they chase
// next month.
func TestBucketBoundaries(t *testing.T) {
	t.Parallel()

	const asOf = "2026-08-21"

	tests := []struct {
		name     string
		due      string
		want     aging.Bucket
		wantDays int
	}{
		{"due in a week", "2026-08-28", aging.NotYetDue, 0},
		{"due tomorrow", "2026-08-22", aging.NotYetDue, 0},
		// Due today is not late. Somebody looking at a specific invoice on its
		// due date expects it not to be flagged yet, and they are right.
		{"due today", "2026-08-21", aging.NotYetDue, 0},
		{"one day late", "2026-08-20", aging.Days1To30, 1},
		{"thirty days late", "2026-07-22", aging.Days1To30, 30},
		{"thirty-one days late", "2026-07-21", aging.Days31To60, 31},
		{"sixty days late", "2026-06-22", aging.Days31To60, 60},
		{"sixty-one days late", "2026-06-21", aging.Days61To90, 61},
		{"ninety days late", "2026-05-23", aging.Days61To90, 90},
		{"ninety-one days late", "2026-05-22", aging.Over90, 91},
		{"a year late", "2025-08-21", aging.Over90, 365},
		// Not aged, and deliberately not folded into "not yet due".
		{"no term recorded", "", aging.NoDueDate, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := age(t, asOf, item("1", "S1", tt.due, 1_000_000))
			if len(got.Counterparties) != 1 || len(got.Counterparties[0].Items) != 1 {
				t.Fatalf("got %d counterparties", len(got.Counterparties))
			}
			row := got.Counterparties[0].Items[0]
			if row.Bucket != tt.want {
				t.Errorf("bucket = %s, want %s", row.Bucket, tt.want)
			}
			if row.DaysOverdue != tt.wantDays {
				t.Errorf("days overdue = %d, want %d", row.DaysOverdue, tt.wantDays)
			}
			if got.Buckets.Get(tt.want) != 1_000_000 {
				t.Errorf("the %s bucket totals %s", tt.want, got.Buckets.Get(tt.want))
			}
		})
	}
}

// TestAnUndatedInvoiceIsNeverReportedAsCurrent is the rule this package exists
// to hold.
//
// Most systems fold an invoice with no due date into the current bucket, which
// reports it as fine. It might be six months late — nobody knows, because
// nobody recorded a term. Saying so is the honest answer, and the count is what
// makes it fixable.
func TestAnUndatedInvoiceIsNeverReportedAsCurrent(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21",
		item("1", "S1", "", 5_000_000),
		item("2", "S1", "2026-09-30", 1_000_000),
	)

	if got.Buckets.NotYetDue != 1_000_000 {
		t.Errorf("not-yet-due = %s, want only the invoice that has a term", got.Buckets.NotYetDue)
	}
	if got.Buckets.NoDueDate != 5_000_000 {
		t.Errorf("no-due-date = %s, want the undated invoice on its own line", got.Buckets.NoDueDate)
	}
	// And it is not claimed as overdue either. This system does not know.
	if !got.Buckets.Overdue().IsZero() {
		t.Errorf("overdue = %s, want zero: nothing here is demonstrably late", got.Buckets.Overdue())
	}
	if got.WithoutDueDate != 1 {
		t.Errorf("%d documents without a term, want 1", got.WithoutDueDate)
	}
	if got.Buckets.Total() != 6_000_000 {
		t.Errorf("total = %s, want everything outstanding", got.Buckets.Total())
	}
}

// TestPartialPaymentsAgeTheRemainder is R5.8's second half. An invoice half
// paid and a month late is half the problem it was.
func TestPartialPaymentsAgeTheRemainder(t *testing.T) {
	t.Parallel()

	half := item("1", "S1", "2026-08-01", 10_000_000)
	half.Paid, half.Outstanding = 6_000_000, 4_000_000

	got := age(t, "2026-08-21", half)

	if got.Buckets.Days1To30 != 4_000_000 {
		t.Errorf("aged %s, want the Rp 4.000.000 still owed rather than the invoice",
			got.Buckets.Days1To30)
	}
	if got.Counterparties[0].Items[0].DaysOverdue != 20 {
		t.Errorf("days overdue = %d, want 20", got.Counterparties[0].Items[0].DaysOverdue)
	}
}

// TestASettledInvoiceLeavesTheReport. A paid invoice on an aging report is
// noise on a screen meant to be scanned in a hurry.
func TestASettledInvoiceLeavesTheReport(t *testing.T) {
	t.Parallel()

	settled := item("1", "S1", "2026-01-01", 9_000_000)
	settled.Paid, settled.Outstanding = 9_000_000, 0

	got := age(t, "2026-08-21", settled, item("2", "S1", "2026-08-20", 1_000_000))

	if len(got.Counterparties) != 1 || len(got.Counterparties[0].Items) != 1 {
		t.Fatalf("the settled invoice is still on the report")
	}
	if got.Buckets.Total() != 1_000_000 {
		t.Errorf("total = %s, want only what is still owed", got.Buckets.Total())
	}
}

// TestAnOverpaymentIsReportedAsACreditNotAged keeps money paid twice visible.
//
// A credit is not a debt and cannot be late. Dropping it would be worse: an
// invoice paid twice is exactly the kind of thing a ledger should not lose
// quietly.
func TestAnOverpaymentIsReportedAsACreditNotAged(t *testing.T) {
	t.Parallel()

	over := item("1", "S1", "2026-01-01", 5_000_000)
	over.Paid, over.Outstanding = 6_000_000, -1_000_000

	got := age(t, "2026-08-21", over, item("2", "S2", "2026-08-20", 2_000_000))

	if got.CreditBalance != 1_000_000 {
		t.Errorf("credit balance = %s, want Rp 1.000.000", got.CreditBalance)
	}
	if len(got.CreditItems) != 1 || got.CreditItems[0].ID != "1" {
		t.Errorf("the over-paid document is not listed: %+v", got.CreditItems)
	}
	// It is not in the buckets, and it has not been netted against the debt.
	if got.Buckets.Total() != 2_000_000 {
		t.Errorf("buckets total %s, want only the real debt", got.Buckets.Total())
	}
	for _, c := range got.Counterparties {
		if c.ID == "S1" {
			t.Error("a counterparty with only a credit was aged")
		}
	}
}

// TestTheReportReadsWorstFirst. The person to ring this morning is at the top.
func TestTheReportReadsWorstFirst(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21",
		// Alphabetically first, and not late at all.
		item("1", "Anugrah", "2026-09-30", 50_000_000),
		// Small, and four months late.
		item("2", "Zenith", "2026-04-01", 900_000),
		// Large, and a fortnight late.
		item("3", "Medika", "2026-08-07", 30_000_000),
	)

	want := []string{"Zenith", "Medika", "Anugrah"}
	for i, name := range want {
		if got.Counterparties[i].Name != name {
			t.Errorf("position %d is %s, want %s", i+1, got.Counterparties[i].Name, name)
		}
	}
	if got.Counterparties[0].OldestDays != 142 {
		t.Errorf("Zenith's oldest debt is %d days overdue, want 142", got.Counterparties[0].OldestDays)
	}
}

// TestDocumentsWithinACounterpartyReadLatestFirst, with the undated ones last:
// they are a question, not a debt to chase today.
func TestDocumentsWithinACounterpartyReadLatestFirst(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21",
		item("a", "S1", "", 1_000_000),
		item("b", "S1", "2026-09-01", 2_000_000),
		item("c", "S1", "2026-08-01", 3_000_000),
		item("d", "S1", "2026-05-01", 4_000_000),
	)

	want := []string{"d", "c", "b", "a"}
	rows := got.Counterparties[0].Items
	for i, id := range want {
		if rows[i].ID != id {
			t.Errorf("position %d is %s, want %s", i+1, rows[i].ID, id)
		}
	}
	if got.Counterparties[0].WithoutDueDate != 1 {
		t.Errorf("%d undated documents, want 1", got.Counterparties[0].WithoutDueDate)
	}
}

// TestTotalsAreTheSumOfTheRowsBeneath. No summary-only views: every figure has
// to decompose, or nobody can act on it.
func TestTotalsAreTheSumOfTheRowsBeneath(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21",
		item("1", "S1", "2026-08-20", 1_111_111),
		item("2", "S1", "2026-06-01", 2_222_222),
		item("3", "S2", "", 3_333_333),
		item("4", "S2", "2026-09-30", 4_444_444),
		item("5", "S3", "2026-01-01", 5_555_555),
	)

	var partyTotal money.IDR
	for _, c := range got.Counterparties {
		var itemTotal money.IDR
		for _, it := range c.Items {
			itemTotal = itemTotal.Add(it.Outstanding)
		}
		if itemTotal != c.Total() {
			t.Errorf("%s: documents total %s, the line says %s", c.Name, itemTotal, c.Total())
		}
		partyTotal = partyTotal.Add(c.Total())
	}
	if partyTotal != got.Buckets.Total() {
		t.Errorf("counterparties total %s, the report says %s", partyTotal, got.Buckets.Total())
	}
	if got.Buckets.Total() != 16_666_665 {
		t.Errorf("total = %s, want Rp 16.666.665", got.Buckets.Total())
	}
	// Overdue excludes what could not be aged.
	if got.Buckets.Overdue() != 8_888_888 {
		t.Errorf("overdue = %s, want Rp 8.888.888", got.Buckets.Overdue())
	}
}

// TestEveryBucketIsReachableFromOrder. A caller building columns iterates
// Order(); a bucket missing from it would be money that never appears.
func TestEveryBucketIsReachableFromOrder(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21",
		item("1", "S1", "2026-09-30", 1),
		item("2", "S1", "2026-08-20", 2),
		item("3", "S1", "2026-07-01", 4),
		item("4", "S1", "2026-06-01", 8),
		item("5", "S1", "2026-01-01", 16),
		item("6", "S1", "", 32),
	)

	var summed money.IDR
	for _, b := range aging.Order() {
		summed = summed.Add(got.Buckets.Get(b))
	}
	if summed != got.Buckets.Total() {
		t.Errorf("iterating Order() reaches %s of %s: a bucket is unreachable",
			summed, got.Buckets.Total())
	}
	if summed != 63 {
		t.Errorf("summed %s, want every one of the six buckets populated", summed)
	}
}

func TestAgeRejectsWhatCannotBeTrue(t *testing.T) {
	t.Parallel()

	good := item("1", "S1", "2026-08-01", 1_000_000)

	tests := []struct {
		name string
		in   aging.Input
		want error
	}{
		{
			name: "as-of is not a date",
			in:   aging.Input{Kind: aging.Hutang, AsOf: "21 Agustus 2026", Items: []aging.Item{good}},
			want: aging.ErrInvalidInput,
		},
		{
			name: "no ledger named",
			in:   aging.Input{AsOf: "2026-08-21", Items: []aging.Item{good}},
			want: aging.ErrInvalidInput,
		},
		{
			name: "no counterparty",
			in: aging.Input{Kind: aging.Hutang, AsOf: "2026-08-21", Items: []aging.Item{
				{ID: "1", IncurredOn: "2026-08-01", Amount: 1, Outstanding: 1},
			}},
			want: aging.ErrInvalidItem,
		},
		{
			name: "due date is not a date",
			in: aging.Input{Kind: aging.Hutang, AsOf: "2026-08-21", Items: []aging.Item{
				item("1", "S1", "01/08/2026", 1_000_000),
			}},
			want: aging.ErrInvalidItem,
		},
		{
			// The view derives outstanding rather than storing it precisely so
			// the two cannot drift. Checking again turns a corrupted read into
			// a refusal instead of chasing a supplier for the wrong amount.
			name: "outstanding disagrees with the payments beneath it",
			in: aging.Input{Kind: aging.Hutang, AsOf: "2026-08-21", Items: []aging.Item{
				{
					ID: "1", CounterpartyID: "S1", IncurredOn: "2026-08-01",
					Amount: 1_000_000, Paid: 400_000, Outstanding: 900_000,
				},
			}},
			want: aging.ErrInvalidItem,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := aging.Age(tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

// TestAnInconsistentBalanceSaysWhichDocument. The UI builds its own Indonesian
// wording from these fields; nobody shows Error() to a person.
func TestAnInconsistentBalanceSaysWhichDocument(t *testing.T) {
	t.Parallel()

	_, err := aging.Age(aging.Input{Kind: aging.Piutang, AsOf: "2026-08-21", Items: []aging.Item{
		{
			ID: "abc", CounterpartyID: "C1", DocumentNo: "INV-9",
			IncurredOn: "2026-08-01", Amount: 500_000, Paid: 100_000, Outstanding: 999,
		},
	}})

	var bad *aging.InconsistentBalanceError
	if !errors.As(err, &bad) {
		t.Fatalf("want an *InconsistentBalanceError, got %v", err)
	}
	if bad.ItemID != "abc" || bad.DocumentNo != "INV-9" {
		t.Errorf("the error does not name the document to go and look at: %+v", bad)
	}
}

// TestAnEmptyLedgerIsAReportNotAnError. A shop that owes nobody anything is a
// good day, not a failure.
func TestAnEmptyLedgerIsAReportNotAnError(t *testing.T) {
	t.Parallel()

	got := age(t, "2026-08-21")

	if len(got.Counterparties) != 0 || !got.Buckets.Total().IsZero() {
		t.Errorf("an empty ledger produced %+v", got)
	}
	if got.AsOf != "2026-08-21" || got.Kind != aging.Hutang {
		t.Errorf("the report does not say what it is: %s as of %s", got.Kind, got.AsOf)
	}
}

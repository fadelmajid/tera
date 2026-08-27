package margin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/margin"
)

func TestMonthBuildsTheSettlementWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		year     int
		month    time.Month
		from, to string
	}{
		{2026, time.October, "2026-10-01", "2026-10-31"},
		{2026, time.November, "2026-11-01", "2026-11-30"},
		{2026, time.February, "2026-02-01", "2026-02-28"},
		{2028, time.February, "2028-02-01", "2028-02-29"}, // leap
		{2026, time.December, "2026-12-01", "2026-12-31"},
	}

	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			t.Parallel()

			got := margin.Month(tt.year, tt.month)
			if got.From != tt.from || got.To != tt.to {
				t.Errorf("Month(%d, %s) = %s…%s, want %s…%s",
					tt.year, tt.month, got.From, got.To, tt.from, tt.to)
			}
		})
	}
}

func TestPeriodContains(t *testing.T) {
	t.Parallel()

	october := margin.Month(2026, time.October)

	tests := []struct {
		day  string
		want bool
	}{
		{"2026-09-30", false},
		{"2026-10-01", true}, // inclusive at both ends: a month has 31 days of
		{"2026-10-15", true}, // trading, not 29
		{"2026-10-31", true},
		{"2026-11-01", false},
	}

	for _, tt := range tests {
		t.Run(tt.day, func(t *testing.T) {
			t.Parallel()

			if got := october.Contains(tt.day); got != tt.want {
				t.Errorf("Contains(%s) = %v, want %v", tt.day, got, tt.want)
			}
		})
	}
}

func TestPeriodValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		period  margin.Period
		wantErr bool
	}{
		{"a month", margin.Month(2026, time.October), false},
		{"a single day", margin.Period{From: "2026-10-05", To: "2026-10-05"}, false},
		{"backwards", margin.Period{From: "2026-10-31", To: "2026-10-01"}, true},
		{"not a date", margin.Period{From: "Oktober", To: "2026-10-31"}, true},
		{"day out of range", margin.Period{From: "2026-10-01", To: "2026-10-32"}, true},
		{"empty", margin.Period{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.period.Validate()
			if tt.wantErr && !errors.Is(err, margin.ErrInvalidPeriod) {
				t.Errorf("got %v, want ErrInvalidPeriod", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("got %v, want nil", err)
			}
		})
	}
}

func TestParseReturnPeriodRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    margin.ReturnPeriodRule
		wantErr bool
	}{
		{"RETURN_DATE", margin.AtReturnDate, false},
		{"SALE_DATE", margin.AtSaleDate, false},
		{"", margin.ReturnPeriodUnset, true},
		{"return_date", margin.ReturnPeriodUnset, true}, // stored form is exact
		{"NEXT_MONTH", margin.ReturnPeriodUnset, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := margin.ParseReturnPeriodRule(tt.in)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.wantErr != (err != nil) {
				t.Errorf("error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// SPEC §4.4 in one assertion: the rule decides which month a return counts in,
// and nothing else about the return changes.
func TestEffectiveDateFollowsTheRule(t *testing.T) {
	t.Parallel()

	ret := margin.Return{BusinessDate: "2026-11-03", SaleBusinessDate: "2026-10-10"}

	if got := ret.EffectiveDate(margin.AtReturnDate); got != "2026-11-03" {
		t.Errorf("AtReturnDate = %s, want the day the goods came back", got)
	}
	if got := ret.EffectiveDate(margin.AtSaleDate); got != "2026-10-10" {
		t.Errorf("AtSaleDate = %s, want the day of the original sale", got)
	}
	if !ret.CrossesPeriod() {
		t.Error("a November return of an October sale does not cross a period")
	}

	sameMonth := margin.Return{BusinessDate: "2026-10-20", SaleBusinessDate: "2026-10-12"}
	if sameMonth.CrossesPeriod() {
		t.Error("a return inside its own month is flagged as crossing one")
	}
	// Same month is the case the rule cannot change, and the one that makes
	// most returns uncontroversial.
	if sameMonth.EffectiveDate(margin.AtReturnDate)[:7] != sameMonth.EffectiveDate(margin.AtSaleDate)[:7] {
		t.Error("the rule moved a return that never left its month")
	}
}

package omzet

import (
	"fmt"
	"sort"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// RegisterByPolicy is how the registration deadline is derived from the day the
// threshold was crossed.
//
// Two of them, both implemented, because the correct one is not settled. SPEC
// §5.2 states registration is due by the end of the current book year and cites
// PMK 164/2023 Pasal 17(3) — that regulation governs the 0.5% final PPh regime
// for small business, where exceeding the threshold moves a taxpayer to ordinary
// rates from the following tax year. The rule usually quoted for PKP
// registration itself is PMK 197/2013 Pasal 4, which gives until the end of the
// month after the month the threshold was passed — a much shorter gap.
//
// They may both be right about different obligations. Rather than pick one and
// present it as settled, both are implemented and the choice is a config row
// (INV-4). See testdata/worked_examples/omzet_unverified.json.
type RegisterByPolicy string

// The registration deadline policies.
const (
	// RegisterByEndOfBookYear is SPEC §5.2's reading: the end of the book year
	// the crossing happened in.
	RegisterByEndOfBookYear RegisterByPolicy = "END_OF_BOOK_YEAR"
	// RegisterByEndOfFollowingMonth is the PMK 197/2013 Pasal 4 reading: the
	// end of the month after the month the threshold was passed.
	RegisterByEndOfFollowingMonth RegisterByPolicy = "END_OF_FOLLOWING_MONTH"
)

// VATStartsPolicy is how the date the PPN obligation begins is derived.
type VATStartsPolicy string

// The obligation-start policies.
const (
	// VATStartsNextBookYear is SPEC §5.2's reading: the first tax period of the
	// book year following the crossing.
	VATStartsNextBookYear VATStartsPolicy = "NEXT_BOOK_YEAR_FIRST_PERIOD"
	// VATStartsAfterRegistration is the reading that pairs with the shorter
	// deadline: the first day of the month after registration is due.
	VATStartsAfterRegistration VATStartsPolicy = "MONTH_AFTER_REGISTRATION"
)

// Base is whether turnover is counted before or after PPN (SPEC §5.4).
type Base string

// The two bases.
const (
	// NetOfVAT counts the DPP. The default, and the only one that means
	// anything at the non-PKP entity, which charges no PPN at all.
	NetOfVAT Base = "NET_OF_VAT"
	// Gross counts what the customer paid, PPN included.
	Gross Base = "GROSS"
)

// Threshold is one effective-dated PKP threshold, as a value.
//
// The figure is config, never a constant (INV-4). Rp 4.8 billion has been the
// number since PMK 197/2013, and an omzet clock is a thing an owner plans a year
// around — if the figure changes, the fix is a row, and the sales already
// measured against the old one keep their own book year's answer.
type Threshold struct {
	ID       string
	EntityID string

	// AmountIDR is the threshold. Whole rupiah (INV-1).
	AmountIDR money.IDR

	// WatchBP and WarnBP are where the alarm changes colour, in basis points of
	// the threshold: 7000 is 70%. Integers, so no ratio is ever a float.
	WatchBP int64
	WarnBP  int64

	RegisterBy RegisterByPolicy
	VATStarts  VATStartsPolicy

	// ValidFrom and ValidTo are inclusive 'YYYY-MM-DD' business dates, with an
	// empty ValidTo meaning still in force — the same discipline as a tax rule
	// (SPEC §2.1): never update a figure, close the row and open another.
	ValidFrom string
	ValidTo   string

	// LegalRef is the regulation. Displayed so the owner's konsultan pajak can
	// check the figure without reading code.
	LegalRef string
}

// Validate rejects a threshold that could not be true.
func (t Threshold) Validate() error {
	switch {
	case t.EntityID == "":
		return fmt.Errorf("%w: no entity", ErrInvalidThreshold)
	case !t.AmountIDR.IsPositive():
		return fmt.Errorf("%w %s: threshold is %s", ErrInvalidThreshold, t.ID, t.AmountIDR)
	case t.WatchBP < 0 || t.WatchBP > 10_000:
		return fmt.Errorf("%w %s: watch level is %d bp", ErrInvalidThreshold, t.ID, t.WatchBP)
	case t.WarnBP < 0 || t.WarnBP > 10_000:
		return fmt.Errorf("%w %s: warn level is %d bp", ErrInvalidThreshold, t.ID, t.WarnBP)
	case t.WatchBP > t.WarnBP:
		// The alarm walks OK → WATCH → WARN → CROSSED. A watch level above the
		// warn level would skip a state, and the state somebody skips is the
		// one that was supposed to give them notice.
		return fmt.Errorf("%w %s: watch %d bp is above warn %d bp",
			ErrInvalidThreshold, t.ID, t.WatchBP, t.WarnBP)
	case t.RegisterBy != RegisterByEndOfBookYear && t.RegisterBy != RegisterByEndOfFollowingMonth:
		return fmt.Errorf("%w %s: registration policy %q is not implemented",
			ErrInvalidThreshold, t.ID, t.RegisterBy)
	case t.VATStarts != VATStartsNextBookYear && t.VATStarts != VATStartsAfterRegistration:
		return fmt.Errorf("%w %s: obligation policy %q is not implemented",
			ErrInvalidThreshold, t.ID, t.VATStarts)
	case t.LegalRef == "":
		// A threshold nobody can check against a regulation is a number
		// somebody typed.
		return fmt.Errorf("%w %s: no legal_ref", ErrInvalidThreshold, t.ID)
	}

	if _, err := time.Parse(DateFormat, t.ValidFrom); err != nil {
		return fmt.Errorf("%w %s: valid_from %q is not a YYYY-MM-DD date",
			ErrInvalidThreshold, t.ID, t.ValidFrom)
	}
	if t.ValidTo != "" {
		if _, err := time.Parse(DateFormat, t.ValidTo); err != nil {
			return fmt.Errorf("%w %s: valid_to %q is not a YYYY-MM-DD date",
				ErrInvalidThreshold, t.ID, t.ValidTo)
		}
		if t.ValidTo < t.ValidFrom {
			return fmt.Errorf("%w %s: valid_to %s is before valid_from %s",
				ErrInvalidThreshold, t.ID, t.ValidTo, t.ValidFrom)
		}
	}
	return nil
}

// AppliesOn reports whether the threshold was in force on a business date.
// Inclusive at both ends, as a tax rule is.
func (t Threshold) AppliesOn(businessDate string) bool {
	return businessDate >= t.ValidFrom && (t.ValidTo == "" || businessDate <= t.ValidTo)
}

// ThresholdSet is one entity's effective-dated threshold config.
type ThresholdSet struct {
	entityID   string
	thresholds []Threshold
}

// NewThresholdSet validates the rows and rejects overlaps.
func NewThresholdSet(rows ...Threshold) (ThresholdSet, error) {
	set := ThresholdSet{thresholds: make([]Threshold, 0, len(rows))}

	for _, t := range rows {
		if err := t.Validate(); err != nil {
			return ThresholdSet{}, err
		}
		switch {
		case set.entityID == "":
			set.entityID = t.EntityID
		case t.EntityID != set.entityID:
			return ThresholdSet{}, fmt.Errorf("%w: %s and %s in one set",
				ErrInvalidThreshold, set.entityID, t.EntityID)
		}
		set.thresholds = append(set.thresholds, t)
	}

	sort.SliceStable(set.thresholds, func(i, j int) bool {
		a, b := set.thresholds[i], set.thresholds[j]
		if a.ValidFrom != b.ValidFrom {
			return a.ValidFrom < b.ValidFrom
		}
		return a.ID < b.ID
	})

	for i := 1; i < len(set.thresholds); i++ {
		prev, cur := set.thresholds[i-1], set.thresholds[i]
		if prev.ValidTo == "" || prev.ValidTo >= cur.ValidFrom {
			return ThresholdSet{}, fmt.Errorf(
				"%w: %s and %s both cover %s; close the earlier row's valid_to first",
				ErrInvalidThreshold, prev.ID, cur.ID, cur.ValidFrom)
		}
	}
	return set, nil
}

// EntityID is the company these thresholds belong to.
func (s ThresholdSet) EntityID() string { return s.entityID }

// Thresholds returns a copy, ordered by start date.
func (s ThresholdSet) Thresholds() []Threshold {
	out := make([]Threshold, len(s.thresholds))
	copy(out, s.thresholds)
	return out
}

// Effective returns the threshold in force on a business date.
func (s ThresholdSet) Effective(businessDate string) (Threshold, bool) {
	for _, t := range s.thresholds {
		if t.AppliesOn(businessDate) {
			return t, true
		}
	}
	return Threshold{}, false
}

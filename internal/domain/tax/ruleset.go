package tax

import (
	"fmt"
	"sort"
)

// RuleSet is one entity's effective-dated tax config (SPEC §2.1).
//
// Construction validates. A rule set is read on every sale, so the cheapest
// place to catch a rate that cannot be true is the moment it is loaded, not the
// moment it is applied to somebody's money.
type RuleSet struct {
	entityID string
	rules    []Rule
}

// NewRuleSet validates the rules, rejects overlaps, and orders them.
//
// An empty set is valid and is what a non-PKP entity has. It is not an error
// until a PKP entity tries to price a sale with it, which is where the refusal
// belongs.
func NewRuleSet(rules ...Rule) (RuleSet, error) {
	set := RuleSet{rules: make([]Rule, 0, len(rules))}

	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return RuleSet{}, err
		}
		switch {
		case set.entityID == "":
			set.entityID = r.EntityID
		case r.EntityID != set.entityID:
			// One rule set belongs to one company. The PKP entity charges PPN
			// and the non-PKP one legally cannot, so a set holding both is a
			// loaded gun pointed at whichever sale reads it next.
			return RuleSet{}, fmt.Errorf("%w: %s and %s in one rule set",
				ErrEntityMismatch, set.entityID, r.EntityID)
		}
		set.rules = append(set.rules, r)
	}

	// Ordered by type then start date, so Effective can stop at the first
	// match and an overlap is always between neighbours.
	sort.SliceStable(set.rules, func(i, j int) bool {
		a, b := set.rules[i], set.rules[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.ValidFrom != b.ValidFrom {
			return a.ValidFrom < b.ValidFrom
		}
		return a.ID < b.ID
	})

	if err := set.checkOverlaps(); err != nil {
		return RuleSet{}, err
	}
	return set, nil
}

// checkOverlaps rejects two rules of the same type claiming the same day.
//
// This is the failure mode SPEC §2.1's discipline exists to prevent, caught
// rather than trusted. Changing a rate means inserting a new row and closing the
// old one's valid_to; forget the second half and both rows cover today, and
// which rate a sale gets depends on row order. That is a rate change that
// applies to some sales and not others, discovered at the filing.
func (s RuleSet) checkOverlaps() error {
	for i := 1; i < len(s.rules); i++ {
		prev, cur := s.rules[i-1], s.rules[i]
		if prev.Type != cur.Type {
			continue
		}
		// Sorted by ValidFrom, so an overlap is exactly an unclosed or
		// late-closed predecessor.
		if prev.ValidTo == "" || prev.ValidTo >= cur.ValidFrom {
			to := prev.ValidTo
			if to == "" {
				to = "(open)"
			}
			return &RuleOverlapError{
				Type: cur.Type, EarlyID: prev.ID, LateID: cur.ID,
				From: cur.ValidFrom, To: to,
			}
		}
	}
	return nil
}

// EntityID is the company these rules belong to, or "" for an empty set.
func (s RuleSet) EntityID() string { return s.entityID }

// Rules returns a copy of the rules, ordered by type then start date.
func (s RuleSet) Rules() []Rule {
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Effective returns the rule of that type in force on a business date.
//
// At most one can match: overlaps were rejected at construction.
func (s RuleSet) Effective(t Type, businessDate string) (Rule, bool) {
	for _, r := range s.rules {
		if r.Type == t && r.AppliesOn(businessDate) {
			return r, true
		}
	}
	return Rule{}, false
}

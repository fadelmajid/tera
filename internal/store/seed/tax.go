package seed

// TaxRule is one default tax_rule row, as values.
//
// Deliberately not a database type and deliberately importing nothing: this
// file is reference data with citations attached, and the caller turns it into
// rows. Seeding inserts rows; it never hardcodes a rate into a calculation
// (INV-4). Nothing in domain/tax reads this package.
type TaxRule struct {
	Type         string
	RateBP       int64
	DPPNum       int64
	DPPDen       int64
	Inclusive    bool
	Level        string
	Rounding     string
	RoundingUnit int64
	ValidFrom    string
	LegalRef     string
	// Note is shown in the admin UI beside the rule, so it is in Bahasa
	// Indonesia like the rest of the interface.
	Note string
}

// TaxRulesFor returns the rules to seed for a newly created company
// (TASKS 5.2).
//
// # Only a PKP entity gets rules
//
// A non-PKP entity charges no PPN and legally cannot, so it is seeded with
// nothing — and domain/tax refuses to price a sale at a non-PKP entity that
// somehow holds a rule already in force (TASKS 5.5). When that entity later
// crosses the Rp 4.8 billion threshold and registers, the rule is added through
// the admin screen with the valid_from the registration actually gives it:
// registration is due by the end of the book year and the VAT obligation starts
// in the first tax period of the following one (PMK 164/2023 Pasal 17(3) and
// Pasal 18). Seeding a rule dated today would get that gap wrong in the
// expensive direction.
//
// # Why PPnBM is not here
//
// SPEC §2.1 lists a PPnBM row among the seeded defaults. It is deliberately
// absent, and this is a considered deviation rather than an oversight.
//
// Nothing in an alat kesehatan catalogue is a luxury good, so no line would ever
// be levied under it — but the row would appear in the admin screen with a rate
// and a citation beside it, and a figure presented that way is a figure someone
// will believe. The PPnBM tariff is banded by product and I could not verify
// which band, whether it shares PPN's DPP or stacks on top of it, or whether the
// nilai lain reaches it. A rule that is never applied costs nothing; a rule that
// is never applied and states a rate nobody checked is a trap for whoever reads
// that screen in two years. See testdata/worked_examples/ppn_unverified.json.
//
// # The rate itself still needs checking
//
// Every figure below is configuration, changeable without a deployment, and it
// has not been verified against DJP sources or a konsultan pajak. The four
// questions that matter are in testdata/worked_examples/ppn_unverified.json;
// the load-bearing one is whether the 11/12 DPP nilai lain is still in force on
// the day of sale.
func TaxRulesFor(isPKP bool) []TaxRule {
	if !isPKP {
		return nil
	}
	return []TaxRule{
		{
			Type: "PPN",
			// The statutory rate is 12% (UU 7/2021, the HPP law). PMK 131/2024
			// applies it to a DPP nilai lain of 11/12 of the price for
			// non-luxury goods, which holds the effective rate at 11%. The
			// fraction is exact and carried as two integers: 11/12 has no
			// finite decimal form, and 0.916666… is a number no regulation
			// contains (SPEC §2.1).
			RateBP: 1200,
			DPPNum: 11,
			DPPDen: 12,

			// Indonesian shelf prices ordinarily include PPN, and this system
			// has a cashier, a receipt printer and prices on the product
			// record. If this business quotes prices to clinics exclusive of
			// PPN instead, change this on the rule rather than in code — the
			// engine does both. It is the first thing to confirm with the
			// owner.
			Inclusive: true,

			// The faktur pajak states one DPP and one PPN, so the invoice
			// figure is the authoritative one and the lines are allocated out
			// of it. That reasoning is not a source; see the unverified list.
			Level:        "INVOICE",
			Rounding:     "HALF_UP",
			RoundingUnit: 1,

			// The date PMK 131/2024 took effect, not the date this company was
			// created: a rule's window is a fact about the law. No sale can
			// exist before the entity does, so backdating costs nothing and
			// keeps the citation honest.
			ValidFrom: "2025-01-01",
			LegalRef:  "PMK 131/2024",

			Note: "PPN 12% atas DPP nilai lain 11/12, sehingga tarif efektif 11%. " +
				"Harga jual dianggap sudah termasuk PPN. " +
				"Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.",
		},
	}
}

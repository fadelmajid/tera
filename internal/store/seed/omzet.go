package seed

// OmzetThreshold is the PKP threshold to seed for a new company, as values.
//
// Like [TaxRule], reference data with a citation attached rather than a
// constant in a calculation (INV-4). internal/domain/omzet has no fallback
// threshold and refuses to measure turnover against a figure nobody configured.
type OmzetThreshold struct {
	AmountIDR int64
	// WatchBP and WarnBP are where the alarm changes colour, in basis points of
	// the threshold. Integers, because a ratio here is not a float either.
	WatchBP int64
	WarnBP  int64

	RegisterByPolicy string
	VATStartsPolicy  string

	ValidFrom string
	LegalRef  string
	// Note is shown beside the row in the admin UI, so it is in Bahasa
	// Indonesia like the rest of the interface.
	Note string
}

// OmzetThresholdFor returns the threshold to seed for a newly created company
// (TASKS 7.1).
//
// # Both companies get one
//
// SPEC §5.3: both are tracked, but the non-PKP entity is the one that can still
// cross — the PKP entity is already registered. Tracking both anyway costs
// nothing and answers the question the owner will actually ask, which is how
// each company is doing against the line rather than how the one that has not
// crossed is doing.
//
// # The deadline policies are the unsettled part
//
// Seeded with SPEC §5.2's reading: registration due by the end of the book year,
// PPN obligation from the first tax period of the following one. That citation
// is PMK 164/2023, which governs the 0.5% final PPh regime rather than PKP
// registration, and the rule usually quoted for registration itself gives a far
// shorter deadline. Both readings are implemented in domain/omzet and the choice
// is this column, so answering the question is a row rather than a release.
//
// See testdata/worked_examples/omzet_unverified.json — it is the first of four
// questions to take to the konsultan pajak.
func OmzetThresholdFor() OmzetThreshold {
	return OmzetThreshold{
		// Rp 4.800.000.000, unchanged since PMK 197/2013.
		AmountIDR: 4_800_000_000,

		// SPEC §5.2's ladder: WATCH from 70%, WARN from 90%, CROSSED at the
		// threshold itself.
		WatchBP: 7_000,
		WarnBP:  9_000,

		RegisterByPolicy: "END_OF_BOOK_YEAR",
		VATStartsPolicy:  "NEXT_BOOK_YEAR_FIRST_PERIOD",

		// The date the figure took effect, not the date the company was
		// created: a threshold's window is a fact about the law. No turnover
		// can predate the company, so backdating costs nothing and keeps the
		// citation honest.
		ValidFrom: "2014-01-01",
		LegalRef:  "PMK 197/2013",

		Note: "Batas pengukuhan PKP Rp 4.800.000.000 per tahun buku, dihitung kumulatif " +
			"dan diulang setiap tahun buku — bukan 12 bulan berjalan. " +
			"Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.",
	}
}

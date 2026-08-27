package escpos

// Receipt is what gets printed, already formatted.
//
// Every money field is a string on purpose. Rupiah is int64 everywhere it is
// computed (INV-1), and it is rendered exactly once -- by money.IDR.String() --
// before it reaches this package. A receipt that formatted its own currency
// would be a second implementation of that formatting, and the two would
// eventually disagree in front of a customer.
//
// This package therefore imports no money type and does no arithmetic. It lays
// out text.
type Receipt struct {
	ShopName string
	// Header carries the address, phone, and NPWP where the company has one.
	// What must legally appear is a question for the owner's tax consultant,
	// so it is configuration rather than something hardcoded here.
	//
	// Each entry is one printed line and is truncated to the paper width rather
	// than wrapped. Split a long address across two entries: letting the
	// printer wrap would push a figure onto a line of its own further down and
	// break the money column.
	Header []string

	InvoiceNo string
	DateTime  string
	Cashier   string
	Customer  string

	Lines []ReceiptLine

	Subtotal string
	Discount string
	// PPN is empty for a non-PKP company, which cannot charge it at all
	// (SPEC §2.3). Empty means the line is not printed, not that it is zero.
	PPN string
	// PPNInclusive changes the label from "PPN" to "Termasuk PPN".
	//
	// Under inclusive pricing the tax is already inside the total, and a line
	// reading "PPN Rp 11.000" directly above "TOTAL Rp 111.000" reads as if it
	// were added on top -- so the receipt appears not to add up, to the one
	// person standing there with the money. One word fixes it.
	PPNInclusive bool
	Total        string

	Payments []ReceiptPayment
	Change   string

	Footer []string
}

// ReceiptLine is one item as printed.
type ReceiptLine struct {
	Name      string
	Qty       int64
	UnitPrice string
	Discount  string
	Total     string
}

// ReceiptPayment is one tender as printed. Reference carries a transfer or
// QRIS reference where the cashier captured one (R9.10).
type ReceiptPayment struct {
	Method    string
	Amount    string
	Reference string
}

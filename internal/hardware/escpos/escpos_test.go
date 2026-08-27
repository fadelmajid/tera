package escpos_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/hardware/escpos"
)

func sample() *escpos.Receipt {
	return &escpos.Receipt{
		ShopName:  "PT Sehat Sentosa",
		Header:    []string{"Jl. Kesehatan 12, Surabaya", "NPWP 01.234.567.8-901.000"},
		InvoiceNo: "20261015-0007",
		DateTime:  "15/10/2026 14:30",
		Cashier:   "Budi",
		Lines: []escpos.ReceiptLine{
			{Name: "Sarung Tangan Steril", Qty: 2, UnitPrice: "Rp 25.000", Total: "Rp 50.000"},
			{Name: "Masker Bedah", Qty: 1, UnitPrice: "Rp 15.000", Total: "Rp 15.000", Discount: "Rp 1.000"},
		},
		Subtotal: "Rp 65.000",
		Discount: "Rp 1.000",
		Total:    "Rp 64.000",
		Payments: []escpos.ReceiptPayment{{Method: "TUNAI", Amount: "Rp 70.000"}},
		Change:   "Rp 6.000",
		Footer:   []string{"Terima kasih"},
	}
}

// text strips the control sequences so the layout can be read.
func text(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		var b strings.Builder
		for i := 0; i < len(line); i++ {
			c := line[i]
			// Skip ESC/GS sequences: two or three bytes of payload each.
			if c == 0x1B || c == 0x1D {
				i += 2
				continue
			}
			if c >= 0x20 || c == '\t' {
				b.WriteByte(c)
			}
		}
		out = append(out, b.String())
	}
	return out
}

// A receipt is read by a person holding a narrow strip of paper. Columns must
// line up or the figures are unreadable, and the total must be findable at a
// glance.
func TestReceiptFitsThePaperAndColumnsLineUp(t *testing.T) {
	t.Parallel()

	for _, width := range []int{32, 48} {
		p := escpos.NewWriter(escpos.Config{Width: width}, &bytes.Buffer{})
		lines := text(p.Render(sample()))

		for i, line := range lines {
			if len([]rune(line)) > width {
				t.Errorf("width %d, line %d is %d chars: %q", width, i, len([]rune(line)), line)
			}
		}

		// The money column is flush right, so every figure ends in the same
		// place.
		var totalLine string
		for _, line := range lines {
			if strings.HasPrefix(line, "TOTAL") {
				totalLine = line
			}
		}
		if totalLine == "" {
			t.Fatalf("width %d: no TOTAL line", width)
		}
		if len([]rune(totalLine)) != width {
			t.Errorf("width %d: TOTAL line is %d chars, want the full width so the figure is flush right",
				width, len([]rune(totalLine)))
		}
		if !strings.HasSuffix(totalLine, "Rp 64.000") {
			t.Errorf("width %d: TOTAL line %q does not end with the amount", width, totalLine)
		}
	}
}

func TestReceiptCarriesEverythingAPersonNeeds(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 32}, &bytes.Buffer{})
	body := strings.Join(text(p.Render(sample())), "\n")

	for _, want := range []string{
		"PT Sehat Sentosa",
		"NPWP 01.234.567.8-901.000",
		"20261015-0007", // findable: the invoice a person can quote
		"15/10/2026 14:30",
		"Sarung Tangan St", // truncated to the paper, not wrapped mid-word
		"Masker Bedah",
		"Rp 64.000",
		"Kembali",
		"Terima kasih",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("receipt is missing %q:\n%s", want, body)
		}
	}
}

// A non-PKP company cannot charge PPN at all (SPEC §2.3). An empty field means
// the line is absent, not printed as zero -- a receipt showing "PPN Rp 0" from
// a company that legally cannot charge it invites exactly the wrong question.
func TestPPNLineIsAbsentWhenThereIsNone(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 32}, &bytes.Buffer{})
	r := sample()
	r.PPN = ""
	if body := strings.Join(text(p.Render(r)), "\n"); strings.Contains(body, "PPN") {
		t.Errorf("a non-PKP receipt printed a PPN line:\n%s", body)
	}

	r.PPN = "Rp 6.400"
	if body := strings.Join(text(p.Render(r)), "\n"); !strings.Contains(body, "Rp 6.400") {
		t.Error("a PKP receipt did not print its PPN")
	}
}

// Under inclusive pricing the tax is already inside the total, and the receipt
// has to say so.
//
// "PPN Rp 6.400" printed directly above "TOTAL Rp 64.000" reads as if it were
// added on top, so the receipt appears not to add up -- to the one person
// standing there with the money, at the moment they are handing it over. The
// arithmetic is right either way; the label is what makes it legible.
func TestAnInclusivePPNLineSaysSo(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 32}, &bytes.Buffer{})

	r := sample()
	r.PPN, r.PPNInclusive = "Rp 6.400", true
	body := strings.Join(text(p.Render(r)), "\n")
	if !strings.Contains(body, "Termasuk PPN") {
		t.Errorf("an inclusive receipt does not say the PPN is included:\n%s", body)
	}

	r.PPNInclusive = false
	body = strings.Join(text(p.Render(r)), "\n")
	if strings.Contains(body, "Termasuk PPN") {
		t.Errorf("an exclusive receipt claims the PPN was already included:\n%s", body)
	}
	if !strings.Contains(body, "PPN") {
		t.Errorf("an exclusive receipt lost its PPN line:\n%s", body)
	}
}

// Product names are user input. A stray escape byte in a name would otherwise
// reconfigure the printer mid-receipt -- double-height for the rest of the day,
// or worse, a cut in the middle of the items.
func TestControlBytesInNamesCannotReachThePrinter(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 32}, &bytes.Buffer{})
	r := sample()
	r.Lines[0].Name = "Masker\x1B\x40\x1DV\x00 Bedah"
	r.ShopName = "Toko\nPalsu"

	raw := p.Render(r)

	// Exactly one initialise (the one Render writes) and one cut (at the end).
	if n := bytes.Count(raw, []byte{0x1B, 0x40}); n != 1 {
		t.Errorf("found %d initialise sequences, want 1 -- a product name reached the printer as a command", n)
	}
	if n := bytes.Count(raw, []byte{0x1D, 0x56}); n != 1 {
		t.Errorf("found %d cut sequences, want 1 -- a product name could cut the paper mid-receipt", n)
	}
	// And the newline smuggled into the shop name did not split it into two.
	if bytes.Contains(raw, []byte("Toko\nPalsu")) {
		t.Error("a newline in user input split a line")
	}
}

// The drawer is wired through the printer, so popping it is a print command
// with no paper. Sending it with the receipt means one round trip, and the
// drawer opens as the paper cuts rather than a beat later.
func TestDrawerPulse(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := escpos.NewWriter(escpos.Config{Width: 32, DrawerPin: 0}, &buf)

	if err := p.OpenDrawer(context.Background()); err != nil {
		t.Fatalf("open drawer: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, []byte{0x1B, 0x70, 0x00, 0x19, 0xFA}) {
		t.Errorf("drawer pulse = % x", got)
	}

	buf.Reset()
	if err := p.PrintAndOpenDrawer(context.Background(), sample()); err != nil {
		t.Fatalf("print and open: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte{0x1B, 0x70, 0x00, 0x19, 0xFA}) {
		t.Error("the drawer pulse did not follow the receipt in the same write")
	}

	// Pin 1 for the other common wiring.
	buf.Reset()
	p1 := escpos.NewWriter(escpos.Config{Width: 32, DrawerPin: 1}, &buf)
	if err := p1.OpenDrawer(context.Background()); err != nil {
		t.Fatalf("open drawer: %v", err)
	}
	if buf.Bytes()[2] != 1 {
		t.Errorf("drawer pin = %d, want 1", buf.Bytes()[2])
	}
}

// A shop with no printer configured must still be able to trade. The sale is
// already committed by the time printing is attempted; a missing printer is a
// message, not a failed transaction.
func TestUnconfiguredPrinterFailsSoftly(t *testing.T) {
	t.Parallel()

	p := escpos.New(escpos.Config{})
	if p.Configured() {
		t.Error("a printer with no address or device reported itself configured")
	}
	if err := p.Print(context.Background(), sample()); !errors.Is(err, escpos.ErrNotConfigured) {
		t.Errorf("got %v, want ErrNotConfigured", err)
	}
	if err := p.OpenDrawer(context.Background()); !errors.Is(err, escpos.ErrNotConfigured) {
		t.Errorf("got %v, want ErrNotConfigured", err)
	}
	// Rendering still works, so the receipt can be shown on screen.
	if len(p.Render(sample())) == 0 {
		t.Error("an unconfigured printer could not render a receipt for display")
	}
}

// Long product names are truncated to the paper rather than wrapped, because a
// wrap pushes the price column onto its own line and the receipt stops being
// readable as a table.
func TestLongNamesTruncateRatherThanWrap(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 32}, &bytes.Buffer{})
	r := sample()
	r.Lines[0].Name = strings.Repeat("Sarung Tangan Steril Panjang ", 4)

	for i, line := range text(p.Render(r)) {
		if len([]rune(line)) > 32 {
			t.Fatalf("line %d overflowed: %q", i, line)
		}
	}
}

// A figure too long to share its line gets its own, right-aligned, rather than
// letting the printer wrap a number in half.
func TestOversizedAmountsGetTheirOwnLine(t *testing.T) {
	t.Parallel()

	p := escpos.NewWriter(escpos.Config{Width: 20}, &bytes.Buffer{})
	r := sample()
	r.Total = "Rp 12.345.678.901"

	lines := text(p.Render(r))
	var found bool
	for i, line := range lines {
		if len([]rune(line)) > 20 {
			t.Fatalf("line %d overflowed: %q", i, line)
		}
		if strings.Contains(line, "Rp 12.345.678.901") {
			found = true
		}
	}
	if !found {
		t.Error("the amount was broken across lines instead of being given its own")
	}
}

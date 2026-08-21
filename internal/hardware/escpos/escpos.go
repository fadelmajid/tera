package escpos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
	"unicode"
)

// ESC/POS control sequences.
//
// Only the portable subset is used here: initialise, alignment, emphasis,
// double-height, feed, cut, and the drawer pulse. Every thermal printer sold
// as "ESC/POS compatible" implements these. The parts that vary by
// manufacturer -- logo upload, barcode rendering, code-page switching, partial
// versus full cut -- are deliberately avoided or made configurable, because the
// shop's actual printer model is not known yet and a driver that only works on
// one vendor's firmware is worse than one that prints plainly on all of them.
var (
	initialise   = []byte{0x1B, 0x40}       // ESC @
	alignLeft    = []byte{0x1B, 0x61, 0x00} // ESC a 0
	alignCentre  = []byte{0x1B, 0x61, 0x01} // ESC a 1
	alignRight   = []byte{0x1B, 0x61, 0x02} // ESC a 2
	emphasisOn   = []byte{0x1B, 0x45, 0x01} // ESC E 1
	emphasisOff  = []byte{0x1B, 0x45, 0x00} // ESC E 0
	doubleHeight = []byte{0x1D, 0x21, 0x01} // GS ! 1
	normalSize   = []byte{0x1D, 0x21, 0x00} // GS ! 0
	fullCut      = []byte{0x1D, 0x56, 0x00} // GS V 0
	partialCut   = []byte{0x1D, 0x56, 0x01} // GS V 1

	// ESC p m t1 t2 -- the drawer is wired to one of two pins on the printer.
	// Which one is a fact about the cable, not about the software, so the pin
	// is configurable and defaults to 0, which is the common wiring.
	drawerPulse = func(pin byte) []byte { return []byte{0x1B, 0x70, pin, 0x19, 0xFA} }
)

// ErrNotConfigured is returned when printing is attempted with no printer set
// up. It is not a failure of the sale: the transaction is already committed,
// and a shop with no printer configured should still be able to trade.
var ErrNotConfigured = errors.New("escpos: printer belum dikonfigurasi")

// Config describes the attached hardware.
//
// The printer is attached to the server machine, which is where the cashier
// sits, so printing is server-side and clients never touch it
// (ARCHITECTURE §1). None of this reaches the internet: TCP here means the
// shop LAN or a USB-to-serial device node (INV-11).
type Config struct {
	// Addr is a TCP host:port, conventionally port 9100. Empty when the
	// printer is attached over USB.
	Addr string
	// Device is a character device path for a USB printer, e.g.
	// /dev/usb/lp0 on Linux or /dev/tty.usbserial-* on macOS.
	Device string
	// Width is the printable width in characters. 32 for 58mm paper, 48 for
	// 80mm. Wrong here and every column in the receipt is ragged, which is why
	// it is configuration rather than a guess.
	Width int
	// DrawerPin is 0 or 1, matching how the drawer cable is wired.
	DrawerPin byte
	// PartialCut leaves the receipt attached by a tab. Some models only
	// implement one of the two cuts; if the paper does not cut, try the other.
	PartialCut bool
	// Timeout bounds a write. A thermal printer that has run out of paper can
	// block forever, and a cashier waiting on a hung socket is worse than a
	// missing receipt.
	Timeout time.Duration
}

func (c Config) width() int {
	if c.Width <= 0 {
		return 32 // 58mm paper, the common small-shop roll
	}
	return c.Width
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 5 * time.Second
	}
	return c.Timeout
}

// Printer talks to the hardware.
type Printer struct {
	cfg Config
	// dial is swapped in tests. The real implementations open a socket or a
	// device node; neither is reachable from a test machine.
	//
	// It takes a context so a print that hangs -- a printer out of paper holds
	// the socket open indefinitely -- is cancelled when the request that asked
	// for it goes away, rather than pinning a connection until the timeout.
	dial func(context.Context) (io.WriteCloser, error)
}

// New builds a printer from configuration.
//
// Returns a printer that reports ErrNotConfigured from every method when
// neither Addr nor Device is set, rather than a nil to be checked at each call
// site. A shop with no printer yet must still be able to ring sales.
func New(cfg Config) *Printer {
	p := &Printer{cfg: cfg}
	switch {
	case strings.TrimSpace(cfg.Addr) != "":
		p.dial = func(ctx context.Context) (io.WriteCloser, error) {
			d := net.Dialer{Timeout: cfg.timeout()}
			conn, err := d.DialContext(ctx, "tcp", cfg.Addr)
			if err != nil {
				return nil, fmt.Errorf("escpos: dial %s: %w", cfg.Addr, err)
			}
			if err := conn.SetDeadline(time.Now().Add(cfg.timeout())); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("escpos: deadline: %w", err)
			}
			return conn, nil
		}
	case strings.TrimSpace(cfg.Device) != "":
		p.dial = func(_ context.Context) (io.WriteCloser, error) {
			f, err := os.OpenFile(cfg.Device, os.O_WRONLY, 0)
			if err != nil {
				return nil, fmt.Errorf("escpos: open %s: %w", cfg.Device, err)
			}
			return f, nil
		}
	}
	return p
}

// NewWriter builds a printer that writes wherever w points.
//
// This is how the receipt is exercised in tests and how a shop without
// hardware can still see what would have printed.
func NewWriter(cfg Config, w io.Writer) *Printer {
	return &Printer{cfg: cfg, dial: func(context.Context) (io.WriteCloser, error) { return nopCloser{w}, nil }}
}

// Configured reports whether any hardware is attached.
func (p *Printer) Configured() bool { return p.dial != nil }

// Width is the printable width in characters.
func (p *Printer) Width() int { return p.cfg.width() }

// send opens the device, writes, and closes it.
//
// A connection per receipt rather than a held one. Thermal printers drop idle
// sockets, and a shop PC that sleeps overnight would wake to a dead connection
// and a cashier who cannot print; reconnecting each time costs milliseconds and
// removes a whole class of morning support call.
func (p *Printer) send(ctx context.Context, payload []byte) error {
	if p.dial == nil {
		return ErrNotConfigured
	}
	w, err := p.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("escpos: write: %w", err)
	}
	return nil
}

// Print renders a receipt and sends it.
func (p *Printer) Print(ctx context.Context, r *Receipt) error { return p.send(ctx, p.Render(r)) }

// OpenDrawer sends the pulse that pops the till.
//
// The drawer is wired through the printer, so this is a print command with no
// paper. It is separate from Print because the drawer also opens for a cash
// refund and at the end of the day, when no receipt is involved.
func (p *Printer) OpenDrawer(ctx context.Context) error {
	return p.send(ctx, drawerPulse(p.cfg.DrawerPin))
}

// PrintAndOpenDrawer sends the receipt and pops the till in one connection.
//
// One round trip, so the drawer opens as the paper cuts rather than a beat
// later -- which is what a cashier expects and what stops them pressing the
// button twice.
func (p *Printer) PrintAndOpenDrawer(ctx context.Context, r *Receipt) error {
	return p.send(ctx, append(p.Render(r), drawerPulse(p.cfg.DrawerPin)...))
}

// --- rendering --------------------------------------------------------------

// Render turns a receipt into bytes without touching hardware.
//
// Separated from sending so the layout can be tested exactly, and so a shop
// with no printer configured can still be shown what would have printed.
func (p *Printer) Render(r *Receipt) []byte {
	w := p.cfg.width()
	var b bytes.Buffer

	b.Write(initialise)

	// Header: shop name large, the rest plain.
	b.Write(alignCentre)
	if r.ShopName != "" {
		b.Write(doubleHeight)
		b.Write(emphasisOn)
		// Double-height is also double-width on every ESC/POS printer, so the
		// shop name gets half the columns.
		writeLine(&b, truncate(r.ShopName, w/2))
		b.Write(emphasisOff)
		b.Write(normalSize)
	}
	for _, line := range r.Header {
		writeLine(&b, truncate(line, w))
	}
	b.Write(alignLeft)
	rule(&b, w)

	// Which sale this is, and when. A person holding the paper has to be able
	// to find the transaction.
	twoCol(&b, w, "No.", r.InvoiceNo)
	twoCol(&b, w, "Tanggal", r.DateTime)
	if r.Cashier != "" {
		twoCol(&b, w, "Kasir", r.Cashier)
	}
	if r.Customer != "" {
		twoCol(&b, w, "Pelanggan", r.Customer)
	}
	rule(&b, w)

	for _, line := range r.Lines {
		writeLine(&b, truncate(line.Name, w))
		// qty x price on the left, the line total right-aligned, so the column
		// of figures reads straight down.
		left := fmt.Sprintf("  %d x %s", line.Qty, line.UnitPrice)
		twoCol(&b, w, left, line.Total)
		if line.Discount != "" {
			twoCol(&b, w, "  Diskon", "-"+line.Discount)
		}
	}

	rule(&b, w)
	if r.Discount != "" {
		twoCol(&b, w, "Subtotal", r.Subtotal)
		twoCol(&b, w, "Diskon nota", "-"+r.Discount)
	}
	if r.PPN != "" {
		twoCol(&b, w, "PPN", r.PPN)
	}

	b.Write(emphasisOn)
	twoCol(&b, w, "TOTAL", r.Total)
	b.Write(emphasisOff)

	for _, pay := range r.Payments {
		twoCol(&b, w, pay.Method, pay.Amount)
		if pay.Reference != "" {
			writeLine(&b, "  Ref: "+pay.Reference)
		}
	}
	if r.Change != "" {
		twoCol(&b, w, "Kembali", r.Change)
	}

	if len(r.Footer) > 0 {
		b.WriteByte('\n')
		b.Write(alignCentre)
		for _, line := range r.Footer {
			writeLine(&b, truncate(line, w))
		}
		b.Write(alignLeft)
	}

	// Feed past the cutter before cutting, or the last lines are still inside
	// the mechanism when the blade comes down.
	b.WriteString("\n\n\n\n")
	if p.cfg.PartialCut {
		b.Write(partialCut)
	} else {
		b.Write(fullCut)
	}
	return b.Bytes()
}

// twoCol prints a label on the left and a value flush right.
func twoCol(b *bytes.Buffer, width int, left, right string) {
	gap := width - runeLen(left) - runeLen(right)
	if gap < 1 {
		// Too long to share a line: give the value its own, right-aligned,
		// rather than letting the printer wrap mid-number.
		writeLine(b, truncate(left, width))
		b.Write(alignRight)
		writeLine(b, truncate(right, width))
		b.Write(alignLeft)
		return
	}
	writeLine(b, left+strings.Repeat(" ", gap)+right)
}

func rule(b *bytes.Buffer, width int) { writeLine(b, strings.Repeat("-", width)) }

// writeLine emits one line, stripped of anything that would be read as a
// control sequence. Product names come from user input, and a stray 0x1B in a
// name would otherwise reconfigure the printer mid-receipt.
func writeLine(b *bytes.Buffer, s string) {
	b.WriteString(sanitise(s))
	b.WriteByte('\n')
}

func sanitise(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func runeLen(s string) int { return len([]rune(sanitise(s))) }

func truncate(s string, width int) string {
	s = sanitise(s)
	rs := []rune(s)
	if len(rs) <= width {
		return s
	}
	if width <= 1 {
		return string(rs[:width])
	}
	return string(rs[:width-1]) + "."
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

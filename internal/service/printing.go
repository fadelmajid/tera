package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/hardware/escpos"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// ErrPrinterNotConfigured is returned when no printer is attached. Re-exported
// so callers do not have to import the hardware package to recognise it.
var ErrPrinterNotConfigured = escpos.ErrNotConfigured

// Printing turns a finished sale into a receipt and sends it to the hardware.
//
// It sits in the service layer because it reads sales, not because it decides
// anything: the printer is attached to the server machine, which is where the
// cashier sits, and browsers never touch it (ARCHITECTURE §1). This path must
// work with the internet disconnected -- it opens a socket on the shop LAN or a
// device node, and nothing else (INV-11).
//
// Printing is deliberately not part of the sale's transaction. The sale is
// already committed by the time a receipt is attempted; paper jamming must
// never roll back a sale the customer has paid for. A failed print is a message
// and a reprint button.
type Printing struct {
	q       *gen.Queries
	printer *escpos.Printer
	footer  []string
	now     func() time.Time
}

// NewPrinting builds the service. footer is the thank-you text at the bottom of
// every receipt.
func NewPrinting(q *gen.Queries, printer *escpos.Printer, footer []string, now func() time.Time) *Printing {
	if now == nil {
		now = time.Now
	}
	return &Printing{q: q, printer: printer, footer: footer, now: now}
}

// Configured reports whether a printer is attached.
func (p *Printing) Configured() bool { return p.printer != nil && p.printer.Configured() }

// Receipt assembles what would be printed for a sale, without printing it.
//
// Every rupiah figure is rendered here, once, by money.IDR.String(). The
// hardware package receives strings and does no arithmetic -- a second
// implementation of currency formatting is a second thing to get wrong in front
// of a customer.
func (p *Printing) Receipt(ctx context.Context, entityID, saleID string) (*escpos.Receipt, error) {
	sale, err := p.q.GetSale(ctx, saleID)
	if err != nil || sale.EntityID != entityID {
		return nil, fmt.Errorf("%w: penjualan tidak ditemukan", ErrNotFound)
	}

	entity, err := p.q.GetLegalEntity(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("%w: perusahaan tidak ditemukan", ErrNotFound)
	}
	loc, err := time.LoadLocation(entity.Timezone)
	if err != nil {
		loc = time.UTC
	}

	lines, err := p.q.ListSaleLines(ctx, saleID)
	if err != nil {
		return nil, fmt.Errorf("service: sale lines: %w", err)
	}
	payments, err := p.q.ListSalePayments(ctx, saleID)
	if err != nil {
		return nil, fmt.Errorf("service: sale payments: %w", err)
	}

	header := make([]string, 0, 3)
	if entity.Address != nil && *entity.Address != "" {
		header = append(header, *entity.Address)
	}
	if entity.Phone != nil && *entity.Phone != "" {
		header = append(header, "Telp "+*entity.Phone)
	}
	// A non-PKP company has no NPWP to print, and printing one it does not
	// have would misrepresent its tax status on paper.
	if entity.Npwp != nil && *entity.Npwp != "" {
		header = append(header, "NPWP "+*entity.Npwp)
	}

	r := &escpos.Receipt{
		ShopName:  entity.Name,
		Header:    header,
		InvoiceNo: sale.InvoiceNo,
		DateTime:  time.Unix(sale.OccurredAt, 0).In(loc).Format("02/01/2006 15:04"),
		Total:     money.IDR(sale.TotalIdr).String(),
		Footer:    p.footer,
	}

	if sale.CustomerID != nil {
		if c, err := p.q.GetCustomer(ctx, *sale.CustomerID); err == nil {
			r.Customer = c.Name
		}
	}
	if sale.CreatedBy != nil {
		if u, err := p.q.GetUser(ctx, *sale.CreatedBy); err == nil {
			r.Cashier = u.FullName
		}
	}

	for _, l := range lines {
		line := escpos.ReceiptLine{
			Name: l.ProductName, Qty: l.Qty,
			UnitPrice: money.IDR(l.UnitPriceIdr).String(),
			Total:     money.IDR(l.GrossIdr).String(),
		}
		if l.LineDiscountIdr > 0 {
			line.Discount = money.IDR(l.LineDiscountIdr).String()
		}
		r.Lines = append(r.Lines, line)
	}

	if sale.DiscountIdr > 0 {
		r.Subtotal = money.IDR(sale.GrossIdr).String()
		r.Discount = money.IDR(sale.DiscountIdr).String()
	}
	// Empty means the line is not printed. A non-PKP company cannot charge PPN
	// at all (SPEC §2.3), and "PPN Rp 0" on its receipt invites exactly the
	// wrong question from a customer.
	if sale.PpnIdr > 0 {
		r.PPN = money.IDR(sale.PpnIdr).String()
	}

	var tendered money.IDR
	for _, pay := range payments {
		amount := money.IDR(pay.AmountIdr)
		tendered = tendered.Add(amount)
		rp := escpos.ReceiptPayment{Method: pay.Method, Amount: amount.String()}
		if pay.Reference != nil {
			rp.Reference = *pay.Reference
		}
		r.Payments = append(r.Payments, rp)
	}
	if change := tendered.Sub(money.IDR(sale.TotalIdr)); change.IsPositive() {
		r.Change = change.String()
	}

	if sale.Status == "VOID" {
		r.Footer = append([]string{"*** DIBATALKAN ***"}, r.Footer...)
	}
	return r, nil
}

// PrintSale sends a sale's receipt to the printer and pops the drawer.
//
// The drawer opens on the same write as the receipt, so it pops as the paper
// cuts rather than a beat later -- which is what a cashier expects and what
// stops them pressing the button twice.
func (p *Printing) PrintSale(ctx context.Context, entityID, saleID string, openDrawer bool) error {
	receipt, err := p.Receipt(ctx, entityID, saleID)
	if err != nil {
		return err
	}
	if !p.Configured() {
		return ErrPrinterNotConfigured
	}
	if openDrawer {
		return p.printer.PrintAndOpenDrawer(ctx, receipt)
	}
	return p.printer.Print(ctx, receipt)
}

// OpenDrawer pops the till without printing: a cash refund, or counting up at
// the end of the day.
func (p *Printing) OpenDrawer(ctx context.Context) error {
	if !p.Configured() {
		return ErrPrinterNotConfigured
	}
	return p.printer.OpenDrawer(ctx)
}

// Preview renders a receipt as plain text, for a shop with no printer attached
// yet and for checking the layout without wasting paper.
func (p *Printing) Preview(ctx context.Context, entityID, saleID string) (string, error) {
	receipt, err := p.Receipt(ctx, entityID, saleID)
	if err != nil {
		return "", err
	}

	width := 32
	if p.printer != nil {
		width = p.printer.Width()
	}
	raw := escpos.NewWriter(escpos.Config{Width: width}, nil).Render(receipt)
	return stripControl(string(raw)), nil
}

// stripControl removes the ESC/POS sequences so the layout is readable on
// screen. Only for display; the printer gets the real bytes.
func stripControl(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == 0x1B || c == 0x1D:
			i += 2 // skip the sequence's parameters
		case c == '\n' || c >= 0x20:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// PrinterFromEnv builds a printer from configuration.
//
// The shop's actual printer model is not known yet, so this exposes the two
// facts that vary and nothing else: how it is attached, and how wide the paper
// is. Everything the driver sends is the portable ESC/POS subset.
type PrinterConfig struct {
	Addr       string
	Device     string
	Width      int
	DrawerPin  byte
	PartialCut bool
}

// NewPrinter builds the hardware handle.
func NewPrinter(cfg PrinterConfig) *escpos.Printer {
	return escpos.New(escpos.Config{
		Addr: cfg.Addr, Device: cfg.Device, Width: cfg.Width,
		DrawerPin: cfg.DrawerPin, PartialCut: cfg.PartialCut,
	})
}

// IsPrinterError reports whether err came from the hardware rather than from
// the sale. Callers use it to keep a print failure from reading as a failed
// transaction.
func IsPrinterError(err error) bool {
	return errors.Is(err, ErrPrinterNotConfigured) || strings.Contains(err.Error(), "escpos:")
}

// Package escpos drives the thermal receipt printer and the cash drawer.
//
// ESC/POS over TCP:9100 or USB. The printer is attached to the server machine,
// which is where the cashier sits, so printing is server-side — clients are
// browsers and never touch the hardware (ARCHITECTURE §1).
//
// The drawer opens on a pulse sent through the printer.
//
// This path must work with the internet disconnected (INV-11): a sale completes
// and a receipt prints on the LAN alone. TASKS 2.11 is a real test, not a
// checkbox.
package escpos

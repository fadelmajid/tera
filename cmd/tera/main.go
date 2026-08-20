// Command tera is the whole system in one binary: HTTP API, embedded SPA,
// domain core, and the ESC/POS driver for the printer and cash drawer.
//
// Deployment is a LAN server (ARCHITECTURE §1). One machine holds the database
// and serves every other device over the shop's own network, so the listener
// binds 0.0.0.0 and never localhost. "Offline" here means no internet, not no
// network (INV-11).
//
// Scaffold only. The HTTP server arrives in TASKS 0.5.
package main

import (
	"fmt"
	"os"

	// Embed the IANA timezone database. Book-year and business-day boundaries
	// are resolved in the entity's timezone (INV-5, DECISIONS D-005), and the
	// server is a shop PC that may carry no system zoneinfo — or be restored
	// onto a laptop that doesn't (R8.7). Without this, LoadLocation("Asia/Jakarta")
	// fails, the fallback is UTC, and a 23:30 WIB sale on 31 December books into
	// the wrong year: silently, once a year, in the figure the omzet alarm reads.
	_ "time/tzdata"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Fprintf(os.Stdout, "tera %s\n", version)
	fmt.Fprintln(os.Stdout, "scaffold: no server yet — see docs/TASKS.md 0.5")
}

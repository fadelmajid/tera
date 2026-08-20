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
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Fprintf(os.Stdout, "tera %s\n", version)
	fmt.Fprintln(os.Stdout, "scaffold: no server yet — see docs/TASKS.md 0.5")
}

// Package web holds the browser client and embeds its build output.
//
// The built SPA ships inside the binary (ARCHITECTURE §1), so installing is
// still "copy one file, run it, everyone opens a URL" — no static directory to
// forget alongside it, and nothing to serve from disk on a shop PC.
package web

import (
	"embed"
	"io/fs"
)

// dist is the Vite build output. The all: prefix includes dotfiles, which is
// what lets a fresh clone compile: web/dist holds only .gitkeep until someone
// runs `make web`, and //go:embed fails at compile time on a pattern that
// matches nothing.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the built SPA and whether a real build is present.
//
// A binary built without running the front-end build is a legitimate state —
// `make build` alone does it, and every Go test does — so this reports the
// absence instead of failing. The server serves an explanatory page in that
// case rather than a blank screen.
func Assets() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}

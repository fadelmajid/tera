// Package http is the HTTP transport: handlers, middleware, and the embedded SPA.
//
// It binds 0.0.0.0, never localhost. Clients are browsers on the shop LAN, not
// processes on this machine (ARCHITECTURE §1, R8.4).
//
// Responsibilities that live here and nowhere else:
//
//   - idempotency on client_request_id — a replay returns the original result,
//     it does not ring a second sale (INV-6)
//   - authorization by role: owner, manager, staff. Staff cannot edit past
//     transactions (R7.1, R13.2)
//   - entity scoping — a user may hold different roles in each company (R13.4)
//
// Money crosses the wire as a JSON number, typed on the TypeScript side as a
// branded IDR so it cannot be confused with a rate or a quantity. Never a
// float, never a formatted string that has to be parsed back (INV-1, SPEC §1).
//
// The React build is embedded into the binary via embed.FS, so deployment stays
// one file to copy.
//
// UI copy is in Bahasa Indonesia. Code and comments are in English.
package http

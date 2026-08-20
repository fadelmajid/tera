# DECISIONS — Tera

Resolved choices, with the reasoning. REQUIREMENTS §11 lists what is still open;
this is its counterpart. Append, don't rewrite — a superseded decision gets a new
row citing the one it replaces.

| # | Decision | Date | Affects |
|---|---|---|---|
| D-001 | Module path is `github.com/fadelmajid/tera` | 2026-08-20 | everything |
| D-002 | Go floor 1.23; tool versions pinned in the Makefile | 2026-08-20 | build, CI |
| D-003 | Identifiers are **UUIDv7**, stored as canonical lowercase `TEXT` | 2026-08-20 | every migration |
| D-004 | All three roles — owner, manager, staff — exist at go-live | 2026-08-20 | TASKS 0.6 |

---

## D-001 — Module path

`github.com/fadelmajid/tera`, private repo under the author's account. Changing
it later is a repo-wide rewrite plus a `.golangci.yml` depguard edit, so it was
settled before the first commit.

## D-002 — Go 1.23 floor, tools pinned in the Makefile

`go.mod` declares `go 1.23.0`, matching the stack in CLAUDE.md, and CI builds on
exactly that so the floor is real rather than aspirational. Tool versions
(golangci-lint, sqlc, goose) are pinned as Makefile variables and fetched with
`go run <pkg>@<version>` when no local binary is present.

The alternative — go.mod `tool` directives — is tidier but requires Go 1.24+,
which would raise the floor for no benefit the project currently needs.

## D-003 — UUIDv7 as TEXT

SQLite has no UUID type, so the choice is a storage encoding plus a version.

**Version 7, not 4.** FIFO consumption is oldest-first and breaks ties on
`acquired_at` by `id`, for determinism (SPEC §3.3). Two layers acquired in the
same second is ordinary — one purchase, several lines. With v4 that tiebreak is
stable but arbitrary: the layer created second can be consumed first. With v7 the
id is time-ordered, so the tiebreak resolves to insertion order, which is what
FIFO means. The invariant is unaffected either way; v7 just makes it agree with
intuition, and it keeps a family from being told the cheaper layer went first
when it didn't.

v7 also clusters index writes instead of scattering them, though at this scale
that's a rounding error and not the reason.

**TEXT, not BLOB.** The canonical 36-character form costs 20 bytes more per id
than a 16-byte blob. At 1,000 SKUs that is noise, and it buys a database anyone
can open in `sqlite3` and read — which matters for a system whose backup story is
"copy the file" and whose recovery story is "restore onto any laptop" (R8.7).

Generated with `github.com/google/uuid` (`uuid.NewV7`). Lowercase, canonical
hyphenated form, enforced at the boundary — mixed case in a TEXT primary key is
a silent duplicate waiting to happen.

## D-004 — Three roles at go-live

owner, manager, staff — all three exist from TASKS 0.6, per R13.2. R7.1 depends
on the staff/manager distinction existing, so deferring the role would mean
retrofitting authorization into every mutating handler later.

**Still open, and separate:** whether staff can *see* other owners' margin
figures (R13.3). That is a visibility rule inside the Phase 3 UI, not a question
of whether the role exists. Sensitive in both directions in a family business —
needs the user's answer before the margin screen ships.

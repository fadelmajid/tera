# queries

Hand-written SQL, compiled to type-safe Go by sqlc into `../gen`.

```bash
make sqlc          # regenerate
make sqlc-vet      # lint the queries
```

One file per aggregate — `product.sql`, `stock_layer.sql`, `sale.sql`, and so on.
Generated code is checked in and never hand-edited.

No ORM by choice: the FIFO consumption and per-owner margin queries are the
interesting part of this system, and they are meant to stay readable
(ARCHITECTURE §7).

**Keep these files ASCII-only.** sqlc rewrites `sqlc.arg(...)` into `?N`
placeholders using byte offsets, and a multi-byte character anywhere in the file
- a section sign, an em dash - shifts every offset after it. The result is a
corrupted query and a parser error pointing at a line that looks fine on disk.
Write "SPEC 3.3", not the section glyph. Migrations are unaffected; they are
parsed as schema and never rewritten.

Queries land from TASKS 0.4 onward.

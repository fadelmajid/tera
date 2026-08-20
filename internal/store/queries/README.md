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

Queries land from TASKS 0.4 onward.

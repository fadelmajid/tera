# migrations

goose, **forward-only**, checked in.

```bash
make migrate-create name=create_legal_entity   # new migration
make migrate-up                                 # apply
make migrate-status
```

Rules:

- A migration that has been merged is never edited. Fix it forward with a new one.
- Numbering is sequential: `001_...`, `002_...`. Ordering is the schema's history.
- Primary keys are UUIDv7 in canonical lowercase `TEXT` (docs/DECISIONS.md D-003).
  v7 is time-ordered, so the FIFO tiebreak on `id` resolves to insertion order (SPEC §3.3).
- Rupiah columns are `INTEGER` (int64). Never `REAL` (INV-1).
- Instants are `INTEGER` unix seconds in UTC. Alongside them, `business_date`
  (`TEXT`, `YYYY-MM-DD`) and `book_year` are computed in the entity's timezone
  **at write time** and stored (INV-5, docs/DECISIONS.md D-005). SQLite's
  `localtime` modifier uses the server's zone, not the entity's, so this
  conversion cannot happen in SQL. Never store local wall-clock time.
- `stock_layer` and `stock_consumption` are append-only. No migration adds a
  mutable balance column to either (INV-7).

First migration lands in TASKS 0.4.

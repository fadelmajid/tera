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
- Timestamps store UTC instants; book-year and business-day boundaries are
  resolved in the entity's timezone at read time, never by storing local time (INV-5).
- `stock_layer` and `stock_consumption` are append-only. No migration adds a
  mutable balance column to either (INV-7).

First migration lands in TASKS 0.4.

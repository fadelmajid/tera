# web

React + TypeScript + Vite. Built to static assets and embedded into the Go
binary via `embed.FS`, so deploying stays "copy one file".

Not scaffolded yet — that is **TASKS 0.9**.

When it is:

- **UI copy is in Bahasa Indonesia from the start.** Not built in English and
  translated later.
- Money is a branded `type IDR = number & { __brand: 'IDR' }` — never a float,
  never mixed with a rate or a quantity (INV-1, SPEC §1).
- Every screen showing a tax figure, threshold, or deadline carries:
  *"Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan
  pajak Anda."*
- The margin report is *Laporan Margin per Owner*. Never *Laba Rugi*.

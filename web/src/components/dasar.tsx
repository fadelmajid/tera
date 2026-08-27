import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { ApiError } from '../api/client'
import { Ikon } from './ikon'

/** Loads a list and exposes a reload, so a create refreshes what is on screen. */
export function useDaftar<T>(muat: () => Promise<T[]>, deps: unknown[]) {
  const [data, setData] = useState<T[]>([])
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const jalankan = useCallback(muat, deps)

  const muatUlang = useCallback(() => {
    setMemuat(true)
    jalankan()
      .then((rows) => {
        setData(rows)
        setGalat(null)
      })
      .catch((err: unknown) => setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data'))
      .finally(() => setMemuat(false))
  }, [jalankan])

  useEffect(muatUlang, [muatUlang])

  return { data, galat, memuat, muatUlang, setGalat }
}

/**
 * An error the screen is showing.
 *
 * `role="alert"` because these appear at the top of a page after an action
 * taken at the bottom of it — a screen reader user gets no other signal that
 * anything happened, and a sighted user may not have the top of the page in
 * view either.
 */
export function Galat({ pesan }: { pesan: string | null }) {
  if (!pesan) return null
  return (
    <div className="galat" role="alert">
      {pesan}
    </div>
  )
}

/** The counterpart: something worked. Also announced. */
export function Berhasil({ pesan, children }: { pesan?: string | null; children?: ReactNode }) {
  if (!pesan && !children) return null
  return (
    <div className="berhasil" role="status">
      {pesan}
      {children}
    </div>
  )
}

/** A screen waiting on the server. `aria-busy` so it is not silent. */
export function Memuat({ apa = 'data' }: { apa?: string }) {
  return (
    <p className="kosong" aria-busy="true" role="status">
      Memuat {apa}…
    </p>
  )
}

export function Teks({
  label,
  nilai,
  ubah,
  wajib,
  petunjuk,
  tipe = 'text',
  inputMode,
  otomatisFokus,
}: {
  label: string
  nilai: string
  ubah: (v: string) => void
  wajib?: boolean
  petunjuk?: string
  tipe?: string
  inputMode?: 'numeric' | 'text' | 'decimal'
  otomatisFokus?: boolean
}) {
  return (
    <label>
      <span>
        {label}
        {wajib ? ' *' : ''}
      </span>
      <input
        type={tipe}
        value={nilai}
        onChange={(e) => ubah(e.target.value)}
        required={wajib}
        inputMode={inputMode}
        autoFocus={otomatisFokus}
      />
      {petunjuk && <small className="petunjuk">{petunjuk}</small>}
    </label>
  )
}

export function Centang({
  label,
  nilai,
  ubah,
  petunjuk,
}: {
  label: string
  nilai: boolean
  ubah: (v: boolean) => void
  petunjuk?: string
}) {
  return (
    <label className="centang">
      <input type="checkbox" checked={nilai} onChange={(e) => ubah(e.target.checked)} />
      <span>
        {label}
        {petunjuk && <small className="petunjuk">{petunjuk}</small>}
      </span>
    </label>
  )
}

/**
 * A table that scrolls inside its own container.
 *
 * Every table in the application goes through this. The rule used to exist in
 * the stylesheet and was applied to nothing, so the ten-column tax rules table
 * and the nine-column margin returns table pushed the whole page sideways
 * instead of scrolling within their card.
 */
export function Tabel({
  children,
  label,
  lengket = 1,
}: {
  children: ReactNode
  label?: string
  /**
   * How many leading columns stay pinned while the figures scroll sideways on
   * a phone. 2 for a drill-down, whose first column is the open/close toggle
   * and whose second is the label that says which row you are reading.
   */
  lengket?: 1 | 2
}) {
  return (
    <div
      className={lengket === 2 ? 'tabel-gulir lengket-2' : 'tabel-gulir'}
      tabIndex={0}
      role="region"
      aria-label={label}
    >
      <table>{children}</table>
    </div>
  )
}

/** One figure with its label. */
export function Stat({
  label,
  nilai,
  kaki,
  besar,
}: {
  label: string
  nilai: ReactNode
  kaki?: ReactNode
  besar?: boolean
}) {
  return (
    <div className={besar ? 'stat besar' : 'stat'}>
      <span className="stat-label">{label}</span>
      <span className="stat-nilai">{nilai}</span>
      {kaki && <span className="stat-kaki">{kaki}</span>}
    </div>
  )
}

/**
 * The open/close control on a drill-down row.
 *
 * A chevron that rotates rather than a `+` that becomes a `−`: the direction
 * says which way the row will move, and it reads at a glance in a table of
 * forty rows where a sign does not.
 */
export function Pemicu({
  buka,
  alih,
  label,
}: {
  buka: boolean
  alih: () => void
  label: string
}) {
  return (
    <button
      type="button"
      className="sekunder ikon-saja"
      onClick={alih}
      aria-expanded={buka}
      aria-label={`${buka ? 'Tutup' : 'Buka'} rincian ${label}`}
    >
      <Ikon nama={buka ? 'tutup-rincian' : 'buka-rincian'} className="" ukuran={15} />
    </button>
  )
}

/** Search over a list of rows, matching against whatever text they expose. */
export function useCari<T>(rows: T[], teks: (row: T) => string) {
  const [q, setQ] = useState('')
  const hasil = useMemo(() => {
    const cari = q.trim().toLowerCase()
    if (!cari) return rows
    return rows.filter((r) => teks(r).toLowerCase().includes(cari))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, q])
  return { q, setQ, hasil }
}

export function Cari({
  nilai,
  ubah,
  label = 'Cari',
  petunjuk,
}: {
  nilai: string
  ubah: (v: string) => void
  label?: string
  petunjuk?: string
}) {
  return (
    <label className="cari">
      <span>{label}</span>
      <input
        type="search"
        value={nilai}
        onChange={(e) => ubah(e.target.value)}
        placeholder={petunjuk}
      />
    </label>
  )
}

export type Arah = 'naik' | 'turun'

/**
 * Column sorting for a table.
 *
 * `kolom` maps a key to the value that key sorts on. Strings sort with the
 * Indonesian collator so "Ürün" and "Andi" land where a person expects;
 * numbers sort numerically, which matters because a rupiah figure rendered as
 * text sorts 9 after 100.
 */
export function useUrut<T>(
  rows: T[],
  kolom: Record<string, (row: T) => string | number>,
  awal?: { kunci: string; arah: Arah },
) {
  const [urutan, setUrutan] = useState<{ kunci: string; arah: Arah } | null>(awal ?? null)

  const hasil = useMemo(() => {
    const ambil = urutan ? kolom[urutan.kunci] : undefined
    if (!urutan || !ambil) return rows
    const tanda = urutan.arah === 'naik' ? 1 : -1
    return [...rows].sort((a, b) => {
      const x = ambil(a)
      const y = ambil(b)
      if (typeof x === 'number' && typeof y === 'number') return (x - y) * tanda
      return String(x).localeCompare(String(y), 'id-ID', { numeric: true }) * tanda
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, urutan])

  const urutkan = useCallback((kunci: string) => {
    setUrutan((prev) =>
      prev?.kunci === kunci
        ? { kunci, arah: prev.arah === 'naik' ? 'turun' : 'naik' }
        : { kunci, arah: 'naik' },
    )
  }, [])

  return { hasil, urutan, urutkan }
}

/** A sortable column header. Renders a plain <th> when no key is given. */
export function Th({
  children,
  kunci,
  urutan,
  urutkan,
  angka,
  lebar,
}: {
  children: ReactNode
  kunci?: string
  urutan?: { kunci: string; arah: Arah } | null
  urutkan?: (kunci: string) => void
  angka?: boolean
  lebar?: number
}) {
  const gaya = lebar === undefined ? undefined : { width: lebar }
  if (!kunci || !urutkan) {
    return (
      <th className={angka ? 'angka' : undefined} style={gaya}>
        {children}
      </th>
    )
  }
  const aktif = urutan?.kunci === kunci
  return (
    <th
      className={angka ? 'angka' : undefined}
      style={gaya}
      aria-sort={aktif ? (urutan.arah === 'naik' ? 'ascending' : 'descending') : undefined}
    >
      <button type="button" className="urut" onClick={() => urutkan(kunci)}>
        {children}
        <span className="panah" aria-hidden="true">
          {aktif ? (urutan.arah === 'naik' ? '▲' : '▼') : '▲'}
        </span>
      </button>
    </th>
  )
}

/**
 * A master-data page: heading, error slot, an add form, and the data.
 *
 * The form is collapsed by default. It used to sit permanently expanded above
 * the table, so on Produk you scrolled past seven fields to reach the
 * catalogue on every visit — even though adding a product is rare and looking
 * one up is constant.
 */
export function Halaman({
  judul,
  keterangan,
  galat,
  form,
  labelTambah,
  alat,
  children,
}: {
  judul: string
  keterangan?: string
  galat: string | null
  form: ReactNode
  /** The button that reveals the form: "Tambah produk", "Tambah owner". */
  labelTambah: string
  /** A search box or filter row, shown above the data. */
  alat?: ReactNode
  children: ReactNode
}) {
  const [buka, setBuka] = useState(false)

  return (
    <>
      <div className="card-kepala">
        <div>
          <h2>{judul}</h2>
          {keterangan && <p className="sub-judul">{keterangan}</p>}
        </div>
        <button type="button" className={buka ? 'sekunder' : undefined} onClick={() => setBuka((b) => !b)} aria-expanded={buka}>
          {buka ? 'Batal' : labelTambah}
        </button>
      </div>

      <Galat pesan={galat} />

      <div className="tumpuk">
        {buka && <div className="card">{form}</div>}
        <div className="card">
          {alat}
          {children}
        </div>
      </div>
    </>
  )
}

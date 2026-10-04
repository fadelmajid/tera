import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../api/client'
import type { Role } from '../api/auth'
import { canManageUsers } from '../api/auth'
import { getMarginReport, monthWindow, setReturnRule } from '../api/margin'
import type { LayerDraw, MarginReport, ReturnPeriodRule } from '../api/margin'
import { formatIDR, idr, sub, sum, ZERO, type IDR } from '../money'
import { Cari, Galat, Memuat, Stat, Tabel, Th, useCari, useUrut } from '../components/dasar'
import { Konfirmasi } from '../components/Modal'

/**
 * Laporan Margin per Owner — the screen money moves on (R2.4).
 *
 * Never *Laba Rugi*. The title and the note come from the server so no client
 * can relabel gross margin as profit; shared costs are excluded and settled
 * outside the application (REQUIREMENTS §5).
 *
 * Every figure opens. Owner → the sales behind it → the products in each →
 * the individual FIFO layers each drew and what they cost (SPEC §4.2). There
 * is nothing here a family member can be shown and not be able to take apart,
 * because "why is mine lower this month" has to be answerable on screen rather
 * than by someone going away to check.
 *
 * # One table, four levels
 *
 * The drill-down used to render a complete <table> at each level inside a
 * <td colSpan> of the level above. Four nested tables meant four independent
 * column grids: nothing lined up with the row it belonged to, every level
 * repeated its own uppercase header, and opening three owners produced a stack
 * of misaligned grids with headers scattered through it.
 *
 * It is now a single table with a single column grid. Depth is carried by
 * indentation and by a background step, so a layer row sits under the product
 * row that drew it and its cost lands in the same column as every other cost
 * on the screen. This is the report a family reads together when they disagree
 * about money; the columns have to line up.
 *
 * Nothing on this page adds money up. Every rupiah was summed on the server
 * from the actual stock_consumption rows; here it is only formatted.
 */

/**
 * One row per product sold, with its stock layers alongside it.
 *
 * The report arrives as a tree — owner, nota, produk, lapisan — because the
 * drill-down is a hard requirement (CLAUDE.md: no summary-only views). A tree
 * of collapsed rows is a poor way to *read* it, though: answering "which sales
 * had the dearest stock" meant opening every branch by hand. Flattening keeps
 * every figure and its evidence on screen at once, and lets sorting and
 * filtering do the work the clicking used to.
 */
type Baris = {
  kunci: string
  ownerId: string
  owner: string
  tanggal: string
  nota: string
  jenis: 'Penjualan' | 'Retur' | 'Retur periode lain'
  /** Context only: a return that came back in another period is excluded from
   *  every total, and a row that is not counted must say so (SPEC §4.4). */
  dihitung: boolean
  produk: string
  kode: string
  qty: number
  pendapatan: IDR
  hpp: IDR
  margin: IDR
  layers: LayerDraw[]
}

function ratakan(laporan: MarginReport): Baris[] {
  const out: Baris[] = []
  for (const o of laporan.owners) {
    const owner = o.owner_name
    for (const s of o.sales) {
      for (const pr of s.products) {
        out.push({
          kunci: `s:${s.sale_id}:${pr.product_id}`,
          ownerId: o.owner_id, owner,
          tanggal: s.business_date, nota: s.invoice_no,
          jenis: 'Penjualan', dihitung: true,
          produk: pr.product_name, kode: pr.product_code,
          qty: pr.qty, pendapatan: pr.revenue_idr,
          hpp: pr.cogs_idr, margin: pr.margin_idr,
          layers: pr.layers,
        })
      }
    }
    for (const [jenis, daftar, dihitung] of [
      ['Retur', o.returns, true],
      ['Retur periode lain', o.later_returns, false],
    ] as const) {
      for (const r of daftar ?? []) {
        for (const pr of r.products) {
          out.push({
            kunci: `${jenis}:${r.return_id}:${pr.product_id}`,
            ownerId: o.owner_id, owner,
            tanggal: r.business_date, nota: r.sale_invoice_no,
            jenis, dihitung,
            produk: pr.product_name, kode: pr.product_code,
            qty: -pr.qty, pendapatan: sub(ZERO, pr.revenue_idr),
            hpp: sub(ZERO, pr.cogs_idr), margin: sub(ZERO, pr.margin_idr),
            layers: pr.layers,
          })
        }
      }
    }
  }
  return out
}

/** The per-unit cost of each layer a line drew, always visible.
 *
 *  This is the column the whole report exists for: the same product in the
 *  same sale can carry two costs 11% apart, and which one applies depends on
 *  whether that delivery's faktur arrived (INV-9). Hiding it behind a click
 *  made the most important figure on the screen the hardest one to reach. */
function Lapisan({ layers }: { layers: LayerDraw[] }) {
  if (layers.length === 0) return <span className="catatan">—</span>
  return (
    <div className="lapisan-sel">
      {layers.map((l) => (
        <div key={l.consumption_id} className="lapisan-baris">
          <span className="angka">{formatIDR(idr(Math.round(l.cost_idr / Math.max(l.qty, 1))))}</span>
          <span className="catatan">× {l.qty}</span>
          <span className={l.faktur_received ? 'lencana lencana-aman' : 'lencana lencana-bahaya'}>
            {l.faktur_received ? 'faktur' : 'tanpa faktur'}
          </span>
        </div>
      ))}
    </div>
  )
}

export function MarginPage({ entityId, role }: { entityId: string; role: Role }) {
  const [bulan, setBulan] = useState(() => new Date().toISOString().slice(0, 7))
  const [laporan, setLaporan] = useState<MarginReport | null>(null)
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)
  const [filterOwner, setFilterOwner] = useState('')

  const muat = useCallback(() => {
    const [tahun, bln] = bulan.split('-')
    const { from, to } = monthWindow(new Date(Number(tahun), Number(bln) - 1, 1))

    setMemuat(true)
    getMarginReport(entityId, from, to)
      .then((r) => {
        setLaporan(r)
        setGalat(null)
      })
      .catch((err: unknown) =>
        setGalat(err instanceof ApiError ? err.message : 'Tidak bisa membuka laporan margin'),
      )
      .finally(() => setMemuat(false))
  }, [entityId, bulan])

  useEffect(muat, [muat])

  // Rows are derived once per report, then sorted and filtered in place.
  const baris = useMemo(() => (laporan ? ratakan(laporan) : []), [laporan])

  const { q, setQ, hasil: dicari } = useCari(baris, (b) =>
    `${b.owner} ${b.nota} ${b.produk} ${b.kode} ${b.jenis}`)

  const disaring = useMemo(
    () => (filterOwner ? dicari.filter((b) => b.ownerId === filterOwner) : dicari),
    [dicari, filterOwner],
  )

  const { hasil: terlihat, urutan, urutkan } = useUrut(
    disaring,
    {
      owner: (b) => b.owner,
      tanggal: (b) => b.tanggal,
      nota: (b) => b.nota,
      produk: (b) => b.produk,
      qty: (b) => b.qty,
      pendapatan: (b) => b.pendapatan,
      hpp: (b) => b.hpp,
      margin: (b) => b.margin,
    },
    { kunci: 'margin', arah: 'turun' },
  )

  // A return that came back in another period is shown for context and left
  // out of every total (SPEC §4.4), so the footer must skip it too.
  const marginTerlihat = sum(terlihat.filter((b) => b.dihitung).map((b) => b.margin))

  return (
    <>
      <h2>{laporan?.title ?? 'Laporan Margin per Owner'}</h2>
      <p className="sub-judul">
        {laporan?.note ??
          'Margin kotor: pendapatan dikurangi HPP dari lapisan stok yang benar-benar terpakai.'}
      </p>

      <Galat pesan={galat} />

      <div className="tumpuk">
        <div className="card">
          <div className="alat">
            <label>
              <span>Periode</span>
              <input type="month" value={bulan} onChange={(e) => setBulan(e.target.value)} />
            </label>
            {laporan && (
              <span className="hitung">
                {laporan.period.from} s/d {laporan.period.to}
              </span>
            )}
          </div>
        </div>

        {laporan && (
          <AturanRetur
            laporan={laporan}
            entityId={entityId}
            bolehUbah={canManageUsers(role)}
            selesai={muat}
            lapor={setGalat}
          />
        )}

        {memuat && <Memuat apa="laporan margin" />}

        {/* The answer to "bulan ini siapa dapat berapa", before any clicking.
            The table below is the evidence for these four numbers; it should
            not be the only way to read them. */}
        {laporan && !memuat && laporan.owners.length > 0 && (
          <div className="card">
            <h3>Margin kotor per owner</h3>
            <div className="statistik">
              {[...laporan.owners]
                .sort((a, b) => b.margin_idr - a.margin_idr)
                .map((o) => (
                  <Stat
                    key={o.owner_id || 'perusahaan'}
                    label={o.owner_name}
                    nilai={formatIDR(o.margin_idr)}
                    kaki={`dari ${o.sales.length} nota`}
                  />
                ))}
              <Stat
                label="Total"
                nilai={formatIDR(laporan.totals.margin_idr)}
                kaki="semua owner"
                besar
              />
            </div>
          </div>
        )}

        {laporan && !memuat && (
          <div className="card">
            <div className="alat">
              <Cari
                nilai={q}
                ubah={setQ}
                label="Cari"
                petunjuk="owner, nota, atau nama produk"
              />
              <label>
                <span>Owner</span>
                <select value={filterOwner} onChange={(e) => setFilterOwner(e.target.value)}>
                  <option value="">Semua owner</option>
                  {(laporan.owners ?? []).map((o) => (
                    <option key={o.owner_id || 'perusahaan'} value={o.owner_id}>
                      {o.owner_name}
                    </option>
                  ))}
                </select>
              </label>
              <span className="hitung">
                {terlihat.length} baris · margin {formatIDR(marginTerlihat)}
              </span>
            </div>

            {terlihat.length === 0 ? (
              <p className="kosong">Tidak ada baris yang cocok pada periode ini.</p>
            ) : (
              <Tabel label="Margin per owner, satu baris per produk terjual" lengket={2}>
                <thead>
                  <tr>
                    <Th kunci="owner" urutan={urutan} urutkan={urutkan}>Owner</Th>
                    <Th kunci="tanggal" urutan={urutan} urutkan={urutkan} lebar={110}>Tanggal</Th>
                    <Th kunci="nota" urutan={urutan} urutkan={urutkan}>Nota</Th>
                    <Th kunci="produk" urutan={urutan} urutkan={urutkan}>Produk</Th>
                    <Th kunci="qty" urutan={urutan} urutkan={urutkan} angka lebar={80}>Jumlah</Th>
                    <Th>Modal per unit, per kiriman</Th>
                    <Th kunci="pendapatan" urutan={urutan} urutkan={urutkan} angka>Pendapatan</Th>
                    <Th kunci="hpp" urutan={urutan} urutkan={urutkan} angka>HPP</Th>
                    <Th kunci="margin" urutan={urutan} urutkan={urutkan} angka>Margin</Th>
                  </tr>
                </thead>
                <tbody>
                  {terlihat.map((b) => (
                    <tr key={b.kunci} className={b.dihitung ? undefined : 'baris-redup'}>
                      <td>{b.owner}</td>
                      <td>{b.tanggal}</td>
                      <td>
                        {b.nota}
                        {b.jenis !== 'Penjualan' && (
                          <>
                            <br />
                            <small className="catatan">
                              {b.jenis}
                              {!b.dihitung && ' · tidak dihitung'}
                            </small>
                          </>
                        )}
                      </td>
                      <td>
                        {b.produk}
                        <br />
                        <small className="catatan">{b.kode}</small>
                      </td>
                      <td className="angka">{b.qty}</td>
                      <td><Lapisan layers={b.layers} /></td>
                      <td className="angka">{formatIDR(b.pendapatan)}</td>
                      <td className="angka">{formatIDR(b.hpp)}</td>
                      <td className="angka">{formatIDR(b.margin)}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td colSpan={6}>
                      Total {filterOwner || q ? 'baris yang terlihat' : 'semua owner'}
                    </td>
                    <td className="angka">{formatIDR(sum(terlihat.filter((b) => b.dihitung).map((b) => b.pendapatan)))}</td>
                    <td className="angka">{formatIDR(sum(terlihat.filter((b) => b.dihitung).map((b) => b.hpp)))}</td>
                    <td className="angka">{formatIDR(marginTerlihat)}</td>
                  </tr>
                </tfoot>
              </Tabel>
            )}
          </div>
        )}

        {laporan && (
          <div className="disclaimer">
            <strong>{laporan.caveat}</strong>
            <br />
            {laporan.note}
            <br />
            Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
          </div>
        )}
      </div>
    </>
  )
}

/* --- the rows -------------------------------------------------------------


/**
 * The rule in force for returns that cross a settlement boundary.
 *
 * Always shown, never as an alarm. SPEC §4.4 is decided (D-012): a return
 * reduces the month the goods came back, so an already-settled month never
 * moves under anyone. But money is divided on these figures, so the rule that
 * placed the returns belongs on the same screen as the numbers it produced.
 *
 * Changing it is deliberate and owner-only, because it restates every past
 * report — including months whose money has already been handed over. That is
 * why it goes through a confirmation that says so, rather than a button that
 * simply does it.
 */
function AturanRetur({
  laporan,
  entityId,
  bolehUbah,
  selesai,
  lapor,
}: {
  laporan: MarginReport
  entityId: string
  bolehUbah: boolean
  selesai: () => void
  lapor: (pesan: string | null) => void
}) {
  const [pilihan, setPilihan] = useState<ReturnPeriodRule | null>(null)
  const [menyimpan, setMenyimpan] = useState(false)
  const aturan = laporan.return_rule
  const lain: ReturnPeriodRule = aturan === 'RETURN_DATE' ? 'SALE_DATE' : 'RETURN_DATE'

  const simpan = (alasan: string) => {
    if (!pilihan) return
    setMenyimpan(true)
    setReturnRule(entityId, pilihan, alasan)
      .then(() => {
        lapor(null)
        setPilihan(null)
        selesai()
      })
      .catch((err: unknown) =>
        lapor(err instanceof ApiError ? err.message : 'Tidak bisa menyimpan aturan retur'),
      )
      .finally(() => setMenyimpan(false))
  }

  return (
    <div className="card">
      <div className="card-kepala">
        <h3>Retur lintas bulan dihitung pada bulan {namaAturan[aturan]}</h3>
        {bolehUbah && (
          <button className="sekunder" onClick={() => setPilihan(lain)}>
            Ubah aturan
          </button>
        )}
      </div>
      <p className="catatan">
        {aturan === 'RETURN_DATE'
          ? 'Bulan penjualan asal tidak berubah, jadi bagi hasil yang sudah dibayarkan tidak perlu ditarik kembali. Barang yang kembali di bulan lain tetap tercatat di bawah, sebagai keterangan.'
          : 'Laporan bulan penjualan menjadi benar, tapi angkanya berubah setelah uangnya dibagi.'}
      </p>

      {pilihan && (
        <Konfirmasi
          judul="Ubah aturan perhitungan retur"
          gawat
          sibuk={menyimpan}
          labelJalankan={`Hitung di bulan ${namaAturan[pilihan]}`}
          tutup={() => setPilihan(null)}
          jalankan={simpan}
          petunjukAlasan="Mis. disepakati rapat keluarga 3 Agustus"
        >
          <p>
            Retur lintas bulan akan dihitung pada <strong>bulan {namaAturan[pilihan]}</strong>,
            bukan bulan {namaAturan[aturan]}.
          </p>
          <p>
            <strong>Ini mengubah semua laporan yang sudah lewat</strong>, termasuk bulan yang bagi
            hasilnya sudah dibayarkan kepada masing-masing owner. Angka yang sudah dipakai untuk
            membagi uang bisa berubah setelah perubahan ini disimpan.
          </p>
          <p className="catatan">Perubahan beserta alasannya dicatat di log audit (INV-10).</p>
        </Konfirmasi>
      )}
    </div>
  )
}

const namaAturan: Record<ReturnPeriodRule, string> = {
  RETURN_DATE: 'barang dikembalikan',
  SALE_DATE: 'penjualan asal',
}

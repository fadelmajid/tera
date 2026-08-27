import { Fragment, useCallback, useEffect, useState, type ReactNode } from 'react'
import { ApiError } from '../api/client'
import type { Role } from '../api/auth'
import { canManageUsers } from '../api/auth'
import { getMarginReport, monthWindow, setReturnRule } from '../api/margin'
import type {
  LayerDraw,
  MarginReport,
  MarginReturn,
  MarginSale,
  OwnerMargin,
  ProductLine,
  ReturnPeriodRule,
} from '../api/margin'
import { formatIDR } from '../money'
import { Galat, Memuat, Pemicu, Tabel } from '../components/dasar'
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
export function MarginPage({ entityId, role }: { entityId: string; role: Role }) {
  const [bulan, setBulan] = useState(() => new Date().toISOString().slice(0, 7))
  const [laporan, setLaporan] = useState<MarginReport | null>(null)
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)
  const [terbuka, setTerbuka] = useState<Set<string>>(new Set())

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
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat laporan margin'),
      )
      .finally(() => setMemuat(false))
  }, [entityId, bulan])

  useEffect(muat, [muat])

  const alih = (kunci: string) =>
    setTerbuka((sebelum) => {
      const sesudah = new Set(sebelum)
      if (!sesudah.delete(kunci)) sesudah.add(kunci)
      return sesudah
    })

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

        {laporan && !memuat && (
          <div className="card">
            {laporan.owners.length === 0 ? (
              <p className="kosong">Belum ada penjualan pada periode ini.</p>
            ) : (
              <Tabel label="Margin per owner, dengan rincian sampai lapisan stok" lengket={2}>
                <thead>
                  <tr>
                    <th style={{ width: 40 }} />
                    <th>Rincian</th>
                    <th className="angka">Jumlah</th>
                    <th className="angka">Pendapatan</th>
                    <th className="angka">HPP</th>
                    <th className="angka">Retur</th>
                    <th className="angka">Margin</th>
                  </tr>
                </thead>
                <tbody>
                  {laporan.owners.map((o) => (
                    <Fragment key={o.owner_id || 'perusahaan'}>
                      {barisOwner(o, terbuka, alih)}
                    </Fragment>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td />
                    <td>Total semua owner</td>
                    <td />
                    <td className="angka">{formatIDR(laporan.totals.revenue_idr)}</td>
                    <td className="angka">{formatIDR(laporan.totals.cogs_idr)}</td>
                    <td className="angka">
                      {laporan.totals.return_margin_idr === 0
                        ? '—'
                        : `-${formatIDR(laporan.totals.return_margin_idr)}`}
                    </td>
                    <td className="angka">
                      <strong>{formatIDR(laporan.totals.margin_idr)}</strong>
                    </td>
                  </tr>
                </tfoot>
              </Tabel>
            )}

            {laporan.owners.length > 0 && (
              <p className="catatan">
                Buka tanda panah untuk melihat nota di balik angka, lalu produk di dalamnya, lalu
                lapisan stok yang benar-benar terpakai beserta status fakturnya.
              </p>
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

   Each level returns a flat array of <tr>, so the whole drill-down is one
   table body with one column grid. Depth is indentation on the first content
   cell plus a background step on the row, never a nested <table>. */

function barisOwner(
  owner: OwnerMargin,
  terbuka: Set<string>,
  alih: (k: string) => void,
): ReactNode[] {
  const kunci = `owner:${owner.owner_id}`
  const buka = terbuka.has(kunci)
  const adaRetur = owner.return_refund_idr !== 0 || owner.return_cogs_idr !== 0
  const rows: ReactNode[] = []

  rows.push(
    <tr key={kunci}>
      <td>
        <Pemicu buka={buka} alih={() => alih(kunci)} label={owner.owner_name} />
      </td>
      <td>
        <strong>{owner.owner_name}</strong>
        {owner.is_company && (
          <>
            <br />
            <small className="catatan">stok tanpa owner</small>
          </>
        )}
      </td>
      <td />
      <td className="angka">{formatIDR(owner.revenue_idr)}</td>
      <td className="angka">{formatIDR(owner.cogs_idr)}</td>
      <td className="angka">{adaRetur ? `-${formatIDR(owner.return_margin_idr)}` : '—'}</td>
      <td className="angka">
        <strong>{formatIDR(owner.margin_idr)}</strong>
      </td>
    </tr>,
  )

  if (!buka) return rows

  if (owner.ppn_idr !== 0 || adaRetur) {
    rows.push(
      <tr key={`${kunci}:hitung`} className="rincian">
        <td />
        <td colSpan={6}>
          {owner.ppn_idr !== 0 && (
            <p className="catatan">
              Diterima dari pelanggan {formatIDR(owner.tendered_idr)} = penjualan{' '}
              {formatIDR(owner.revenue_idr)} + PPN {formatIDR(owner.ppn_idr)}. PPN disetor ke
              negara dan tidak masuk margin.
            </p>
          )}
          {adaRetur && (
            <p className="catatan">
              Penjualan {formatIDR(owner.revenue_idr)} − HPP {formatIDR(owner.cogs_idr)} ={' '}
              {formatIDR(owner.gross_margin_idr)}, dikurangi retur{' '}
              {formatIDR(owner.return_margin_idr)} (uang kembali{' '}
              {formatIDR(owner.return_refund_idr)}
              {owner.return_ppn_idr !== 0 && <> termasuk PPN {formatIDR(owner.return_ppn_idr)}</>},
              HPP kembali {formatIDR(owner.return_cogs_idr)}).
            </p>
          )}
        </td>
      </tr>,
    )
  }

  if (owner.sales.length === 0) {
    rows.push(
      <tr key={`${kunci}:kosong`} className="baris-2">
        <td />
        <td colSpan={6} className="tingkat-1 teks-redup">
          Tidak ada penjualan pada periode ini.
        </td>
      </tr>,
    )
  }

  for (const s of owner.sales) {
    rows.push(...barisPenjualan(owner, s, terbuka, alih))
  }

  if (owner.returns.length > 0) {
    rows.push(seksi(`${kunci}:seksi-retur`, 'Retur'))
    for (const r of owner.returns) {
      rows.push(...barisRetur(owner, r, 'retur', terbuka, alih))
    }
  }

  if (owner.later_returns.length > 0) {
    rows.push(
      seksi(
        `${kunci}:seksi-retur-lain`,
        'Barang bulan ini yang kembali di bulan lain',
        'Keterangan saja — tidak dikurangkan dari angka di atas. Sudah dihitung di bulan barang itu kembali.',
      ),
    )
    for (const r of owner.later_returns) {
      rows.push(...barisRetur(owner, r, 'retur-lain', terbuka, alih))
    }
  }

  return rows
}

function barisPenjualan(
  owner: OwnerMargin,
  sale: MarginSale,
  terbuka: Set<string>,
  alih: (k: string) => void,
): ReactNode[] {
  const kunci = `sale:${owner.owner_id}:${sale.sale_id}`
  const buka = terbuka.has(kunci)
  const rows: ReactNode[] = [
    <tr key={kunci} className="baris-2">
      <td>
        <Pemicu buka={buka} alih={() => alih(kunci)} label={`nota ${sale.invoice_no}`} />
      </td>
      <td className="tingkat-1">
        {sale.invoice_no}
        <br />
        <small className="catatan">
          {sale.business_date} · {sale.customer_name || 'tanpa pelanggan'}
        </small>
      </td>
      <td />
      <td className="angka">{formatIDR(sale.revenue_idr)}</td>
      <td className="angka">{formatIDR(sale.cogs_idr)}</td>
      <td className="angka">—</td>
      <td className="angka">{formatIDR(sale.margin_idr)}</td>
    </tr>,
  ]

  if (buka) {
    for (const p of sale.products) rows.push(...barisProduk(p, kunci, terbuka, alih))
  }
  return rows
}

function barisRetur(
  owner: OwnerMargin,
  r: MarginReturn,
  jenis: string,
  terbuka: Set<string>,
  alih: (k: string) => void,
): ReactNode[] {
  const kunci = `${jenis}:${owner.owner_id}:${r.return_id}`
  const buka = terbuka.has(kunci)
  const rows: ReactNode[] = [
    <tr key={kunci} className="baris-2">
      <td>
        <Pemicu buka={buka} alih={() => alih(kunci)} label={`retur nota ${r.sale_invoice_no}`} />
      </td>
      <td className="tingkat-1">
        Retur nota {r.sale_invoice_no}
        <br />
        {/* All three dates, always. Nobody reading a settlement should have to
            work out which rule was in force when a return crossed a boundary. */}
        <small className="catatan">
          dikembalikan {r.business_date} · dijual {r.sale_business_date} · dihitung pada{' '}
          {r.effective_date}
          {r.crosses_period && ' · lintas bulan'}
          {r.reason && ` · ${r.reason}`}
        </small>
      </td>
      <td />
      <td className="angka">-{formatIDR(r.revenue_reversed_idr)}</td>
      <td className="angka">-{formatIDR(r.cogs_reversed_idr)}</td>
      <td className="angka">-{formatIDR(r.margin_idr)}</td>
      <td className="angka">—</td>
    </tr>,
  ]

  if (buka) {
    for (const p of r.products) rows.push(...barisProduk(p, kunci, terbuka, alih))
  }
  return rows
}

function barisProduk(
  p: ProductLine,
  induk: string,
  terbuka: Set<string>,
  alih: (k: string) => void,
): ReactNode[] {
  const kunci = `${induk}:${p.product_id}`
  const buka = terbuka.has(kunci)
  const rows: ReactNode[] = [
    <tr key={kunci} className="baris-3">
      <td>
        <Pemicu buka={buka} alih={() => alih(kunci)} label={p.product_name} />
      </td>
      <td className="tingkat-2">
        {p.product_name}
        <br />
        <small className="catatan">{p.product_code}</small>
      </td>
      <td className="angka">{p.qty}</td>
      <td className="angka">{formatIDR(p.revenue_idr)}</td>
      <td className="angka">{formatIDR(p.cogs_idr)}</td>
      <td className="angka">—</td>
      <td className="angka">{formatIDR(p.margin_idr)}</td>
    </tr>,
  ]

  if (buka) {
    if (p.layers.length === 0) {
      rows.push(
        <tr key={`${kunci}:kosong`} className="baris-4">
          <td />
          <td colSpan={6} className="tingkat-3 teks-redup">
            Tidak ada lapisan stok.
          </td>
        </tr>,
      )
    }
    for (const l of p.layers) rows.push(barisLapisan(l, kunci))
  }
  return rows
}

/**
 * The bottom of the drill-down: one slice of one FIFO layer, oldest first —
 * the order they were actually drawn.
 *
 * The faktur note is the point of this row. Two purchases at the same price
 * from the same supplier produce different costs depending on whether the
 * supplier handed over the faktur pajak, because without it the PPN paid was
 * never creditable and became cost (INV-9, SPEC §3.2). On this screen that is
 * usually the whole answer to "why is my margin lower this month".
 */
function barisLapisan(l: LayerDraw, induk: string): ReactNode {
  return (
    <tr key={`${induk}:${l.consumption_id}`} className="baris-4">
      <td />
      <td className="tingkat-3">
        <code>…{l.layer_id.slice(-8)}</code>
        {l.is_reversal && <span className="lencana"> dikembalikan</span>}
        <br />
        <small className="catatan">
          masuk {new Date(l.acquired_at * 1000).toLocaleDateString('id-ID')} · {l.source} ·{' '}
          {formatIDR(l.layer_cost_total_idr)} untuk {l.layer_qty_in}
        </small>
        <br />
        {l.faktur_received ? (
          <small className="catatan">faktur pajak ada</small>
        ) : (
          <small className="teks-bahaya">tanpa faktur — PPN masuk ke biaya</small>
        )}
      </td>
      <td className="angka">{l.qty}</td>
      <td className="angka">—</td>
      <td className="angka">{formatIDR(l.cost_idr)}</td>
      <td className="angka">—</td>
      <td className="angka">—</td>
    </tr>
  )
}

/** A labelled break inside the drill-down. */
function seksi(kunci: string, judul: string, keterangan?: string): ReactNode {
  return (
    <tr key={kunci} className="rincian">
      <td />
      <td colSpan={6}>
        <strong>{judul}</strong>
        {keterangan && (
          <>
            <br />
            <small className="catatan">{keterangan}</small>
          </>
        )}
      </td>
    </tr>
  )
}

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
        lapor(err instanceof ApiError ? err.message : 'Gagal menyimpan aturan retur'),
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
          ? 'Bulan penjualan asal tidak berubah, sehingga bagi hasil yang sudah dibayarkan tidak perlu ditarik kembali. Barang yang kembali di bulan lain tetap tercatat di bawah, sebagai keterangan.'
          : 'Laporan bulan penjualan menjadi benar, tetapi angkanya berubah setelah uangnya dibagi.'}
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

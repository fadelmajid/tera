import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import type { Entity } from '../api/masterdata'
import { listOwners, listProducts, listSuppliers } from '../api/masterdata'
import { currentSession } from '../api/kasir'
import type { CashSession } from '../api/kasir'
import { agingReport, salesReport, stockReport } from '../api/laporan'
import type { AgingReport, SalesReport, StockReport } from '../api/laporan'
import { getMarginReport, monthWindow } from '../api/margin'
import type { MarginReport } from '../api/margin'
import { formatIDR, ZERO } from '../money'
import { Galat, Memuat, Stat, Tabel } from '../components/dasar'
import { OmzetBanner } from '../components/OmzetBanner'

/**
 * Beranda — what is happening, and what to do next.
 *
 * This used to be a card containing one prose sentence listing which features
 * existed. It had no figures on it, no state, and no next action, so the
 * landing page of a business system was a table of contents — and on a fresh
 * installation it said nothing at all about the order the app has to be set up
 * in, which is the reason the flow was hard to follow.
 *
 * Two jobs now, and which one it does depends on the data:
 *
 *   Empty install — the setup checklist. Owners before products before stock
 *   before selling, because that is the actual dependency chain (INV-8), and
 *   each step links straight into the screen that clears it.
 *
 *   Running shop — today's takings, whether the till is open, this month's
 *   margin per owner, and what is overdue. Every figure links to the report it
 *   came from; nothing here is a number you cannot take apart.
 *
 * Nothing on this page adds money up. Every rupiah was summed on the server.
 */
export function DasborPage({
  entity,
  entityId,
  namaPengguna,
  bolehMargin,
  bolehPajak,
  bolehLaporan,
  bolehBeli,
}: {
  entity: Entity | undefined
  entityId: string
  namaPengguna: string
  bolehMargin: boolean
  bolehPajak: boolean
  bolehLaporan: boolean
  bolehBeli: boolean
}) {
  const [sesi, setSesi] = useState<CashSession | null>(null)
  const [hariIni, setHariIni] = useState<SalesReport | null>(null)
  const [bulan, setBulan] = useState<SalesReport | null>(null)
  const [stok, setStok] = useState<StockReport | null>(null)
  const [margin, setMargin] = useState<MarginReport | null>(null)
  const [piutang, setPiutang] = useState<AgingReport | null>(null)
  const [hutang, setHutang] = useState<AgingReport | null>(null)
  const [jumlah, setJumlah] = useState({ owner: 0, produk: 0, pemasok: 0 })
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)

  const muat = useCallback(async () => {
    setMemuat(true)
    try {
      // "Hari ini" means today. An earlier version took the open session's
      // business_date instead, on the theory that it was the entity-local
      // boundary (INV-5) — but a till left open for three days then made the
      // card report three-day-old figures under the word "today". A stale
      // session is a real operational problem, so it is flagged below rather
      // than quietly redefining the date.
      const s = await currentSession(entityId)
      setSesi(s)
      const tanggal = isoHariIni()
      const { from, to } = monthWindow(hariLokal(tanggal))

      const [owners, produk, pemasok, hari, bln, stk] = await Promise.all([
        listOwners(entityId),
        listProducts(entityId),
        listSuppliers(entityId).catch(() => []),
        salesReport(entityId, tanggal, tanggal),
        salesReport(entityId, from, to),
        stockReport(entityId, from, to),
      ])

      setJumlah({ owner: owners.length, produk: produk.length, pemasok: pemasok.length })
      setHariIni(hari)
      setBulan(bln)
      setStok(stk)

      if (bolehMargin) {
        setMargin(await getMarginReport(entityId, from, to).catch(() => null))
      }
      if (bolehLaporan) {
        const [pi, hu] = await Promise.all([
          agingReport(entityId, 'piutang', tanggal).catch(() => null),
          agingReport(entityId, 'hutang', tanggal).catch(() => null),
        ])
        setPiutang(pi)
        setHutang(hu)
      }
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat beranda')
    } finally {
      setMemuat(false)
    }
  }, [entityId, bolehMargin, bolehLaporan])

  useEffect(() => {
    void muat()
  }, [muat])

  const hariIniTanggal = isoHariIni()
  const sesiBasi = sesi !== null && sesi.business_date !== hariIniTanggal
  const umurSesi = sesi ? selisihHari(sesi.business_date, hariIniTanggal) : 0

  const adaStok = (stok?.on_hand.length ?? 0) > 0
  const adaJual = (bulan?.summary.sale_count ?? 0) > 0
  const langkah = [
    { selesai: true, judul: 'Perusahaan dibuat', ket: entity?.name ?? '—', ke: '/perusahaan' },
    {
      selesai: jumlah.owner > 0,
      judul: 'Tambahkan owner',
      ket: 'Anggota keluarga yang memiliki lini produk. Margin dibagi per owner, jadi ini harus ada sebelum produk.',
      ke: '/owner',
    },
    {
      selesai: jumlah.produk > 0,
      judul: 'Tambahkan produk',
      ket: 'Setiap produk ditandai miliknya siapa. Tanda itulah yang menentukan margin siapa yang naik saat terjual.',
      ke: '/produk',
    },
    {
      selesai: jumlah.pemasok > 0,
      judul: 'Tambahkan pemasok',
      ket: 'Diperlukan untuk mencatat pembelian dan status faktur pajaknya.',
      ke: '/pemasok',
    },
    {
      selesai: adaStok,
      judul: 'Masukkan stok',
      ket: 'Saldo awal untuk barang yang sudah di rak, atau pembelian untuk barang yang baru datang.',
      ke: '/saldo-awal',
    },
    {
      selesai: adaJual,
      judul: 'Buka kasir dan jual',
      ket: 'Buka sesi kas, lalu scan atau pilih produk.',
      ke: '/kasir',
    },
  ]
  const belumSiap = langkah.some((l) => !l.selesai)
  const selesaiCount = langkah.filter((l) => l.selesai).length

  return (
    <>
      <h2>Beranda</h2>
      <p className="sub-judul">
        {entity?.name ?? 'Perusahaan'}
        {entity?.is_pkp
          ? ' — PKP, wajib memungut PPN pada setiap penjualan kena pajak.'
          : ' — non-PKP, tidak memungut PPN dan tidak dapat mengkreditkan PPN masukan.'}
      </p>

      <Galat pesan={galat} />
      {memuat && <Memuat apa="beranda" />}

      {!memuat && (
        <div className="tumpuk">
          {belumSiap && bolehBeli && (
            <div className="card">
              <div className="card-kepala">
                <h3>Persiapan — langkah {selesaiCount} dari {langkah.length}</h3>
                <span className="lencana">Belum selesai</span>
              </div>
              <p className="catatan">
                Urutannya penting: setiap langkah memerlukan yang di atasnya. Halo {namaPengguna} —
                kerjakan dari atas dan sistem siap dipakai berjualan.
              </p>
              {langkah.map((l) => (
                <div key={l.judul} className={l.selesai ? 'langkah selesai' : 'langkah'}>
                  <span className="langkah-no" aria-hidden="true">
                    {l.selesai ? '✓' : langkah.indexOf(l) + 1}
                  </span>
                  <span className="langkah-teks">
                    <strong>{l.judul}</strong>
                    <span>{l.ket}</span>
                  </span>
                  {l.selesai ? (
                    <span className="lencana lencana-aman">Selesai</span>
                  ) : (
                    <Link className="tombol" to={l.ke}>
                      Buka
                    </Link>
                  )}
                </div>
              ))}
            </div>
          )}

          <div className="dasbor-atas">
            <div className="card rapi">
              <h3>Hari ini</h3>
              <div className="statistik">
                <Stat
                  label="Diterima dari pelanggan"
                  nilai={formatIDR(hariIni?.summary.total_idr ?? ZERO)}
                  kaki={
                    <>
                      {hariIni?.summary.sale_count ?? 0} nota ·{' '}
                      <span className="tgl">{hariIniTanggal}</span>
                    </>
                  }
                  besar
                />
              </div>

              {!sesi && (
                <p className="catatan">
                  Tidak ada sesi kas yang terbuka. Kasir belum bisa dipakai.
                </p>
              )}

              {sesi && !sesiBasi && (
                <p className="catatan">
                  Sesi kas terbuka. Modal awal {formatIDR(sesi.opening_float_idr)}.
                </p>
              )}

              {/* A till open across days keeps the void window open on every
                  sale inside it and makes the Z-report meaningless, so this is
                  stated as a problem rather than as a status line. */}
              {sesi && sesiBasi && (
                <div className="disclaimer">
                  <strong>
                    Sesi kas masih terbuka sejak{' '}
                    <span className="tgl">{sesi.business_date}</span>
                    {umurSesi > 0 && ` — ${umurSesi} hari`}.
                  </strong>{' '}
                  Selama belum ditutup, penjualan di dalamnya masih bisa dibatalkan dan uang laci
                  belum pernah dicocokkan. Tutup sesinya, lalu buka sesi baru untuk hari ini.
                </div>
              )}
              <div className="aksi">
                <Link className="tombol" to="/kasir">
                  {sesi ? 'Ke kasir' : 'Buka sesi kas'}
                </Link>
                <Link className="tombol sekunder" to="/sesi-kas">
                  Sesi kas
                </Link>
              </div>
            </div>

            <div className="card rapi">
              <h3>Bulan ini</h3>
              <div className="statistik">
                <Stat
                  label="Margin kotor"
                  nilai={formatIDR(bulan?.summary.gross_margin_idr ?? ZERO)}
                  kaki={`${bulan?.summary.sale_count ?? 0} nota · ${formatIDR(bulan?.summary.total_idr ?? ZERO)} diterima`}
                  besar
                />
              </div>
              {(bulan?.summary.void_count ?? 0) > 0 && (
                <p className="catatan">
                  {bulan?.summary.void_count} nota dibatalkan, tidak termasuk angka di atas.
                </p>
              )}
            </div>

            <div className="card rapi">
              <h3>Stok</h3>
              <div className="statistik">
                <Stat
                  label="Nilai persediaan"
                  nilai={formatIDR(stok?.summary.value_idr ?? ZERO)}
                  kaki={`${stok?.summary.qty_total ?? 0} unit di rak`}
                  besar
                />
              </div>
              {(stok?.out_of_stock.length ?? 0) > 0 && (
                <p className="catatan">
                  <strong>{stok?.out_of_stock.length} produk habis</strong> — bisa di-scan kasir
                  tetapi tidak bisa dijual.
                </p>
              )}
            </div>
          </div>

          {bolehPajak && <OmzetBanner entityId={entityId} />}

          {bolehMargin && margin && margin.owners.length > 0 && (
            <div className="card">
              <div className="card-kepala">
                <h3>Margin per owner — bulan ini</h3>
                <Link to="/margin">Laporan lengkap</Link>
              </div>
              <Tabel label="Margin per owner bulan ini">
                <thead>
                  <tr>
                    <th>Owner</th>
                    <th className="angka">Pendapatan</th>
                    <th className="angka">HPP</th>
                    <th className="angka">Margin</th>
                  </tr>
                </thead>
                <tbody>
                  {margin.owners.map((o) => (
                    <tr key={o.owner_id || 'perusahaan'}>
                      <td>{o.owner_name}</td>
                      <td className="angka">{formatIDR(o.revenue_idr)}</td>
                      <td className="angka">{formatIDR(o.cogs_idr)}</td>
                      <td className="angka">
                        <strong>{formatIDR(o.margin_idr)}</strong>
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td>Total</td>
                    <td className="angka">{formatIDR(margin.totals.revenue_idr)}</td>
                    <td className="angka">{formatIDR(margin.totals.cogs_idr)}</td>
                    <td className="angka">
                      <strong>{formatIDR(margin.totals.margin_idr)}</strong>
                    </td>
                  </tr>
                </tfoot>
              </Tabel>
              <p className="disclaimer">
                Margin kotor — pendapatan dikurangi HPP. Biaya bersama seperti listrik, gaji dan
                sewa tidak termasuk dan diselesaikan di luar aplikasi.
              </p>
            </div>
          )}

          {bolehLaporan && (piutang || hutang) && (
            <div className="dasbor-atas">
              {piutang && (
                <Link className="card rapi kartu-tautan" to="/piutang">
                  <h3>Piutang</h3>
                  <div className="statistik">
                    <Stat
                      label="Menunggak"
                      nilai={formatIDR(piutang.buckets.overdue_idr)}
                      kaki={`dari ${formatIDR(piutang.buckets.total_idr)} total`}
                    />
                  </div>
                  {piutang.without_due_date > 0 && (
                    <p className="catatan">
                      {piutang.without_due_date} dokumen tanpa jatuh tempo — umurnya tidak bisa
                      dihitung.
                    </p>
                  )}
                </Link>
              )}
              {hutang && (
                <Link className="card rapi kartu-tautan" to="/hutang">
                  <h3>Hutang</h3>
                  <div className="statistik">
                    <Stat
                      label="Menunggak"
                      nilai={formatIDR(hutang.buckets.overdue_idr)}
                      kaki={`dari ${formatIDR(hutang.buckets.total_idr)} total`}
                    />
                  </div>
                  {hutang.without_due_date > 0 && (
                    <p className="catatan">
                      {hutang.without_due_date} dokumen tanpa jatuh tempo — umurnya tidak bisa
                      dihitung.
                    </p>
                  )}
                </Link>
              )}
            </div>
          )}
        </div>
      )}
    </>
  )
}

/**
 * Reads a YYYY-MM-DD business date as a local calendar day.
 *
 * `new Date('2026-08-24')` is parsed as UTC midnight, which reads back as the
 * 23rd anywhere west of Greenwich — so the month window would be wrong for one
 * day either side of a month boundary (INV-5 in spirit: a business day is a
 * local calendar day, never a UTC instant).
 */
/** Whole days between two YYYY-MM-DD dates, as a local calendar difference. */
function selisihHari(dari: string, sampai: string): number {
  const ms = hariLokal(sampai).getTime() - hariLokal(dari).getTime()
  return Math.max(0, Math.round(ms / 86_400_000))
}

function hariLokal(iso: string): Date {
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1)
}

function isoHariIni(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

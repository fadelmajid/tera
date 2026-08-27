import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import { purchasesReport, salesReport, stockReport } from '../api/laporan'
import type { PurchasesReport, SalesReport, StockReport } from '../api/laporan'
import { formatIDR } from '../money'
import { Cari, Galat, Memuat, Stat, Tabel, Th, useCari, useUrut } from '../components/dasar'

/**
 * Laporan penjualan, pembelian dan stok. TASKS 6.1-6.3, R5.2-5.4.
 *
 * R5.7 is the user's own framing: reports can start simple provided the raw
 * data can be pulled. So these are period roll-ups with the breakdown
 * underneath and nothing more clever than that — the fidelity requirement is
 * carried by the export screen instead.
 *
 * Three tabs rather than three pages: they answer one question — how did this
 * month go — and switching between them should not lose the date range.
 */
export function LaporanPage({ entityId }: { entityId: string }) {
  const [tab, setTab] = useState<'penjualan' | 'pembelian' | 'stok'>('penjualan')
  const [{ from, to }, setRange] = useState(() => bulanIni())

  const tabs = [
    ['penjualan', 'Penjualan'],
    ['pembelian', 'Pembelian'],
    ['stok', 'Stok'],
  ] as const

  return (
    <>
      <h2>Laporan</h2>
      <p className="sub-judul">
        Ringkasan periode dengan rinciannya di bawah. Untuk data mentah selengkapnya, gunakan
        ekspor data.
      </p>

      <div className="tab-baris" role="tablist" aria-label="Jenis laporan">
        {tabs.map(([nilai, label]) => (
          <button
            key={nilai}
            type="button"
            role="tab"
            className="tab"
            aria-selected={tab === nilai}
            onClick={() => setTab(nilai)}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="tumpuk">
        <div className="card">
          <div className="alat">
            <label>
              <span>Dari</span>
              <input
                type="date"
                value={from}
                onChange={(e) => setRange((r) => ({ ...r, from: e.target.value }))}
              />
            </label>
            <label>
              <span>Sampai</span>
              <input
                type="date"
                value={to}
                onChange={(e) => setRange((r) => ({ ...r, to: e.target.value }))}
              />
            </label>
            <button className="sekunder" onClick={() => setRange(bulanIni())}>
              Bulan ini
            </button>
          </div>
        </div>

        {tab === 'penjualan' && <Penjualan entityId={entityId} from={from} to={to} />}
        {tab === 'pembelian' && <Pembelian entityId={entityId} from={from} to={to} />}
        {tab === 'stok' && <Stok entityId={entityId} from={from} to={to} />}
      </div>
    </>
  )
}

function bulanIni() {
  const now = new Date()
  const first = new Date(now.getFullYear(), now.getMonth(), 1)
  const last = new Date(now.getFullYear(), now.getMonth() + 1, 0)
  const iso = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  return { from: iso(first), to: iso(last) }
}

/** Loads one report and keeps the error beside it. */
function useLaporan<T>(muat: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null)
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const jalankan = useCallback(muat, deps)

  useEffect(() => {
    setMemuat(true)
    jalankan()
      .then((d) => {
        setData(d)
        setGalat(null)
      })
      .catch((err: unknown) => {
        setData(null)
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat laporan')
      })
      .finally(() => setMemuat(false))
  }, [jalankan])

  return { data, galat, memuat }
}

// --- penjualan (TASKS 6.1) --------------------------------------------------

function Penjualan({ entityId, from, to }: { entityId: string; from: string; to: string }) {
  const { data, galat, memuat } = useLaporan<SalesReport>(
    () => salesReport(entityId, from, to),
    [entityId, from, to],
  )

  const { q, setQ, hasil } = useCari(
    data?.by_product ?? [],
    (p) => `${p.product_code} ${p.product_name} ${p.owner_name ?? 'Perusahaan'}`,
  )
  const { hasil: produk, urutan, urutkan } = useUrut(hasil, {
    nama: (p) => p.product_name,
    owner: (p) => p.owner_name ?? 'Perusahaan',
    jumlah: (p) => p.qty,
    penjualan: (p) => p.revenue_idr,
    hpp: (p) => p.cogs_idr,
    margin: (p) => p.margin_idr,
  })

  if (galat) return <Galat pesan={galat} />
  if (memuat || !data) return <Memuat apa="laporan penjualan" />

  return (
    <>
      <div className="card">
        <div className="statistik">
          <Stat label="Nota" nilai={String(data.summary.sale_count)} />
          <Stat label="Diterima pelanggan" nilai={formatIDR(data.summary.total_idr)} />
          {/* Kept apart from the takings for the same reason the margin
              report keeps them apart: PPN is owed to the state and was
              never the shop's money (SPEC 2.4). */}
          <Stat label="Penjualan (DPP)" nilai={formatIDR(data.summary.dpp_idr)} />
          <Stat label="PPN" nilai={formatIDR(data.summary.ppn_idr)} />
          <Stat label="HPP" nilai={formatIDR(data.summary.cogs_idr)} />
          <Stat label="Margin kotor" nilai={formatIDR(data.summary.gross_margin_idr)} besar />
        </div>

        {data.summary.return_count > 0 && (
          <p className="catatan">
            Setelah {data.summary.return_count} retur ({formatIDR(data.summary.refund_idr)}{' '}
            dikembalikan), margin bersih {formatIDR(data.summary.net_margin_idr)}.
          </p>
        )}
        {/* Not hidden. A week with eleven voids is a training problem, and
            it is invisible on a report showing only what stuck. */}
        {data.summary.void_count > 0 && (
          <p className="catatan">
            {data.summary.void_count} nota dibatalkan senilai{' '}
            {formatIDR(data.summary.void_total_idr)} — tidak termasuk angka di atas.
          </p>
        )}
        {data.summary.credit_idr !== 0 && (
          <p className="catatan">
            {formatIDR(data.summary.credit_idr)} dijual secara kredit dan menjadi piutang.
          </p>
        )}
        <p className="disclaimer">{data.caveat}</p>
      </div>

      <div className="card">
        <div className="card-kepala">
          <h3>Per produk</h3>
        </div>
        {data.by_product.length === 0 ? (
          <p className="kosong">Belum ada penjualan pada periode ini.</p>
        ) : (
          <>
            <div className="alat">
              <Cari nilai={q} ubah={setQ} petunjuk="Nama produk, kode, owner" />
              <span className="hitung">
                {produk.length} dari {data.by_product.length} baris
              </span>
            </div>
            <Tabel label="Penjualan per produk">
              <thead>
                <tr>
                  <Th kunci="nama" urutan={urutan} urutkan={urutkan}>
                    Produk
                  </Th>
                  <Th kunci="owner" urutan={urutan} urutkan={urutkan}>
                    Owner
                  </Th>
                  <Th kunci="jumlah" urutan={urutan} urutkan={urutkan} angka>
                    Jumlah
                  </Th>
                  <Th kunci="penjualan" urutan={urutan} urutkan={urutkan} angka>
                    Penjualan
                  </Th>
                  <Th kunci="hpp" urutan={urutan} urutkan={urutkan} angka>
                    HPP
                  </Th>
                  <Th kunci="margin" urutan={urutan} urutkan={urutkan} angka>
                    Margin
                  </Th>
                </tr>
              </thead>
              <tbody>
                {produk.map((p) => (
                  <tr key={`${p.product_id}:${p.owner_id ?? ''}`}>
                    <td>
                      {p.product_name}
                      <br />
                      <small className="catatan">{p.product_code}</small>
                    </td>
                    {/* R2.2: unowned stock is a real bucket beside the family. */}
                    <td>{p.owner_name ?? 'Perusahaan'}</td>
                    <td className="angka">
                      {p.qty} {p.product_unit}
                    </td>
                    <td className="angka">{formatIDR(p.revenue_idr)}</td>
                    <td className="angka">{formatIDR(p.cogs_idr)}</td>
                    <td className="angka">{formatIDR(p.margin_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </>
        )}
      </div>

      <div className="baris">
        <div className="card">
          <h3>Per hari</h3>
          {data.by_day.length === 0 ? (
            <p className="kosong">Tidak ada penjualan.</p>
          ) : (
            <Tabel label="Penjualan per hari">
              <thead>
                <tr>
                  <th>Tanggal</th>
                  <th className="angka">Nota</th>
                  <th className="angka">Diterima</th>
                  <th className="angka">Margin</th>
                </tr>
              </thead>
              <tbody>
                {data.by_day.map((d) => (
                  <tr key={d.business_date}>
                    <td>{d.business_date}</td>
                    <td className="angka">{d.sale_count}</td>
                    <td className="angka">{formatIDR(d.total_idr)}</td>
                    <td className="angka">{formatIDR(d.margin_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>

        <div className="card">
          <h3>Cara bayar</h3>
          {data.by_method.length === 0 ? (
            <p className="kosong">Tidak ada pembayaran.</p>
          ) : (
            <Tabel label="Penjualan per cara bayar">
              <thead>
                <tr>
                  <th>Cara</th>
                  <th className="angka">Transaksi</th>
                  <th className="angka">Jumlah</th>
                </tr>
              </thead>
              <tbody>
                {data.by_method.map((m) => (
                  <tr key={m.method}>
                    <td>{m.method}</td>
                    <td className="angka">{m.payment_count}</td>
                    <td className="angka">{formatIDR(m.amount_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>
      </div>
    </>
  )
}

// --- pembelian (TASKS 6.2) --------------------------------------------------

function Pembelian({ entityId, from, to }: { entityId: string; from: string; to: string }) {
  const { data, galat, memuat } = useLaporan<PurchasesReport>(
    () => purchasesReport(entityId, from, to),
    [entityId, from, to],
  )

  if (galat) return <Galat pesan={galat} />
  if (memuat || !data) return <Memuat apa="laporan pembelian" />

  return (
    <>
      <div className="card">
        <div className="statistik">
          <Stat label="Nota" nilai={String(data.summary.purchase_count)} />
          <Stat label="Dibayar ke pemasok" nilai={formatIDR(data.summary.total_idr)} besar />
          <Stat label="Dengan faktur" nilai={formatIDR(data.summary.with_faktur_idr)} />
          <Stat label="Tanpa faktur" nilai={formatIDR(data.summary.without_faktur_idr)} />
        </div>

        {/* The figure this business cannot see today, and the reason
            purchase tracking is worth building at all (INV-9, SPEC 3.2). */}
        {data.summary.ppn_into_cost_idr !== 0 && (
          <div className="disclaimer">
            <strong>{formatIDR(data.summary.ppn_into_cost_idr)}</strong> PPN dibayar tanpa faktur
            pada periode ini. Tidak bisa dikreditkan, jadi masuk ke harga pokok barang — stok itu
            benar-benar lebih mahal, dan marginnya lebih tipis daripada yang terlihat di nota.
          </div>
        )}
      </div>

      <div className="card">
        <h3>Per pemasok</h3>
        {data.by_supplier.length === 0 ? (
          <p className="kosong">Belum ada pembelian pada periode ini.</p>
        ) : (
          <Tabel label="Pembelian per pemasok">
            <thead>
              <tr>
                <th>Pemasok</th>
                <th className="angka">Nota</th>
                <th className="angka">Dengan faktur</th>
                <th className="angka">Dibayar</th>
                <th className="angka">PPN jadi biaya</th>
              </tr>
            </thead>
            <tbody>
              {data.by_supplier.map((s) => (
                <tr key={s.supplier_id}>
                  <td>
                    {s.supplier_name}
                    <br />
                    <small className="catatan">{s.supplier_code}</small>
                  </td>
                  <td className="angka">{s.purchase_count}</td>
                  <td className="angka">
                    {s.with_faktur_count} / {s.purchase_count}
                  </td>
                  <td className="angka">{formatIDR(s.total_idr)}</td>
                  <td className={s.ppn_into_cost_idr !== 0 ? 'angka teks-bahaya' : 'angka'}>
                    {s.ppn_into_cost_idr !== 0 ? formatIDR(s.ppn_into_cost_idr) : '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </Tabel>
        )}
        {/* R10.6: comparing suppliers on price alone is comparing the wrong
            number at a PKP company. */}
        <p className="catatan">
          Pemasok yang selalu memberi faktur efektif lebih murah bagi perusahaan PKP: PPN-nya bisa
          dikreditkan, bukan menjadi biaya.
        </p>
      </div>

      <div className="card">
        <h3>Per produk</h3>
        {data.by_product.length === 0 ? (
          <p className="kosong">Belum ada pembelian pada periode ini.</p>
        ) : (
          <Tabel label="Pembelian per produk">
            <thead>
              <tr>
                <th>Produk</th>
                <th className="angka">Jumlah</th>
                <th className="angka">Dibayar</th>
                <th className="angka">Masuk biaya persediaan</th>
              </tr>
            </thead>
            <tbody>
              {data.by_product.map((p) => (
                <tr key={p.product_id}>
                  <td>
                    {p.product_name}
                    <br />
                    <small className="catatan">{p.product_code}</small>
                  </td>
                  <td className="angka">
                    {p.qty} {p.product_unit}
                  </td>
                  <td className="angka">{formatIDR(p.gross_idr)}</td>
                  <td className="angka">{formatIDR(p.cost_total_idr)}</td>
                </tr>
              ))}
            </tbody>
          </Tabel>
        )}
      </div>
    </>
  )
}

// --- stok (TASKS 6.3) -------------------------------------------------------

function Stok({ entityId, from, to }: { entityId: string; from: string; to: string }) {
  const { data, galat, memuat } = useLaporan<StockReport>(
    () => stockReport(entityId, from, to),
    [entityId, from, to],
  )

  const { q, setQ, hasil } = useCari(
    data?.on_hand ?? [],
    (s) => `${s.product_code} ${s.product_name} ${s.owner_name ?? 'Perusahaan'} ${s.category ?? ''}`,
  )
  const { hasil: rak, urutan, urutkan } = useUrut(hasil, {
    nama: (s) => s.product_name,
    owner: (s) => s.owner_name ?? 'Perusahaan',
    sisa: (s) => s.qty_on_hand,
    nilai: (s) => s.value_idr,
  })

  if (galat) return <Galat pesan={galat} />
  if (memuat || !data) return <Memuat apa="laporan stok" />

  return (
    <>
      <div className="card">
        <div className="statistik">
          <Stat label="Baris stok" nilai={String(data.summary.lines)} />
          <Stat label="Unit di rak" nilai={String(data.summary.qty_total)} />
          <Stat label="Nilai persediaan" nilai={formatIDR(data.summary.value_idr)} besar />
        </div>
        {/* Said plainly rather than implied: this system does not
            reconstruct historical balances, so the shelf is now and the
            movement is the period. */}
        <p className="catatan">
          Sisa stok dihitung <strong>saat ini</strong>, bukan per akhir periode. Pergerakan di bawah
          mengikuti periode yang dipilih.
        </p>
      </div>

      <div className="card">
        <h3>Sisa stok</h3>
        {data.on_hand.length === 0 ? (
          <p className="kosong">Tidak ada stok di perusahaan ini.</p>
        ) : (
          <>
            <div className="alat">
              <Cari nilai={q} ubah={setQ} petunjuk="Nama produk, kode, owner, kategori" />
              <span className="hitung">
                {rak.length} dari {data.on_hand.length} baris
              </span>
            </div>
            <Tabel label="Sisa stok per produk dan pemilik">
              <thead>
                <tr>
                  <Th kunci="nama" urutan={urutan} urutkan={urutkan}>
                    Produk
                  </Th>
                  <Th kunci="owner" urutan={urutan} urutkan={urutkan}>
                    Owner
                  </Th>
                  <Th kunci="sisa" urutan={urutan} urutkan={urutkan} angka>
                    Sisa
                  </Th>
                  <Th kunci="nilai" urutan={urutan} urutkan={urutkan} angka>
                    Nilai
                  </Th>
                  <Th angka>Lapisan</Th>
                </tr>
              </thead>
              <tbody>
                {rak.map((s) => (
                  <tr key={`${s.product_id}:${s.owner_id ?? ''}`}>
                    <td>
                      {s.product_name}
                      <br />
                      <small className="catatan">{s.product_code}</small>
                    </td>
                    {/* Stock is owner-attributed; a total mixing two family
                        members' goods is useless to both (INV-8). */}
                    <td>{s.owner_name ?? 'Perusahaan'}</td>
                    <td className="angka">
                      {s.qty_on_hand} {s.product_unit}
                    </td>
                    <td className="angka">{formatIDR(s.value_idr)}</td>
                    <td className="angka">
                      {s.layer_count}
                      {s.layers_without_faktur > 0 && (
                        <>
                          <br />
                          <small className="catatan">
                            {s.layers_without_faktur} tanpa faktur
                          </small>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </>
        )}
      </div>

      {data.out_of_stock.length > 0 && (
        <div className="card">
          <h3>Habis ({data.out_of_stock.length})</h3>
          {/* Not the same question as "what is on the shelf", and it is the
              one that costs a sale: a catalogue entry the cashier can scan
              and cannot sell. */}
          <p className="catatan">
            Produk aktif yang tidak punya stok sama sekali di perusahaan ini.
          </p>
          <Tabel label="Produk yang habis">
            <thead>
              <tr>
                <th>Kode</th>
                <th>Produk</th>
              </tr>
            </thead>
            <tbody>
              {data.out_of_stock.map((p) => (
                <tr key={p.product_id}>
                  <td>{p.product_code}</td>
                  <td>{p.product_name}</td>
                </tr>
              ))}
            </tbody>
          </Tabel>
        </div>
      )}

      <div className="baris">
        <div className="card">
          <h3>Barang masuk</h3>
          {data.intake.length === 0 ? (
            <p className="kosong">Tidak ada barang masuk pada periode ini.</p>
          ) : (
            <Tabel label="Barang masuk">
              <thead>
                <tr>
                  <th>Produk</th>
                  <th>Asal</th>
                  <th className="angka">Jumlah</th>
                  <th className="angka">Biaya</th>
                </tr>
              </thead>
              <tbody>
                {data.intake.map((i) => (
                  <tr key={`${i.product_code}:${i.source}`}>
                    <td>{i.product_name}</td>
                    <td>{i.source}</td>
                    <td className="angka">{i.qty}</td>
                    <td className="angka">{formatIDR(i.cost_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>

        <div className="card">
          <h3>Barang keluar</h3>
          {data.movement.length === 0 ? (
            <p className="kosong">Tidak ada barang keluar pada periode ini.</p>
          ) : (
            <Tabel label="Barang keluar">
              <thead>
                <tr>
                  <th>Produk</th>
                  <th>Sebab</th>
                  <th className="angka">Jumlah</th>
                  <th className="angka">HPP</th>
                </tr>
              </thead>
              <tbody>
                {data.movement.map((m) => (
                  <tr key={`${m.product_code}:${m.movement_type}`}>
                    <td>{m.product_name}</td>
                    <td>{m.movement_type}</td>
                    <td className="angka">{m.qty}</td>
                    <td className="angka">{formatIDR(m.cost_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>
      </div>
    </>
  )
}

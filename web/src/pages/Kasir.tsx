import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { listProducts, listOwners, listCustomers } from '../api/masterdata'
import type { Product, Owner, Customer } from '../api/masterdata'
import {
  ringSale,
  currentSession,
  openSession,
  receiptPreview,
  printReceipt,
  METODE_BAYAR,
} from '../api/kasir'
import type { CashSession, SaleResult } from '../api/kasir'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO, add, sub, mulQty } from '../money'
import type { IDR } from '../money'
import { Galat, Teks } from '../components/dasar'

interface Item {
  product: Product
  qty: number
  unitPrice: IDR
  discount: IDR
}

/**
 * Kasir — the till (TASKS 2.3, 2.4, R9).
 *
 * Built for a shop floor: large targets, few decisions per sale, and the total
 * always on screen. The cashier uses this all day.
 *
 * Barcode input is a keyboard wedge (R9.3). The scanner types the code and
 * presses Enter, which is indistinguishable from a person typing fast — so the
 * search box stays focused and an exact code match adds the item straight to
 * the cart rather than showing a list of one.
 */
export function KasirPage({ entityId, isPKP }: { entityId: string; isPKP: boolean }) {
  const [produk, setProduk] = useState<Product[]>([])
  const [owners, setOwners] = useState<Owner[]>([])
  const [pelanggan, setPelanggan] = useState<Customer[]>([])
  const [sesi, setSesi] = useState<CashSession | null>(null)
  const [galat, setGalat] = useState<string | null>(null)

  const [cari, setCari] = useState('')
  const [kategori, setKategori] = useState('')
  const [keranjang, setKeranjang] = useState<Item[]>([])
  const [diskonNota, setDiskonNota] = useState('')
  const [metode, setMetode] = useState('TUNAI')
  const [dibayar, setDibayar] = useState('')
  const [referensi, setReferensi] = useState('')
  const [kredit, setKredit] = useState(false)
  const [customerId, setCustomerId] = useState('')
  const [tempo, setTempo] = useState('')
  const [fakturDiterbitkan, setFakturDiterbitkan] = useState(false)
  const [sedang, setSedang] = useState(false)
  const [struk, setStruk] = useState<{ hasil: SaleResult; teks: string } | null>(null)

  const cariRef = useRef<HTMLInputElement>(null)
  const [modalAwal, setModalAwal] = useState('')

  const muat = useCallback(async () => {
    try {
      setProduk(await listProducts(entityId))
      setOwners(await listOwners(entityId))
      setPelanggan(await listCustomers(entityId))
      setSesi(await currentSession(entityId))
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data')
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  // The scanner is a keyboard. Keeping focus here means a scan lands in the
  // search box wherever the cashier last clicked.
  useEffect(() => {
    cariRef.current?.focus()
  }, [keranjang.length, struk])

  const kategoriList = useMemo(
    () => [...new Set(produk.map((p) => p.category).filter((c): c is string => !!c))].sort(),
    [produk],
  )

  const terlihat = useMemo(() => {
    const q = cari.trim().toLowerCase()
    return produk.filter((p) => {
      if (kategori && p.category !== kategori) return false
      if (!q) return true
      return (
        p.name.toLowerCase().includes(q) ||
        p.code.toLowerCase().includes(q) ||
        (p.barcode ?? '').toLowerCase().includes(q)
      )
    })
  }, [produk, cari, kategori])

  const namaOwner = (id: string | null) =>
    id === null ? 'Perusahaan' : (owners.find((o) => o.id === id)?.name ?? '—')

  function tambah(p: Product) {
    setKeranjang((rows) => {
      const found = rows.findIndex((r) => r.product.id === p.id)
      if (found >= 0) {
        return rows.map((r, i) => (i === found ? { ...r, qty: r.qty + 1 } : r))
      }
      return [...rows, { product: p, qty: 1, unitPrice: p.sale_price_idr, discount: ZERO }]
    })
    setCari('')
  }

  /** R9.3: the wedge types the code then presses Enter. */
  function onCariKey(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key !== 'Enter') return
    e.preventDefault()
    const q = cari.trim().toLowerCase()
    if (!q) return

    const exact = produk.find(
      (p) => (p.barcode ?? '').toLowerCase() === q || p.code.toLowerCase() === q,
    )
    if (exact) {
      tambah(exact)
      return
    }
    const only = terlihat.length === 1 ? terlihat[0] : undefined
    if (only) {
      tambah(only)
      return
    }
    setGalat(`Tidak ada produk dengan kode atau barcode "${cari.trim()}"`)
  }

  const bruto = keranjang.reduce((acc, i) => add(acc, mulQty(i.unitPrice, i.qty)), ZERO)
  const diskonBaris = keranjang.reduce((acc, i) => add(acc, i.discount), ZERO)

  let diskonNotaIdr = ZERO
  let diskonSalah = false
  try {
    diskonNotaIdr = diskonNota.trim() === '' ? ZERO : parseIDR(diskonNota)
  } catch {
    diskonSalah = true
  }
  const total = sub(sub(bruto, diskonBaris), diskonNotaIdr)

  let dibayarIdr = ZERO
  let bayarSalah = false
  try {
    dibayarIdr = dibayar.trim() === '' ? total : parseIDR(dibayar)
  } catch {
    bayarSalah = true
  }
  const kembali = dibayarIdr > total ? sub(dibayarIdr, total) : ZERO
  const kurang = total > dibayarIdr ? sub(total, dibayarIdr) : ZERO

  async function bukaSesi() {
    try {
      const s = await openSession(entityId, modalAwal.trim() === '' ? ZERO : parseIDR(modalAwal))
      setSesi(s)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal membuka sesi kas')
    }
  }

  async function bayar() {
    setSedang(true)
    try {
      const hasil = await ringSale(entityId, {
        customer_id: customerId,
        invoice_discount_idr: diskonNotaIdr,
        faktur_issued: fakturDiterbitkan,
        is_credit: kredit,
        due_date: kredit ? tempo : '',
        lines: keranjang.map((i) => ({
          product_id: i.product.id,
          qty: i.qty,
          unit_price_idr: i.unitPrice,
          line_discount_idr: i.discount,
        })),
        payments: kredit
          ? [{ method: 'KREDIT', amount_idr: total }]
          : [{ method: metode, amount_idr: dibayarIdr, reference: referensi }],
      })

      const preview = await receiptPreview(entityId, hasil.sale.id)
      setStruk({ hasil, teks: preview.text })

      setKeranjang([])
      setDiskonNota('')
      setDibayar('')
      setReferensi('')
      setKredit(false)
      setCustomerId('')
      setFakturDiterbitkan(false)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan penjualan')
    } finally {
      setSedang(false)
    }
  }

  if (!sesi) {
    return (
      <>
        <h2>Kasir</h2>
        <Galat pesan={galat} />
        <div className="card">
          <h3>Buka sesi kas</h3>
          <p style={{ color: 'var(--muted)' }}>
            Penjualan dicatat dalam satu sesi kas supaya bisa dicocokkan di akhir hari. Selama sesi
            masih terbuka, penjualan yang salah bisa dibatalkan; setelah ditutup, koreksinya lewat
            retur.
          </p>
          <Teks
            label="Modal awal laci"
            nilai={modalAwal}
            ubah={setModalAwal}
            petunjuk="Rupiah bulat, mis. 500.000"
          />
          <button onClick={() => void bukaSesi()}>Buka sesi</button>
        </div>
      </>
    )
  }

  if (struk) {
    return (
      <>
        <h2>Penjualan tersimpan</h2>
        <Galat pesan={galat} />
        <div className="card">
          <p>
            Nota <strong>{struk.hasil.sale.invoice_no}</strong> — total{' '}
            <strong>{formatIDR(struk.hasil.sale.total_idr)}</strong>
          </p>
          {struk.hasil.receivable && (
            <p>
              Piutang tercatat <strong>{formatIDR(struk.hasil.receivable.amount_idr)}</strong>
              {struk.hasil.receivable.due_date ? `, jatuh tempo ${struk.hasil.receivable.due_date}` : ''}.
            </p>
          )}
          {!struk.hasil.printed && (
            <p style={{ color: 'var(--muted)' }}>
              {struk.hasil.print_error
                ? `Struk gagal dicetak: ${struk.hasil.print_error}. Penjualan tetap tersimpan.`
                : 'Printer belum dikonfigurasi. Penjualan tetap tersimpan.'}
            </p>
          )}
          <pre
            style={{
              background: 'var(--bg)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--radius)',
              padding: 12,
              fontSize: 13,
              overflowX: 'auto',
            }}
          >
            {struk.teks}
          </pre>
          <p>
            <button
              onClick={() => {
                void printReceipt(entityId, struk.hasil.sale.id).catch((err: unknown) =>
                  setGalat(err instanceof ApiError ? err.message : 'Gagal mencetak'),
                )
              }}
            >
              Cetak ulang
            </button>{' '}
            <button className="sekunder" onClick={() => setStruk(null)}>
              Penjualan berikutnya
            </button>
          </p>
        </div>
      </>
    )
  }

  return (
    <>
      <h2>Kasir</h2>
      <Galat pesan={galat} />

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 3fr) minmax(320px, 2fr)', gap: 16 }}>
        <div className="card">
          <input
            ref={cariRef}
            value={cari}
            onChange={(e) => setCari(e.target.value)}
            onKeyDown={onCariKey}
            placeholder="Scan barcode atau cari produk…"
            style={{ fontSize: 18, padding: 12 }}
          />

          {kategoriList.length > 0 && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, margin: '10px 0' }}>
              <button
                className={kategori === '' ? undefined : 'sekunder'}
                onClick={() => setKategori('')}
              >
                Semua
              </button>
              {kategoriList.map((k) => (
                <button
                  key={k}
                  className={kategori === k ? undefined : 'sekunder'}
                  onClick={() => setKategori(k)}
                >
                  {k}
                </button>
              ))}
            </div>
          )}

          {/* A grid is fine at 100-1,000 SKUs (R9.2). */}
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))',
              gap: 8,
              maxHeight: 420,
              overflowY: 'auto',
            }}
          >
            {terlihat.map((p) => (
              <button
                key={p.id}
                className="sekunder"
                onClick={() => tambah(p)}
                style={{ textAlign: 'left', padding: 10, height: 'auto' }}
              >
                <div style={{ fontWeight: 600 }}>{p.name}</div>
                <div style={{ fontSize: 13, color: 'var(--muted)' }}>
                  {formatIDR(p.sale_price_idr)} · {namaOwner(p.owner_id)}
                </div>
              </button>
            ))}
            {terlihat.length === 0 && <p className="kosong">Tidak ada produk.</p>}
          </div>
        </div>

        <div className="card">
          <h3 style={{ marginTop: 0 }}>Keranjang</h3>
          {keranjang.length === 0 ? (
            <p className="kosong">Kosong. Scan atau pilih produk.</p>
          ) : (
            <table>
              <tbody>
                {keranjang.map((i, n) => (
                  <tr key={i.product.id}>
                    <td>
                      {i.product.name}
                      <br />
                      <small style={{ color: 'var(--muted)' }}>
                        {formatIDR(i.unitPrice)} · {namaOwner(i.product.owner_id)}
                      </small>
                    </td>
                    <td style={{ width: 70 }}>
                      <input
                        type="number"
                        min={1}
                        value={i.qty}
                        onChange={(e) =>
                          setKeranjang((rows) =>
                            rows.map((r, x) =>
                              x === n ? { ...r, qty: Math.max(1, Number(e.target.value) || 1) } : r,
                            ),
                          )
                        }
                      />
                    </td>
                    <td className="angka">{formatIDR(mulQty(i.unitPrice, i.qty))}</td>
                    <td style={{ width: 36 }}>
                      <button
                        className="sekunder"
                        onClick={() => setKeranjang((rows) => rows.filter((_, x) => x !== n))}
                      >
                        ×
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 10, marginTop: 10 }}>
            <Teks label="Diskon nota" nilai={diskonNota} ubah={setDiskonNota} petunjuk="Rupiah bulat" />
            <p style={{ fontSize: 22, margin: '10px 0' }}>
              Total <strong className="angka">{formatIDR(total)}</strong>
            </p>

            {!kredit && (
              <>
                <label>
                  <span>Metode bayar</span>
                  <select value={metode} onChange={(e) => setMetode(e.target.value)}>
                    {METODE_BAYAR.map(([nilai, label]) => (
                      <option key={nilai} value={nilai}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <Teks label="Dibayar" nilai={dibayar} ubah={setDibayar} petunjuk="Kosong = pas" />
                {metode !== 'TUNAI' && (
                  <Teks
                    label="Referensi"
                    nilai={referensi}
                    ubah={setReferensi}
                    petunjuk="No. transaksi transfer/QRIS — dicatat, bukan diproses"
                  />
                )}
                {kembali > 0 && (
                  <p>
                    Kembali <strong className="angka">{formatIDR(kembali)}</strong>
                  </p>
                )}
                {kurang > 0 && (
                  <p style={{ color: 'var(--danger)' }}>
                    Kurang <strong className="angka">{formatIDR(kurang)}</strong>
                  </p>
                )}
              </>
            )}

            <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <input type="checkbox" checked={kredit} onChange={(e) => setKredit(e.target.checked)} />
              <span style={{ margin: 0 }}>Penjualan kredit (piutang)</span>
            </label>

            {kredit && (
              <>
                <label>
                  <span>Pelanggan *</span>
                  <select value={customerId} onChange={(e) => setCustomerId(e.target.value)} required>
                    <option value="">Pilih pelanggan</option>
                    {pelanggan.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
                <Teks label="Jatuh tempo" nilai={tempo} ubah={setTempo} tipe="date" />
              </>
            )}

            {isPKP && (
              <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginTop: 8 }}>
                <input
                  type="checkbox"
                  checked={fakturDiterbitkan}
                  onChange={(e) => setFakturDiterbitkan(e.target.checked)}
                />
                <span style={{ margin: 0 }}>
                  Faktur pajak diterbitkan
                  <br />
                  <small style={{ color: 'var(--muted)' }}>
                    Dicatat terpisah dari PPN. PPN keluaran tetap terutang walaupun pembeli tidak
                    minta faktur.
                  </small>
                </span>
              </label>
            )}

            <button
              onClick={() => void bayar()}
              disabled={sedang || keranjang.length === 0 || diskonSalah || bayarSalah}
              style={{ width: '100%', fontSize: 18, padding: 14, marginTop: 12 }}
            >
              {sedang ? 'Menyimpan…' : 'Bayar'}
            </button>
          </div>
        </div>
      </div>
    </>
  )
}

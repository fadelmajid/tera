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

interface Struk {
  hasil: SaleResult
  teks: string
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
 *
 * # The till never leaves the screen
 *
 * Two states used to replace the whole page: the receipt after a sale, and the
 * "open a session" form before the first one. Both meant that a cashier
 * arriving in the morning, or finishing any transaction, was looking at a
 * screen that did not resemble the till they were trained on — and getting
 * back cost a click and a full re-orientation, several hundred times a day.
 *
 * Now both are panels in the right-hand column. The receipt appears above a
 * cart that is already empty and already focused, so the next barcode scan
 * simply works and the receipt slides out of the way when it is scrolled past.
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
  const [struk, setStruk] = useState<Struk | null>(null)

  const cariRef = useRef<HTMLInputElement>(null)
  const [modalAwal, setModalAwal] = useState('')
  const [membuka, setMembuka] = useState(false)

  const muat = useCallback(async () => {
    try {
      const [p, o, c, s] = await Promise.all([
        listProducts(entityId),
        listOwners(entityId),
        listCustomers(entityId),
        currentSession(entityId),
      ])
      setProduk(p)
      setOwners(o)
      setPelanggan(c)
      setSesi(s)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data')
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  useEffect(() => {
    if (sesi) cariRef.current?.focus()
  }, [keranjang.length, struk, sesi])

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
    setGalat(null)
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

  /* A cash sale that does not cover the total is not a cash sale. The screen
     used to show "Kurang Rp x" in red and leave the button enabled, so the
     only thing standing between a short payment and the books was the
     cashier noticing the colour. A part payment is a credit sale; that path
     is one checkbox away and records a piutang against a named customer. */
  const kurangBayar = !kredit && kurang > 0
  const kreditTanpaPelanggan = kredit && customerId === ''
  const bisaBayar =
    !sedang &&
    keranjang.length > 0 &&
    !diskonSalah &&
    !bayarSalah &&
    !kurangBayar &&
    !kreditTanpaPelanggan

  async function bukaSesi() {
    setMembuka(true)
    try {
      const s = await openSession(entityId, modalAwal.trim() === '' ? ZERO : parseIDR(modalAwal))
      setSesi(s)
      setModalAwal('')
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal membuka sesi kas')
    } finally {
      setMembuka(false)
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

      const preview = await receiptPreview(entityId, hasil.sale.id).catch(() => ({ text: '' }))
      setStruk({ hasil, teks: preview.text })

      setKeranjang([])
      setDiskonNota('')
      setDibayar('')
      setReferensi('')
      setKredit(false)
      setCustomerId('')
      setTempo('')
      setFakturDiterbitkan(false)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan penjualan')
    } finally {
      setSedang(false)
    }
  }

  return (
    <>
      <div className="card-kepala">
        <h2>Kasir</h2>
        {sesi ? (
          <span className="lencana lencana-aman">Sesi kas terbuka · {sesi.business_date}</span>
        ) : (
          <span className="lencana lencana-hati">Sesi kas tertutup</span>
        )}
      </div>

      <Galat pesan={galat} />

      <div className="kasir">
        <div className="card">
          <label>
            <span>Scan atau cari produk</span>
            <input
              ref={cariRef}
              className="kasir-cari"
              value={cari}
              onChange={(e) => setCari(e.target.value)}
              onKeyDown={onCariKey}
              placeholder="Scan barcode atau ketik nama produk…"
              disabled={!sesi}
              aria-describedby="kasir-hitung"
            />
          </label>

          {kategoriList.length > 0 && (
            <div className="chip-baris" role="group" aria-label="Saring kategori">
              <button
                type="button"
                className="chip"
                aria-pressed={kategori === ''}
                onClick={() => setKategori('')}
              >
                Semua
              </button>
              {kategoriList.map((k) => (
                <button
                  key={k}
                  type="button"
                  className="chip"
                  aria-pressed={kategori === k}
                  onClick={() => setKategori(k)}
                >
                  {k}
                </button>
              ))}
            </div>
          )}

          <p className="catatan" id="kasir-hitung">
            {terlihat.length} produk ditampilkan
          </p>

          <div className="kasir-rak">
            {terlihat.map((p) => (
              <button
                key={p.id}
                type="button"
                className="ubin"
                onClick={() => tambah(p)}
                disabled={!sesi}
              >
                <span className="ubin-nama">{p.name}</span>
                <span className="ubin-ket">
                  {formatIDR(p.sale_price_idr)} · {namaOwner(p.owner_id)}
                </span>
              </button>
            ))}
          </div>
          {terlihat.length === 0 && <p className="kosong">Tidak ada produk yang cocok.</p>}
        </div>

        <div className="kasir-samping">
          {!sesi && (
            <div className="card">
              <h3>Buka sesi kas</h3>
              <p className="catatan">
                Penjualan dicatat dalam satu sesi kas supaya bisa dicocokkan di akhir hari. Selama
                sesi masih terbuka, penjualan yang salah bisa dibatalkan; setelah ditutup,
                koreksinya lewat retur.
              </p>
              <Teks
                label="Modal awal laci"
                nilai={modalAwal}
                ubah={setModalAwal}
                petunjuk="Rupiah bulat, mis. 500.000. Kosongkan jika laci mulai kosong."
              />
              <button onClick={() => void bukaSesi()} disabled={membuka}>
                {membuka ? 'Membuka…' : 'Buka sesi'}
              </button>
            </div>
          )}

          {struk && (
            <StrukPanel
              struk={struk}
              entityId={entityId}
              tutup={() => setStruk(null)}
              lapor={setGalat}
            />
          )}

          {sesi && (
            <div className="card">
              <h3>Keranjang</h3>
              {keranjang.length === 0 ? (
                <p className="kosong">Kosong. Scan atau pilih produk.</p>
              ) : (
                <div className="tabel-gulir">
                  <table>
                    <caption className="mikro">
                      {keranjang.length} baris
                    </caption>
                    <tbody>
                      {keranjang.map((i, n) => (
                        <tr key={i.product.id}>
                          <td>
                            {i.product.name}
                            <br />
                            <small className="catatan">
                              {formatIDR(i.unitPrice)} · {namaOwner(i.product.owner_id)}
                            </small>
                          </td>
                          <td style={{ width: 74 }}>
                            <input
                              type="number"
                              min={1}
                              value={i.qty}
                              aria-label={`Jumlah ${i.product.name}`}
                              onChange={(e) =>
                                setKeranjang((rows) =>
                                  rows.map((r, x) =>
                                    x === n
                                      ? { ...r, qty: Math.max(1, Number(e.target.value) || 1) }
                                      : r,
                                  ),
                                )
                              }
                            />
                          </td>
                          <td className="angka">{formatIDR(mulQty(i.unitPrice, i.qty))}</td>
                          <td style={{ width: 40 }}>
                            <button
                              type="button"
                              className="sekunder ikon-saja"
                              aria-label={`Hapus ${i.product.name} dari keranjang`}
                              onClick={() =>
                                setKeranjang((rows) => rows.filter((_, x) => x !== n))
                              }
                            >
                              ×
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              <Teks
                label="Diskon nota"
                nilai={diskonNota}
                ubah={setDiskonNota}
                petunjuk="Rupiah bulat"
                inputMode="numeric"
              />

              <div className="kasir-total">
                <span className="label">Total</span>
                <span className="nilai">{formatIDR(total)}</span>
              </div>

              <label className="centang">
                <input
                  type="checkbox"
                  checked={kredit}
                  onChange={(e) => setKredit(e.target.checked)}
                />
                <span>
                  Penjualan kredit (piutang)
                  <small className="petunjuk">
                    Barang keluar sekarang, uangnya ditagih nanti. Wajib atas nama pelanggan.
                  </small>
                </span>
              </label>

              {kredit ? (
                <>
                  <label>
                    <span>Pelanggan *</span>
                    <select
                      value={customerId}
                      onChange={(e) => setCustomerId(e.target.value)}
                      required
                    >
                      <option value="">Pilih pelanggan</option>
                      {pelanggan.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name}
                        </option>
                      ))}
                    </select>
                    {kreditTanpaPelanggan && (
                      <small className="petunjuk">
                        Piutang harus punya nama. Tambahkan pelanggan lebih dulu bila belum ada.
                      </small>
                    )}
                  </label>
                  <Teks label="Jatuh tempo" nilai={tempo} ubah={setTempo} tipe="date" />
                </>
              ) : (
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
                  <Teks
                    label="Dibayar"
                    nilai={dibayar}
                    ubah={setDibayar}
                    petunjuk="Kosongkan bila uangnya pas"
                    inputMode="numeric"
                  />
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
                      Kembali <strong className="angka-kiri">{formatIDR(kembali)}</strong>
                    </p>
                  )}
                  {kurangBayar && (
                    <div className="galat" role="alert">
                      Kurang <strong className="angka-kiri">{formatIDR(kurang)}</strong>. Terima
                      uangnya penuh, atau centang penjualan kredit supaya sisanya tercatat sebagai
                      piutang atas nama pelanggan.
                    </div>
                  )}
                </>
              )}

              {isPKP && (
                <label className="centang">
                  <input
                    type="checkbox"
                    checked={fakturDiterbitkan}
                    onChange={(e) => setFakturDiterbitkan(e.target.checked)}
                  />
                  <span>
                    Faktur pajak diterbitkan
                    <small className="petunjuk">
                      Dicatat terpisah dari PPN. PPN keluaran tetap terutang walaupun pembeli tidak
                      minta faktur.
                    </small>
                  </span>
                </label>
              )}

              <button className="bayar" onClick={() => void bayar()} disabled={!bisaBayar}>
                {sedang ? 'Menyimpan…' : `Bayar ${formatIDR(total)}`}
              </button>
            </div>
          )}
        </div>
      </div>
    </>
  )
}

/**
 * The receipt for the sale just rung.
 *
 * A panel above the cart rather than a page. The cashier does not have to
 * dismiss it before serving the next customer — scanning simply fills the cart
 * underneath it — but it stays until it is dismissed, so a printer failure is
 * not something that scrolls away unnoticed.
 */
function StrukPanel({
  struk,
  entityId,
  tutup,
  lapor,
}: {
  struk: Struk
  entityId: string
  tutup: () => void
  lapor: (pesan: string | null) => void
}) {
  const { sale } = struk.hasil
  const [mencetak, setMencetak] = useState(false)

  const cetak = () => {
    setMencetak(true)
    printReceipt(entityId, sale.id)
      .then(() => lapor(null))
      .catch((err: unknown) =>
        lapor(err instanceof ApiError ? err.message : 'Gagal mencetak'),
      )
      .finally(() => setMencetak(false))
  }

  return (
    <div className="card">
      <div className="card-kepala">
        <h3>Tersimpan — {sale.invoice_no}</h3>
        <button type="button" className="sekunder ikon-saja" onClick={tutup} aria-label="Tutup struk">
          ×
        </button>
      </div>

      <div className="berhasil" role="status">
        Total <strong className="angka-kiri">{formatIDR(sale.total_idr)}</strong>
        {sale.ppn_idr !== 0 && (
          <>
            {' '}
            — {sale.ppn_inclusive ? 'termasuk' : 'ditambah'} PPN{' '}
            <strong className="angka-kiri">{formatIDR(sale.ppn_idr)}</strong> atas DPP{' '}
            <strong className="angka-kiri">{formatIDR(sale.dpp_idr)}</strong>
            {!sale.faktur_issued && ' — tanpa faktur, PPN tetap terutang'}
          </>
        )}
        .
      </div>

      {struk.hasil.receivable && (
        <p className="catatan">
          Piutang tercatat{' '}
          <strong className="angka-kiri">{formatIDR(struk.hasil.receivable.amount_idr)}</strong>
          {struk.hasil.receivable.due_date
            ? `, jatuh tempo ${struk.hasil.receivable.due_date}`
            : ', tanpa jatuh tempo — umurnya tidak bisa dihitung di laporan piutang'}
          .
        </p>
      )}

      {!struk.hasil.printed && (
        <div className="disclaimer">
          {struk.hasil.print_error
            ? `Struk gagal dicetak: ${struk.hasil.print_error}. Penjualan tetap tersimpan.`
            : 'Printer belum dikonfigurasi. Penjualan tetap tersimpan.'}
        </div>
      )}

      {struk.teks && <pre className="struk">{struk.teks}</pre>}

      <div className="aksi">
        <button className="sekunder" onClick={cetak} disabled={mencetak}>
          {mencetak ? 'Mencetak…' : 'Cetak ulang'}
        </button>
        <button className="sekunder" onClick={tutup}>
          Tutup
        </button>
      </div>
    </div>
  )
}

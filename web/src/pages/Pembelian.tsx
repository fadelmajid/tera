import { useState, type FormEvent } from 'react'
import { listProducts, listSuppliers, listOwners } from '../api/masterdata'
import type { Product, Supplier, Owner } from '../api/masterdata'
import { createPurchase, listPurchases } from '../api/pembelian'
import type { Purchase, PurchaseLineBody, PurchaseResult } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO, add, mulQty } from '../money'
import type { IDR } from '../money'
import { useDaftar, Galat, Teks, Centang } from '../components/dasar'

interface Baris {
  productId: string
  jumlah: string
  harga: string
  ppn: string
}

const barisKosong = (): Baris => ({ productId: '', jumlah: '1', harga: '', ppn: '' })

/**
 * Pembelian — the purchasing screen (TASKS 1.8, R10).
 *
 * The faktur toggle is the loudest thing on this page on purpose. It is one
 * checkbox, and it changes what every layer on the invoice cost by ~11%
 * (SPEC §3.2, INV-9). Nothing else here has that leverage, and the business
 * currently has no way to see the difference at all.
 *
 * The PPN is typed in from the supplier's invoice rather than computed. No rate
 * is hardcoded anywhere (INV-4), and what the supplier actually charged is a
 * fact about a piece of paper, not a calculation.
 */
export function PembelianPage({ entityId, isPKP }: { entityId: string; isPKP: boolean }) {
  const pembelian = useDaftar<Purchase>(() => listPurchases(entityId), [entityId])
  const produk = useDaftar<Product>(() => listProducts(entityId), [entityId])
  const pemasok = useDaftar<Supplier>(() => listSuppliers(entityId), [entityId])
  const owners = useDaftar<Owner>(() => listOwners(entityId), [entityId])

  const [supplierId, setSupplierId] = useState('')
  const [noFaktur, setNoFaktur] = useState('')
  const [noInvoice, setNoInvoice] = useState('')
  const [tanggal, setTanggal] = useState('')
  const [fakturDiterima, setFakturDiterima] = useState(false)
  const [kredit, setKredit] = useState(false)
  const [jatuhTempo, setJatuhTempo] = useState('')
  const [baris, setBaris] = useState<Baris[]>([barisKosong()])
  const [sedang, setSedang] = useState(false)
  const [hasil, setHasil] = useState<PurchaseResult | null>(null)

  const namaProduk = (id: string) => produk.data.find((p) => p.id === id)?.name ?? '—'
  const namaOwner = (id: string | null) =>
    id === null ? 'Perusahaan' : (owners.data.find((o) => o.id === id)?.name ?? '—')

  /** Reads a rupiah field, treating blank as zero. Never rounds (INV-1). */
  const baca = (teks: string): IDR => (teks.trim() === '' ? ZERO : parseIDR(teks))

  // Totals recomputed on every keystroke so the invoice can be checked against
  // the paper before it is committed. A purchase is immutable once written
  // (INV-2) — the correction is a return, so it is worth getting right here.
  let subtotal = ZERO
  let ppnTotal = ZERO
  let salah = false
  for (const b of baris) {
    try {
      subtotal = add(subtotal, mulQty(baca(b.harga), Number(b.jumlah) || 0))
      ppnTotal = add(ppnTotal, baca(b.ppn))
    } catch {
      salah = true
    }
  }
  const total = salah ? ZERO : add(subtotal, ppnTotal)

  // The number the toggle actually moves. Shown before committing, because
  // "this purchase costs Rp 11.000 more than the invoice says" is the thing
  // the business cannot currently see.
  const dasarBiaya = isPKP && fakturDiterima ? subtotal : total
  const ppnJadiBiaya = isPKP && fakturDiterima ? ZERO : ppnTotal

  function ubahBaris(i: number, patch: Partial<Baris>) {
    setBaris((rows) => rows.map((r, n) => (n === i ? { ...r, ...patch } : r)))
  }

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    setHasil(null)
    try {
      const lines: PurchaseLineBody[] = baris
        .filter((b) => b.productId !== '')
        .map((b) => ({
          product_id: b.productId,
          qty: Number(b.jumlah),
          unit_price_idr: baca(b.harga),
          ppn_idr: baca(b.ppn),
        }))
      if (lines.length === 0) throw new ApiError(400, 'Tambahkan minimal satu baris produk')

      const got = await createPurchase(entityId, {
        supplier_id: supplierId,
        invoice_no: noInvoice,
        purchase_date: tanggal,
        faktur_received: fakturDiterima,
        faktur_no: fakturDiterima ? noFaktur : '',
        is_credit: kredit,
        due_date: kredit ? jatuhTempo : '',
        lines,
      })

      setHasil(got)
      setBaris([barisKosong()])
      setNoInvoice('')
      setNoFaktur('')
      pembelian.muatUlang()
    } catch (err) {
      pembelian.setGalat(
        err instanceof ApiError ? err.message : 'Nilai rupiah harus bilangan bulat, tanpa koma',
      )
    } finally {
      setSedang(false)
    }
  }

  return (
    <>
      <h2>Pembelian</h2>
      <p style={{ color: 'var(--muted)', marginTop: -8 }}>
        Setiap baris pembelian membuat satu lapisan stok. Status faktur menentukan biaya lapisan itu.
      </p>
      <Galat pesan={pembelian.galat} />

      <div className="card" style={{ marginBottom: 20 }}>
        <form onSubmit={simpan}>
          <label>
            <span>Pemasok *</span>
            <select value={supplierId} onChange={(e) => setSupplierId(e.target.value)} required>
              <option value="">Pilih pemasok</option>
              {pemasok.data.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                  {s.issues_faktur ? ' (biasanya memberi faktur)' : ''}
                </option>
              ))}
            </select>
          </label>

          <Teks label="No. invoice" nilai={noInvoice} ubah={setNoInvoice} />
          <Teks
            label="Tanggal invoice"
            nilai={tanggal}
            ubah={setTanggal}
            tipe="date"
            petunjuk="Kosongkan untuk hari ini. Tanggal barang datang, bukan tanggal input."
          />

          {/* The centrepiece. Boxed and warned because one checkbox moves the
              cost basis of everything on this invoice (INV-9). */}
          <div
            style={{
              background: 'var(--warn-bg)',
              border: '2px solid var(--warn-border)',
              borderRadius: 'var(--radius)',
              padding: 16,
              margin: '16px 0',
            }}
          >
            <Centang
              label="Faktur pajak diterima dari pemasok"
              nilai={fakturDiterima}
              ubah={setFakturDiterima}
              petunjuk="Centang hanya jika fakturnya benar-benar ada di tangan. Bukan dijanjikan, bukan menyusul."
            />

            {fakturDiterima && (
              <div style={{ marginTop: 12 }}>
                <Teks label="No. faktur pajak" nilai={noFaktur} ubah={setNoFaktur} />
              </div>
            )}

            <p style={{ margin: '12px 0 0', fontSize: 14 }}>
              {isPKP ? (
                fakturDiterima ? (
                  <>
                    PPN <strong>{formatIDR(ppnTotal)}</strong> dapat dikreditkan, jadi{' '}
                    <strong>tidak masuk biaya</strong>. Biaya persediaan{' '}
                    <strong>{formatIDR(dasarBiaya)}</strong>.
                  </>
                ) : (
                  <>
                    Tanpa faktur, PPN <strong>{formatIDR(ppnJadiBiaya)}</strong> menjadi{' '}
                    <strong>biaya nyata</strong>. Biaya persediaan{' '}
                    <strong>{formatIDR(dasarBiaya)}</strong> — margin barang ini lebih tipis
                    daripada yang terlihat di harga invoice.
                  </>
                )
              ) : (
                <>
                  Perusahaan ini non-PKP dan tidak pernah bisa mengkreditkan PPN masukan. Berapa pun
                  fakturnya, biaya persediaan <strong>{formatIDR(total)}</strong>.
                </>
              )}
            </p>
          </div>

          <h3 style={{ marginBottom: 8 }}>Baris</h3>
          <table>
            <thead>
              <tr>
                <th>Produk</th>
                <th style={{ width: 90 }}>Jumlah</th>
                <th style={{ width: 140 }}>Harga satuan</th>
                <th style={{ width: 140 }}>PPN baris</th>
                <th className="angka" style={{ width: 130 }}>
                  Subtotal
                </th>
                <th style={{ width: 40 }} />
              </tr>
            </thead>
            <tbody>
              {baris.map((b, i) => (
                <tr key={i}>
                  <td>
                    <select
                      value={b.productId}
                      onChange={(e) => ubahBaris(i, { productId: e.target.value })}
                    >
                      <option value="">Pilih produk</option>
                      {produk.data.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name} — {namaOwner(p.owner_id)}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <input
                      type="number"
                      min={1}
                      value={b.jumlah}
                      onChange={(e) => ubahBaris(i, { jumlah: e.target.value })}
                    />
                  </td>
                  <td>
                    <input value={b.harga} onChange={(e) => ubahBaris(i, { harga: e.target.value })} />
                  </td>
                  <td>
                    <input
                      value={b.ppn}
                      onChange={(e) => ubahBaris(i, { ppn: e.target.value })}
                      placeholder="dari invoice"
                    />
                  </td>
                  <td className="angka">
                    {(() => {
                      try {
                        return formatIDR(mulQty(baca(b.harga), Number(b.jumlah) || 0))
                      } catch {
                        return '—'
                      }
                    })()}
                  </td>
                  <td>
                    {baris.length > 1 && (
                      <button
                        type="button"
                        className="sekunder"
                        onClick={() => setBaris((rows) => rows.filter((_, n) => n !== i))}
                      >
                        ×
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <p>
            <button type="button" className="sekunder" onClick={() => setBaris((r) => [...r, barisKosong()])}>
              Tambah baris
            </button>
          </p>

          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 12, marginTop: 12 }}>
            <p style={{ margin: 0 }}>
              Subtotal <strong className="angka">{formatIDR(subtotal)}</strong> · PPN{' '}
              <strong className="angka">{formatIDR(ppnTotal)}</strong> · Total dibayar{' '}
              <strong className="angka">{formatIDR(total)}</strong>
            </p>
            <p style={{ margin: '4px 0 0', color: 'var(--muted)' }}>
              Masuk ke biaya persediaan: <strong className="angka">{formatIDR(dasarBiaya)}</strong>
            </p>
          </div>

          <div style={{ marginTop: 12 }}>
            <Centang label="Pembelian kredit (jadi hutang)" nilai={kredit} ubah={setKredit} />
            {kredit && (
              <Teks label="Jatuh tempo" nilai={jatuhTempo} ubah={setJatuhTempo} tipe="date" />
            )}
          </div>

          <button type="submit" disabled={sedang || salah}>
            {sedang ? 'Menyimpan…' : 'Simpan pembelian'}
          </button>
        </form>
      </div>

      {hasil && (
        <div className="card" style={{ marginBottom: 20 }}>
          <h3>Lapisan stok yang terbentuk</h3>
          <p style={{ color: 'var(--muted)', marginTop: -4 }}>
            Inilah biaya yang akan dipakai saat barang ini terjual, dan yang menentukan margin
            pemiliknya.
          </p>
          <table>
            <thead>
              <tr>
                <th>Produk</th>
                <th className="angka">Dibayar</th>
                <th className="angka">Biaya persediaan</th>
                <th className="angka">PPN dapat dikreditkan</th>
              </tr>
            </thead>
            <tbody>
              {hasil.lines.map((l) => (
                <tr key={l.id}>
                  <td>{namaProduk(l.product_id)}</td>
                  <td className="angka">{formatIDR(l.gross_idr)}</td>
                  <td className="angka">
                    <strong>{formatIDR(l.cost_total_idr)}</strong>
                  </td>
                  <td className="angka">{formatIDR(l.creditable_ppn_idr)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {hasil.payable && (
            <p style={{ marginBottom: 0 }}>
              Hutang tercatat <strong>{formatIDR(hasil.payable.amount_idr)}</strong>
              {hasil.payable.due_date ? `, jatuh tempo ${hasil.payable.due_date}` : ''}.
            </p>
          )}
        </div>
      )}

      <div className="card">
        <h3>Riwayat pembelian</h3>
        {pembelian.data.length === 0 ? (
          <p className="kosong">Belum ada pembelian.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Tanggal</th>
                <th>Invoice</th>
                <th>Faktur</th>
                <th className="angka">PPN</th>
                <th className="angka">Total</th>
                <th>Bayar</th>
              </tr>
            </thead>
            <tbody>
              {pembelian.data.map((p) => (
                <tr key={p.id}>
                  <td>{p.business_date}</td>
                  <td>{p.invoice_no ?? '—'}</td>
                  <td>
                    {p.faktur_received ? (
                      <span>Ada</span>
                    ) : (
                      <span style={{ color: 'var(--danger)' }}>Tidak ada</span>
                    )}
                  </td>
                  <td className="angka">{formatIDR(p.ppn_idr)}</td>
                  <td className="angka">{formatIDR(p.total_idr)}</td>
                  <td>{p.is_credit ? `Kredit${p.due_date ? ` s/d ${p.due_date}` : ''}` : 'Tunai'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="disclaimer">
        Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
      </div>
    </>
  )
}

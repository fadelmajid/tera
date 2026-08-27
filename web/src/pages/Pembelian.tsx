import { useState, type FormEvent } from 'react'
import { listProducts, listSuppliers, listOwners } from '../api/masterdata'
import type { Product, Supplier, Owner } from '../api/masterdata'
import { createPurchase, listPurchases } from '../api/pembelian'
import type { Purchase, PurchaseLineBody, PurchaseResult } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO, add, mulQty } from '../money'
import type { IDR } from '../money'
import { useDaftar, Galat, Stat, Tabel, Teks, Centang } from '../components/dasar'

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
      <p className="sub-judul">
        Setiap baris pembelian membuat satu lapisan stok. Status faktur menentukan biaya lapisan itu.
      </p>
      <Galat pesan={pembelian.galat} />

      <div className="tumpuk">
        <div className="card">
          <form onSubmit={simpan}>
            <div className="baris">
              <label>
                <span>Pemasok *</span>
                <select
                  value={supplierId}
                  onChange={(e) => setSupplierId(e.target.value)}
                  required
                >
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
            </div>

            <div className="sorotan">
              <Centang
                label="Faktur pajak diterima dari pemasok"
                nilai={fakturDiterima}
                ubah={setFakturDiterima}
                petunjuk="Centang hanya jika fakturnya benar-benar ada di tangan. Bukan dijanjikan, bukan menyusul."
              />

              {fakturDiterima && (
                <Teks label="No. faktur pajak" nilai={noFaktur} ubah={setNoFaktur} />
              )}

              <p className="akibat">
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
                    Perusahaan ini non-PKP dan tidak pernah bisa mengkreditkan PPN masukan. Berapa
                    pun fakturnya, biaya persediaan <strong>{formatIDR(total)}</strong>.
                  </>
                )}
              </p>
            </div>

            <h3>Baris</h3>
            <Tabel label="Baris pembelian">
              <thead>
                <tr>
                  <th>Produk</th>
                  <th style={{ width: 90 }}>Jumlah</th>
                  <th style={{ width: 150 }}>Harga satuan</th>
                  <th style={{ width: 150 }}>PPN baris</th>
                  <th className="angka" style={{ width: 140 }}>
                    Subtotal
                  </th>
                  <th style={{ width: 44 }} />
                </tr>
              </thead>
              <tbody>
                {baris.map((b, i) => (
                  <tr key={i}>
                    <td>
                      <select
                        value={b.productId}
                        aria-label={`Produk baris ${i + 1}`}
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
                        aria-label={`Jumlah baris ${i + 1}`}
                        onChange={(e) => ubahBaris(i, { jumlah: e.target.value })}
                      />
                    </td>
                    <td>
                      <input
                        value={b.harga}
                        inputMode="numeric"
                        aria-label={`Harga satuan baris ${i + 1}`}
                        onChange={(e) => ubahBaris(i, { harga: e.target.value })}
                      />
                    </td>
                    <td>
                      <input
                        value={b.ppn}
                        inputMode="numeric"
                        aria-label={`PPN baris ${i + 1}`}
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
                          className="sekunder ikon-saja"
                          aria-label={`Hapus baris ${i + 1}`}
                          onClick={() => setBaris((rows) => rows.filter((_, n) => n !== i))}
                        >
                          ×
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Tabel>

            <div className="aksi">
              <button
                type="button"
                className="sekunder"
                onClick={() => setBaris((r) => [...r, barisKosong()])}
              >
                Tambah baris
              </button>
            </div>

            <div className="statistik">
              <Stat label="Subtotal" nilai={formatIDR(subtotal)} />
              <Stat label="PPN" nilai={formatIDR(ppnTotal)} />
              <Stat label="Total dibayar" nilai={formatIDR(total)} />
              <Stat
                label="Masuk ke biaya persediaan"
                nilai={formatIDR(dasarBiaya)}
                kaki={
                  isPKP && fakturDiterima
                    ? 'PPN dikreditkan, tidak jadi biaya'
                    : 'PPN ikut menjadi biaya'
                }
                besar
              />
            </div>

            <Centang label="Pembelian kredit (jadi hutang)" nilai={kredit} ubah={setKredit} />
            {kredit && (
              <Teks
                label="Jatuh tempo"
                nilai={jatuhTempo}
                ubah={setJatuhTempo}
                tipe="date"
                petunjuk="Tanpa ini, umur hutangnya tidak bisa dihitung di laporan."
              />
            )}

            <button type="submit" disabled={sedang || salah || supplierId === ''}>
              {sedang ? 'Menyimpan…' : 'Simpan pembelian'}
            </button>
          </form>
        </div>

        {hasil && (
          <div className="card">
            <h3>Lapisan stok yang terbentuk</h3>
            <p className="catatan">
              Inilah biaya yang akan dipakai saat barang ini terjual, dan yang menentukan margin
              pemiliknya.
            </p>
            <Tabel label="Lapisan stok yang terbentuk">
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
            </Tabel>
            {hasil.payable && (
              <p className="catatan">
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
            <Tabel label="Riwayat pembelian">
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
                        <span className="lencana lencana-aman">Ada</span>
                      ) : (
                        <span className="lencana lencana-bahaya">Tidak ada</span>
                      )}
                    </td>
                    <td className="angka">{formatIDR(p.ppn_idr)}</td>
                    <td className="angka">{formatIDR(p.total_idr)}</td>
                    <td>
                      {p.is_credit ? `Kredit${p.due_date ? ` s/d ${p.due_date}` : ''}` : 'Tunai'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>

        <div className="disclaimer">
          Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
        </div>
      </div>
    </>
  )
}

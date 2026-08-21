import { useState, type FormEvent } from 'react'
import { listProducts, listOwners, listSuppliers, listCustomers } from '../api/masterdata'
import type { Product, Owner, Supplier, Customer } from '../api/masterdata'
import {
  carryInStock,
  carryInPayable,
  carryInReceivable,
  listPayables,
  listReceivables,
  payPayable,
  payReceivable,
} from '../api/pembelian'
import type { Debt } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR } from '../money'
import { useDaftar, Galat, Teks, Centang } from '../components/dasar'

/**
 * Saldo awal — the go-live carry-in (TASKS 1.11, R11.5).
 *
 * The business is switching systems mid-life, so day one is not day zero:
 * there is stock on the shelves and money owed in both directions, none of
 * which has a document in this system. It gets typed in once and tagged as an
 * opening balance — never dressed up as a purchase or a sale that never
 * happened, which would pollute the purchases report, the PPN position, and
 * the omzet clock with figures belonging to a previous set of books.
 */
export function SaldoAwalPage({ entityId }: { entityId: string }) {
  const [tab, setTab] = useState<'stok' | 'hutang' | 'piutang'>('stok')

  return (
    <>
      <h2>Saldo awal</h2>
      <p style={{ color: 'var(--muted)', marginTop: -8 }}>
        Posisi bisnis saat pindah ke sistem ini. Dicatat sekali, ditandai sebagai saldo awal — bukan
        sebagai pembelian atau penjualan yang tidak pernah terjadi.
      </p>

      <nav style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        {(
          [
            ['stok', 'Stok'],
            ['hutang', 'Hutang'],
            ['piutang', 'Piutang'],
          ] as const
        ).map(([nilai, label]) => (
          <button
            key={nilai}
            className={tab === nilai ? undefined : 'sekunder'}
            onClick={() => setTab(nilai)}
          >
            {label}
          </button>
        ))}
      </nav>

      {tab === 'stok' && <StokAwal entityId={entityId} />}
      {tab === 'hutang' && <Hutang entityId={entityId} />}
      {tab === 'piutang' && <Piutang entityId={entityId} />}
    </>
  )
}

function StokAwal({ entityId }: { entityId: string }) {
  const produk = useDaftar<Product>(() => listProducts(entityId), [entityId])
  const owners = useDaftar<Owner>(() => listOwners(entityId), [entityId])

  const [productId, setProductId] = useState('')
  const [ownerId, setOwnerId] = useState('')
  const [jumlah, setJumlah] = useState('')
  const [nilai, setNilai] = useState('')
  const [tanggal, setTanggal] = useState('')
  const [adaFaktur, setAdaFaktur] = useState(false)
  const [pesan, setPesan] = useState<string | null>(null)
  const [sedang, setSedang] = useState(false)

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      const got = await carryInStock(entityId, {
        as_of_date: tanggal,
        lines: [
          {
            product_id: productId,
            owner_id: ownerId,
            qty: Number(jumlah),
            cost_total_idr: parseIDR(nilai),
            faktur_received: adaFaktur,
          },
        ],
      })
      setPesan(`Tercatat ${got.total_qty} unit senilai ${formatIDR(got.total_cost_idr)}.`)
      setJumlah('')
      setNilai('')
      produk.setGalat(null)
    } catch (err) {
      produk.setGalat(err instanceof ApiError ? err.message : 'Nilai rupiah harus bilangan bulat')
    } finally {
      setSedang(false)
    }
  }

  return (
    <div className="card">
      <Galat pesan={produk.galat} />
      {pesan && <p style={{ color: 'var(--accent)' }}>{pesan}</p>}

      <form onSubmit={simpan}>
        <label>
          <span>Produk *</span>
          <select value={productId} onChange={(e) => setProductId(e.target.value)} required>
            <option value="">Pilih produk</option>
            {produk.data.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>Pemilik *</span>
          <select value={ownerId} onChange={(e) => setOwnerId(e.target.value)}>
            {/* Attribution at go-live is required thinking: get it wrong here
                and every margin figure afterwards starts from the wrong place
                (INV-8). */}
            <option value="">Perusahaan (tanpa owner)</option>
            {owners.data.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>Jumlah *</span>
          <input type="number" min={1} value={jumlah} onChange={(e) => setJumlah(e.target.value)} required />
        </label>

        <Teks
          label="Nilai persediaan (total, bukan per unit)"
          nilai={nilai}
          ubah={setNilai}
          wajib
          petunjuk="Mis. 7 unit senilai Rp 100.000 — isi 100.000. Harga per unit dihitung sistem."
        />

        <Teks
          label="Tanggal mulai pakai sistem"
          nilai={tanggal}
          ubah={setTanggal}
          tipe="date"
          petunjuk="Stok ini akan terjual lebih dulu daripada pembelian setelah tanggal ini."
        />

        <Centang
          label="PPN masukan atas stok ini masih bisa dikreditkan"
          nilai={adaFaktur}
          ubah={setAdaFaktur}
          petunjuk="Hampir selalu tidak. PPN atas barang yang dibeli di pembukuan lama umumnya sudah dikreditkan di sana atau hangus — mengklaimnya lagi di sini berarti mengklaim rupiah yang sama dua kali."
        />

        <button type="submit" disabled={sedang}>
          {sedang ? 'Menyimpan…' : 'Catat stok awal'}
        </button>
      </form>
    </div>
  )
}

function Hutang({ entityId }: { entityId: string }) {
  const pemasok = useDaftar<Supplier>(() => listSuppliers(entityId), [entityId])
  return (
    <DaftarHutang
      entityId={entityId}
      judul="Hutang"
      labelPihak="Pemasok"
      pihak={pemasok.data.map((s) => ({ id: s.id, name: s.name }))}
      muat={() => listPayables(entityId)}
      catat={(body) => carryInPayable(entityId, body)}
      bayar={(id, body) => payPayable(entityId, id, body)}
    />
  )
}

function Piutang({ entityId }: { entityId: string }) {
  const pelanggan = useDaftar<Customer>(() => listCustomers(entityId), [entityId])
  return (
    <DaftarHutang
      entityId={entityId}
      judul="Piutang"
      labelPihak="Pelanggan"
      pihak={pelanggan.data.map((c) => ({ id: c.id, name: c.name }))}
      muat={() => listReceivables(entityId)}
      catat={(body) => carryInReceivable(entityId, body)}
      bayar={(id, body) => payReceivable(entityId, id, body)}
    />
  )
}

/** Hutang and piutang differ only in which party they name. */
function DaftarHutang({
  entityId,
  judul,
  labelPihak,
  pihak,
  muat,
  catat,
  bayar,
}: {
  entityId: string
  judul: string
  labelPihak: string
  pihak: { id: string; name: string }[]
  muat: () => Promise<Debt[]>
  catat: (body: {
    party_id: string
    invoice_no?: string
    amount_idr: ReturnType<typeof parseIDR>
    incurred_on?: string
    due_date?: string
  }) => Promise<{ id: string }>
  bayar: (
    id: string,
    body: { amount_idr: ReturnType<typeof parseIDR>; paid_on?: string },
  ) => Promise<{ id: string }>
}) {
  const daftar = useDaftar<Debt>(muat, [entityId])

  const [partyId, setPartyId] = useState('')
  const [invoice, setInvoice] = useState('')
  const [jumlah, setJumlah] = useState('')
  const [tanggal, setTanggal] = useState('')
  const [tempo, setTempo] = useState('')
  const [sedang, setSedang] = useState(false)

  const namaPihak = (id: string) => pihak.find((p) => p.id === id)?.name ?? '—'

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      await catat({
        party_id: partyId,
        invoice_no: invoice,
        amount_idr: parseIDR(jumlah),
        incurred_on: tanggal,
        due_date: tempo,
      })
      setJumlah('')
      setInvoice('')
      daftar.muatUlang()
    } catch (err) {
      daftar.setGalat(err instanceof ApiError ? err.message : 'Nilai rupiah harus bilangan bulat')
    } finally {
      setSedang(false)
    }
  }

  async function lunasiSebagian(id: string) {
    const jawab = window.prompt('Jumlah pembayaran (rupiah bulat)')
    if (jawab === null) return
    try {
      await bayar(id, { amount_idr: parseIDR(jawab) })
      daftar.muatUlang()
    } catch (err) {
      daftar.setGalat(err instanceof ApiError ? err.message : 'Nilai rupiah harus bilangan bulat')
    }
  }

  return (
    <>
      <div className="card" style={{ marginBottom: 20 }}>
        <Galat pesan={daftar.galat} />
        <form onSubmit={simpan}>
          <label>
            <span>{labelPihak} *</span>
            <select value={partyId} onChange={(e) => setPartyId(e.target.value)} required>
              <option value="">Pilih {labelPihak.toLowerCase()}</option>
              {pihak.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <Teks label="No. invoice" nilai={invoice} ubah={setInvoice} />
          <Teks label="Jumlah" nilai={jumlah} ubah={setJumlah} wajib />
          <Teks label="Tanggal" nilai={tanggal} ubah={setTanggal} tipe="date" />
          <Teks label="Jatuh tempo" nilai={tempo} ubah={setTempo} tipe="date" />
          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : `Catat ${judul.toLowerCase()} awal`}
          </button>
        </form>
      </div>

      <div className="card">
        <h3>{judul} berjalan</h3>
        {daftar.data.length === 0 ? (
          <p className="kosong">Tidak ada {judul.toLowerCase()} yang belum lunas.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>{labelPihak}</th>
                <th>Invoice</th>
                <th>Asal</th>
                <th>Jatuh tempo</th>
                <th className="angka">Jumlah</th>
                <th className="angka">Dibayar</th>
                <th className="angka">Sisa</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {daftar.data.map((d) => (
                <tr key={d.id}>
                  <td>{namaPihak(d.party_id)}</td>
                  <td>{d.invoice_no ?? '—'}</td>
                  <td>{d.source === 'OPENING' ? 'Saldo awal' : 'Transaksi'}</td>
                  <td>{d.due_date ?? '—'}</td>
                  <td className="angka">{formatIDR(d.amount_idr)}</td>
                  <td className="angka">{formatIDR(d.paid_idr)}</td>
                  <td className="angka">
                    <strong>{formatIDR(d.outstanding_idr)}</strong>
                  </td>
                  <td>
                    <button className="sekunder" onClick={() => void lunasiSebagian(d.id)}>
                      Bayar
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}

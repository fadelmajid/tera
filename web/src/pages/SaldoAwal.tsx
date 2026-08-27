import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { listProducts, listOwners, listSuppliers, listCustomers } from '../api/masterdata'
import type { Product, Owner, Supplier, Customer } from '../api/masterdata'
import {
  carryInStock,
  carryInPayable,
  carryInReceivable,
  listPayables,
  listReceivables,
} from '../api/pembelian'
import type { Debt } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR } from '../money'
import { useDaftar, Berhasil, Galat, Tabel, Teks, Centang } from '../components/dasar'

/**
 * Saldo awal — the go-live carry-in (TASKS 1.11, R11.5).
 *
 * The business is switching systems mid-life, so day one is not day zero:
 * there is stock on the shelves and money owed in both directions, none of
 * which has a document in this system. It gets typed in once and tagged as an
 * opening balance — never dressed up as a purchase or a sale that never
 * happened, which would pollute the purchases report, the PPN position, and
 * the omzet clock with figures belonging to a previous set of books.
 *
 * # This screen records; it does not settle
 *
 * It used to also let you pay a debt off, through a bare `window.prompt` that
 * asked for an amount and nothing else — no date, no method, no note, no
 * validation, and no sight of what was owed while typing. The hutang and
 * piutang reports already do that properly, against the ageing that makes the
 * payment meaningful. One action, one place; this screen links there.
 */
export function SaldoAwalPage({ entityId }: { entityId: string }) {
  const [tab, setTab] = useState<'stok' | 'hutang' | 'piutang'>('stok')

  const tabs = [
    ['stok', 'Stok'],
    ['hutang', 'Hutang'],
    ['piutang', 'Piutang'],
  ] as const

  return (
    <>
      <h2>Saldo awal</h2>
      <p className="sub-judul">
        Posisi bisnis saat pindah ke sistem ini. Dicatat sekali, ditandai sebagai saldo awal — bukan
        sebagai pembelian atau penjualan yang tidak pernah terjadi.
      </p>

      <div className="tab-baris" role="tablist" aria-label="Jenis saldo awal">
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
      <Berhasil pesan={pesan} />

      <form onSubmit={simpan}>
        <div className="baris">
          <label>
            <span>Produk *</span>
            <select value={productId} onChange={(e) => setProductId(e.target.value)} required>
              <option value="">Pilih produk</option>
              {produk.data.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.code} · {p.name}
                </option>
              ))}
            </select>
          </label>

          {/* Not marked required, because it genuinely is not: the empty value
              is the company bucket, which is a real attribution and its own
              line on the margin report (R2.2). It used to carry an asterisk it
              did not enforce, which taught people the asterisk means nothing. */}
          <label>
            <span>Pemilik</span>
            <select value={ownerId} onChange={(e) => setOwnerId(e.target.value)}>
              <option value="">Perusahaan (tanpa owner)</option>
              {owners.data.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
            <small className="petunjuk">
              Menentukan margin siapa yang naik saat stok ini terjual. Stok tanpa owner masuk ke
              bucket perusahaan.
            </small>
          </label>
        </div>

        <div className="baris">
          <label>
            <span>Jumlah *</span>
            <input
              type="number"
              min={1}
              value={jumlah}
              onChange={(e) => setJumlah(e.target.value)}
              required
            />
          </label>

          <Teks
            label="Nilai persediaan (total, bukan per unit)"
            nilai={nilai}
            ubah={setNilai}
            wajib
            inputMode="numeric"
            petunjuk="Mis. 7 unit senilai Rp 100.000 — isi 100.000. Harga per unit dihitung sistem."
          />
        </div>

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

        <button type="submit" disabled={sedang || productId === ''}>
          {sedang ? 'Menyimpan…' : 'Catat stok awal'}
        </button>
      </form>
    </div>
  )
}

function Hutang({ entityId }: { entityId: string }) {
  const pemasok = useDaftar<Supplier>(() => listSuppliers(entityId), [entityId])
  return (
    <DaftarSaldo
      entityId={entityId}
      judul="Hutang"
      labelPihak="Pemasok"
      keLaporan="/hutang"
      pihak={pemasok.data.map((s) => ({ id: s.id, name: s.name }))}
      muat={() => listPayables(entityId)}
      catat={(body) => carryInPayable(entityId, body)}
    />
  )
}

function Piutang({ entityId }: { entityId: string }) {
  const pelanggan = useDaftar<Customer>(() => listCustomers(entityId), [entityId])
  return (
    <DaftarSaldo
      entityId={entityId}
      judul="Piutang"
      labelPihak="Pelanggan"
      keLaporan="/piutang"
      pihak={pelanggan.data.map((c) => ({ id: c.id, name: c.name }))}
      muat={() => listReceivables(entityId)}
      catat={(body) => carryInReceivable(entityId, body)}
    />
  )
}

/** Hutang and piutang differ only in which party they name. */
function DaftarSaldo({
  entityId,
  judul,
  labelPihak,
  keLaporan,
  pihak,
  muat,
  catat,
}: {
  entityId: string
  judul: string
  labelPihak: string
  keLaporan: string
  pihak: { id: string; name: string }[]
  muat: () => Promise<Debt[]>
  catat: (body: {
    party_id: string
    invoice_no?: string
    amount_idr: ReturnType<typeof parseIDR>
    incurred_on?: string
    due_date?: string
  }) => Promise<{ id: string }>
}) {
  const daftar = useDaftar<Debt>(muat, [entityId])

  const [partyId, setPartyId] = useState('')
  const [invoice, setInvoice] = useState('')
  const [jumlah, setJumlah] = useState('')
  const [tanggal, setTanggal] = useState('')
  const [tempo, setTempo] = useState('')
  const [sedang, setSedang] = useState(false)

  const namaPihak = (id: string) => pihak.find((p) => p.id === id)?.name ?? '—'
  const tanpaTempo = daftar.data.filter((d) => !d.due_date).length

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

  return (
    <div className="tumpuk">
      <div className="card">
        <h3>Catat {judul.toLowerCase()} awal</h3>
        <Galat pesan={daftar.galat} />
        <form onSubmit={simpan}>
          <div className="baris">
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
          </div>
          <div className="baris">
            <Teks label="Jumlah" nilai={jumlah} ubah={setJumlah} wajib inputMode="numeric" />
            <Teks label="Tanggal" nilai={tanggal} ubah={setTanggal} tipe="date" />
            <Teks
              label="Jatuh tempo"
              nilai={tempo}
              ubah={setTempo}
              tipe="date"
              petunjuk="Tanpa ini, umurnya tidak bisa dihitung di laporan."
            />
          </div>
          <button type="submit" disabled={sedang || partyId === ''}>
            {sedang ? 'Menyimpan…' : `Catat ${judul.toLowerCase()} awal`}
          </button>
        </form>
      </div>

      <div className="card">
        <div className="card-kepala">
          <h3>{judul} berjalan</h3>
          <Link to={keLaporan}>Laporan umur &amp; pembayaran</Link>
        </div>

        {tanpaTempo > 0 && (
          <div className="disclaimer">
            <strong>{tanpaTempo} dokumen tanpa tanggal jatuh tempo.</strong> Umurnya tidak bisa
            dihitung, jadi tidak akan muncul sebagai menunggak di laporan meskipun mungkin sudah
            lama lewat.
          </div>
        )}

        {daftar.data.length === 0 ? (
          <p className="kosong">Tidak ada {judul.toLowerCase()} yang belum lunas.</p>
        ) : (
          <Tabel label={`${judul} yang belum lunas`}>
            <thead>
              <tr>
                <th>{labelPihak}</th>
                <th>Invoice</th>
                <th>Asal</th>
                <th>Jatuh tempo</th>
                <th className="angka">Jumlah</th>
                <th className="angka">Dibayar</th>
                <th className="angka">Sisa</th>
              </tr>
            </thead>
            <tbody>
              {daftar.data.map((d) => (
                <tr key={d.id}>
                  <td>{namaPihak(d.party_id)}</td>
                  <td>{d.invoice_no ?? '—'}</td>
                  <td>
                    {d.source === 'OPENING' ? (
                      <span className="lencana">Saldo awal</span>
                    ) : (
                      'Transaksi'
                    )}
                  </td>
                  <td>{d.due_date ?? <span className="teks-redup">belum diisi</span>}</td>
                  <td className="angka">{formatIDR(d.amount_idr)}</td>
                  <td className="angka">{formatIDR(d.paid_idr)}</td>
                  <td className="angka">
                    <strong>{formatIDR(d.outstanding_idr)}</strong>
                  </td>
                </tr>
              ))}
            </tbody>
          </Tabel>
        )}

        <p className="catatan">
          Pembayaran dicatat di laporan {judul.toLowerCase()}, bersama umur tagihan yang membuat
          pembayaran itu punya arti.
        </p>
      </div>
    </div>
  )
}

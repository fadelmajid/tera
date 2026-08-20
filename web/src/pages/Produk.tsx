import { useState, type FormEvent } from 'react'
import { listProducts, createProduct, listOwners } from '../api/masterdata'
import type { Product, Owner } from '../api/masterdata'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO } from '../money'
import { useDaftar, Halaman, Teks } from '../components/dasar'

/** The catalogue. Owner attribution here decides whose margin a sale lands in (R2.3). */
export function ProdukPage({ entityId }: { entityId: string }) {
  const produk = useDaftar<Product>(() => listProducts(entityId), [entityId])
  const owners = useDaftar<Owner>(() => listOwners(entityId), [entityId])

  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [satuan, setSatuan] = useState('pcs')
  const [barcode, setBarcode] = useState('')
  const [kategori, setKategori] = useState('')
  const [ownerId, setOwnerId] = useState('')
  const [harga, setHarga] = useState('')
  const [sedang, setSedang] = useState(false)

  const namaOwner = (id: string | null) =>
    id === null ? 'Perusahaan' : (owners.data.find((o) => o.id === id)?.name ?? '—')

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      // parseIDR refuses anything fractional rather than rounding it (INV-1).
      const hargaIdr = harga.trim() === '' ? ZERO : parseIDR(harga)
      await createProduct(entityId, {
        code: kode,
        name: nama,
        unit: satuan,
        barcode,
        category: kategori,
        owner_id: ownerId,
        sale_price_idr: hargaIdr,
      })
      setKode('')
      setNama('')
      setBarcode('')
      setKategori('')
      setHarga('')
      produk.muatUlang()
    } catch (err) {
      produk.setGalat(
        err instanceof ApiError ? err.message : 'Harga harus bilangan bulat rupiah, tanpa koma',
      )
    } finally {
      setSedang(false)
    }
  }

  return (
    <Halaman
      judul="Produk"
      keterangan="Katalog dipakai bersama kedua perusahaan. Stok-lah yang dipisah per perusahaan."
      galat={produk.galat}
      form={
        <form onSubmit={simpan}>
          <Teks label="Kode" nilai={kode} ubah={setKode} wajib />
          <Teks label="Nama" nilai={nama} ubah={setNama} wajib />
          <Teks label="Satuan" nilai={satuan} ubah={setSatuan} wajib />
          <Teks label="Barcode" nilai={barcode} ubah={setBarcode} petunjuk="Opsional, harus unik" />
          <Teks label="Kategori" nilai={kategori} ubah={setKategori} />

          <label>
            <span>Owner</span>
            <select value={ownerId} onChange={(e) => setOwnerId(e.target.value)}>
              {/* Tanpa owner is the company bucket (R2.2) — a deliberate choice,
                  reported as its own line, not a missing value. */}
              <option value="">Perusahaan (tanpa owner)</option>
              {owners.data.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
          </label>

          <Teks
            label="Harga jual"
            nilai={harga}
            ubah={setHarga}
            petunjuk="Rupiah bulat, mis. 27.500"
          />

          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : 'Tambah produk'}
          </button>
        </form>
      }
    >
      {produk.data.length === 0 ? (
        <p className="kosong">Belum ada produk.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Kode</th>
              <th>Nama</th>
              <th>Satuan</th>
              <th>Owner</th>
              <th className="angka">Harga jual</th>
            </tr>
          </thead>
          <tbody>
            {produk.data.map((p) => (
              <tr key={p.id}>
                <td>{p.code}</td>
                <td>{p.name}</td>
                <td>{p.unit}</td>
                <td>{namaOwner(p.owner_id)}</td>
                <td className="angka">{formatIDR(p.sale_price_idr)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Halaman>
  )
}

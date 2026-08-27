import { useState, type FormEvent } from 'react'
import { listProducts, createProduct, listOwners } from '../api/masterdata'
import type { Product, Owner } from '../api/masterdata'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO } from '../money'
import { useDaftar, Halaman, Teks, Cari, Tabel, Th, useCari, useUrut } from '../components/dasar'

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

  // A catalogue runs to a thousand SKUs at this business's scale. Finding one
  // by scrolling is not a workflow.
  const { q, setQ, hasil } = useCari(produk.data, (p) =>
    `${p.code} ${p.name} ${p.barcode ?? ''} ${p.category ?? ''} ${namaOwner(p.owner_id)}`,
  )
  const { hasil: baris, urutan, urutkan } = useUrut(hasil, {
    kode: (p) => p.code,
    nama: (p) => p.name,
    owner: (p) => namaOwner(p.owner_id),
    harga: (p) => p.sale_price_idr,
  })

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
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
      labelTambah="Tambah produk"
      alat={
        <div className="alat">
          <Cari nilai={q} ubah={setQ} petunjuk="Kode, nama, barcode, kategori, owner" />
          <span className="hitung">
            {baris.length} dari {produk.data.length} produk
          </span>
        </div>
      }
      form={
        <form onSubmit={simpan}>
          <div className="baris">
            <Teks label="Kode" nilai={kode} ubah={setKode} wajib />
            <Teks label="Nama" nilai={nama} ubah={setNama} wajib />
          </div>
          <div className="baris">
            <Teks label="Satuan" nilai={satuan} ubah={setSatuan} wajib />
            <Teks label="Barcode" nilai={barcode} ubah={setBarcode} petunjuk="Opsional, harus unik" />
            <Teks label="Kategori" nilai={kategori} ubah={setKategori} />
          </div>

          <div className="baris">
            <label>
              <span>Owner</span>
              <select value={ownerId} onChange={(e) => setOwnerId(e.target.value)}>
                <option value="">Perusahaan (tanpa owner)</option>
                {owners.data.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.name}
                  </option>
                ))}
              </select>
              {/* R2.3: this field, and only this field, decides whose margin a
                  sale of this product lands in. Said here rather than
                  discovered when a settlement disagrees. */}
              <small className="petunjuk">
                Menentukan margin siapa yang naik saat produk ini terjual. Tanpa owner, marginnya
                masuk ke bucket perusahaan.
              </small>
            </label>

            <Teks
              label="Harga jual"
              nilai={harga}
              ubah={setHarga}
              petunjuk="Rupiah bulat, mis. 27.500"
            />
          </div>

          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : 'Tambah produk'}
          </button>
        </form>
      }
    >
      {produk.data.length === 0 ? (
        <p className="kosong">Belum ada produk.</p>
      ) : baris.length === 0 ? (
        <p className="kosong">Tidak ada produk yang cocok dengan "{q}".</p>
      ) : (
        <Tabel label="Daftar produk">
          <thead>
            <tr>
              <Th kunci="kode" urutan={urutan} urutkan={urutkan}>
                Kode
              </Th>
              <Th kunci="nama" urutan={urutan} urutkan={urutkan}>
                Nama
              </Th>
              <Th>Satuan</Th>
              <Th kunci="owner" urutan={urutan} urutkan={urutkan}>
                Owner
              </Th>
              <Th kunci="harga" urutan={urutan} urutkan={urutkan} angka>
                Harga jual
              </Th>
            </tr>
          </thead>
          <tbody>
            {baris.map((p) => (
              <tr key={p.id}>
                <td>{p.code}</td>
                <td>
                  {p.name}
                  {p.category && (
                    <>
                      <br />
                      <small className="catatan">{p.category}</small>
                    </>
                  )}
                </td>
                <td>{p.unit}</td>
                <td>{namaOwner(p.owner_id)}</td>
                <td className="angka">{formatIDR(p.sale_price_idr)}</td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}
    </Halaman>
  )
}

import { useState, type FormEvent } from 'react'
import { listCustomers, createCustomer } from '../api/masterdata'
import type { Customer } from '../api/masterdata'
import { ApiError } from '../api/client'
import { useDaftar, Halaman, Teks, Tabel, Cari, useCari } from '../components/dasar'

/** Customers. NPWP for businesses, NIK for individuals — needed by a buyer who wants a faktur (R11.3). */
export function PelangganPage({ entityId }: { entityId: string }) {
  const { data, galat, muatUlang, setGalat } = useDaftar<Customer>(
    () => listCustomers(entityId),
    [entityId],
  )
  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [npwp, setNpwp] = useState('')
  const [nik, setNik] = useState('')
  const [telepon, setTelepon] = useState('')
  const [sedang, setSedang] = useState(false)

  const { q, setQ, hasil } = useCari(
    data,
    (c) => `${c.code} ${c.name} ${c.npwp ?? ''} ${c.nik ?? ''} ${c.phone ?? ''}`,
  )

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      await createCustomer(entityId, { code: kode, name: nama, npwp, nik, phone: telepon })
      setKode('')
      setNama('')
      setNpwp('')
      setNik('')
      setTelepon('')
      muatUlang()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan')
    } finally {
      setSedang(false)
    }
  }

  return (
    <Halaman
      judul="Pelanggan"
      keterangan="Diperlukan untuk penjualan kredit, dan untuk pembeli yang meminta faktur pajak."
      galat={galat}
      labelTambah="Tambah pelanggan"
      alat={
        data.length > 0 ? (
          <div className="alat">
            <Cari nilai={q} ubah={setQ} petunjuk="Kode, nama, NPWP, NIK, telepon" />
            <span className="hitung">
              {hasil.length} dari {data.length} pelanggan
            </span>
          </div>
        ) : undefined
      }
      form={
        <form onSubmit={simpan}>
          <div className="baris">
            <Teks label="Kode" nilai={kode} ubah={setKode} wajib />
            <Teks label="Nama" nilai={nama} ubah={setNama} wajib />
          </div>
          <div className="baris">
            <Teks label="NPWP" nilai={npwp} ubah={setNpwp} petunjuk="Untuk pembeli badan usaha" />
            <Teks label="NIK" nilai={nik} ubah={setNik} petunjuk="Untuk pembeli perorangan" />
            <Teks label="Telepon" nilai={telepon} ubah={setTelepon} />
          </div>
          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : 'Tambah pelanggan'}
          </button>
        </form>
      }
    >
      {data.length === 0 ? (
        <p className="kosong">Belum ada pelanggan.</p>
      ) : hasil.length === 0 ? (
        <p className="kosong">Tidak ada pelanggan yang cocok dengan "{q}".</p>
      ) : (
        <Tabel label="Daftar pelanggan">
          <thead>
            <tr>
              <th>Kode</th>
              <th>Nama</th>
              <th>NPWP / NIK</th>
              <th>Telepon</th>
            </tr>
          </thead>
          <tbody>
            {hasil.map((c) => (
              <tr key={c.id}>
                <td>{c.code}</td>
                <td>{c.name}</td>
                <td>{c.npwp ?? c.nik ?? '—'}</td>
                <td>{c.phone ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}
    </Halaman>
  )
}

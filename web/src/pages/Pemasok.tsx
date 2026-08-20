import { useState, type FormEvent } from 'react'
import { listSuppliers, createSupplier } from '../api/masterdata'
import type { Supplier } from '../api/masterdata'
import { ApiError } from '../api/client'
import { useDaftar, Halaman, Teks, Centang } from '../components/dasar'

/** Suppliers. Shared across both companies; which one bought is recorded on the purchase (R10.5). */
export function PemasokPage({ entityId }: { entityId: string }) {
  const { data, galat, muatUlang, setGalat } = useDaftar<Supplier>(
    () => listSuppliers(entityId),
    [entityId],
  )
  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [npwp, setNpwp] = useState('')
  const [telepon, setTelepon] = useState('')
  const [faktur, setFaktur] = useState(false)
  const [sedang, setSedang] = useState(false)

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      await createSupplier(entityId, {
        code: kode,
        name: nama,
        npwp,
        phone: telepon,
        issues_faktur: faktur,
      })
      setKode('')
      setNama('')
      setNpwp('')
      setTelepon('')
      setFaktur(false)
      muatUlang()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan')
    } finally {
      setSedang(false)
    }
  }

  return (
    <Halaman
      judul="Pemasok"
      galat={galat}
      form={
        <form onSubmit={simpan}>
          <Teks label="Kode" nilai={kode} ubah={setKode} wajib />
          <Teks label="Nama" nilai={nama} ubah={setNama} wajib />
          <Teks label="NPWP" nilai={npwp} ubah={setNpwp} />
          <Teks label="Telepon" nilai={telepon} ubah={setTelepon} />
          <Centang
            label="Biasanya memberi faktur pajak"
            nilai={faktur}
            ubah={setFaktur}
            petunjuk="Hanya nilai bawaan untuk layar pembelian. Faktur yang sebenarnya dicatat per pembelian, dan itulah yang menentukan harga pokok."
          />
          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : 'Tambah pemasok'}
          </button>
        </form>
      }
    >
      {data.length === 0 ? (
        <p className="kosong">Belum ada pemasok.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Kode</th>
              <th>Nama</th>
              <th>NPWP</th>
              <th>Faktur</th>
            </tr>
          </thead>
          <tbody>
            {data.map((s) => (
              <tr key={s.id}>
                <td>{s.code}</td>
                <td>{s.name}</td>
                <td>{s.npwp ?? '—'}</td>
                <td>{s.issues_faktur ? 'Biasanya ya' : 'Biasanya tidak'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Halaman>
  )
}

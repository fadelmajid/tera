import { useState, type FormEvent } from 'react'
import { listOwners, createOwner } from '../api/masterdata'
import type { Owner } from '../api/masterdata'
import { ApiError } from '../api/client'
import { useDaftar, Halaman, Teks, Tabel } from '../components/dasar'

/**
 * Owners — the family members products are attributed to.
 *
 * This list is what makes the owner-margin report possible. The user currently
 * fakes it with product categories because there is no proper slot for it
 * (REQUIREMENTS §2); this is that slot.
 *
 * First in the master-data menu, and second in the setup checklist, because a
 * product cannot be attributed to an owner who does not exist yet.
 */
export function OwnerPage({ entityId }: { entityId: string }) {
  const { data, galat, muatUlang, setGalat } = useDaftar<Owner>(() => listOwners(entityId), [entityId])
  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [catatan, setCatatan] = useState('')
  const [sedang, setSedang] = useState(false)

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    try {
      await createOwner(entityId, { code: kode, name: nama, note: catatan })
      setKode('')
      setNama('')
      setCatatan('')
      muatUlang()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan')
    } finally {
      setSedang(false)
    }
  }

  return (
    <Halaman
      judul="Owner"
      keterangan="Anggota keluarga yang memiliki lini produk. Margin dihitung dan disetorkan per owner."
      galat={galat}
      labelTambah="Tambah owner"
      form={
        <form onSubmit={simpan}>
          <div className="baris">
            <Teks label="Kode" nilai={kode} ubah={setKode} wajib />
            <Teks label="Nama" nilai={nama} ubah={setNama} wajib />
          </div>
          <Teks label="Catatan" nilai={catatan} ubah={setCatatan} />
          <button type="submit" disabled={sedang}>
            {sedang ? 'Menyimpan…' : 'Tambah owner'}
          </button>
        </form>
      }
    >
      {data.length === 0 ? (
        <p className="kosong">Belum ada owner. Tambahkan anggota keluarga yang memiliki produk.</p>
      ) : (
        <Tabel label="Daftar owner">
          <thead>
            <tr>
              <th>Kode</th>
              <th>Nama</th>
              <th>Catatan</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {data.map((o) => (
              <tr key={o.id}>
                <td>{o.code}</td>
                <td>{o.name}</td>
                <td>{o.note ?? '—'}</td>
                <td>{o.is_active ? 'Aktif' : 'Nonaktif'}</td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}
    </Halaman>
  )
}

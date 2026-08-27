import { useState, type FormEvent } from 'react'
import { setupFirstEntity } from '../api/masterdata'
import type { Entity } from '../api/masterdata'
import { ApiError } from '../api/client'
import { Teks, Centang, Galat } from '../components/dasar'

/**
 * First-run setup: create the first company.
 *
 * Access is per company (R13.4), so on a fresh install the first account holds
 * no role anywhere and nothing else is reachable. This screen resolves that
 * once; afterwards adding a company requires being an owner of one.
 */
export function Setup({ selesai }: { selesai: (entity: Entity) => void }) {
  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [pkp, setPkp] = useState(false)
  const [npwp, setNpwp] = useState('')
  const [bulan, setBulan] = useState('1')
  const [galat, setGalat] = useState<string | null>(null)
  const [sedang, setSedang] = useState(false)

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSedang(true)
    setGalat(null)
    try {
      selesai(
        await setupFirstEntity({
          code: kode,
          name: nama,
          is_pkp: pkp,
          npwp,
          timezone: 'Asia/Jakarta',
          book_year_start_month: Number(bulan),
        }),
      )
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan')
    } finally {
      setSedang(false)
    }
  }

  return (
    <div className="masuk">
      <form className="card siapkan" onSubmit={simpan}>
        <div className="merek">
          <span className="merek-tanda" aria-hidden="true">
            T
          </span>
          <h1>Siapkan perusahaan</h1>
        </div>
        <p className="sub">
          Perusahaan pertama. Anda otomatis menjadi pemiliknya dan dapat menambahkan pengguna
          lain setelah ini.
        </p>

        <Galat pesan={galat} />

        <Teks label="Kode" nilai={kode} ubah={setKode} wajib petunjuk="mis. PKP atau CV" />
        <Teks label="Nama perusahaan" nilai={nama} ubah={setNama} wajib />

        <Centang
          label="Perusahaan ini PKP (wajib memungut PPN)"
          nilai={pkp}
          ubah={setPkp}
          petunjuk="PKP wajib memungut PPN keluaran pada setiap penjualan kena pajak, baik pembeli meminta faktur maupun tidak."
        />

        <Teks label="NPWP" nilai={npwp} ubah={setNpwp} />
        <Teks
          label="Bulan awal tahun buku"
          nilai={bulan}
          ubah={setBulan}
          tipe="number"
          petunjuk="1 = Januari"
        />

        <button type="submit" disabled={sedang}>
          {sedang ? 'Menyimpan…' : 'Simpan perusahaan'}
        </button>

        <p className="disclaimer">
          Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
        </p>
      </form>
    </div>
  )
}

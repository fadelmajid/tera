import { useState, type FormEvent } from 'react'
import { createEntity } from '../api/masterdata'
import type { Entity } from '../api/masterdata'
import { ApiError } from '../api/client'
import { Berhasil, Centang, Halaman, Tabel, Teks } from '../components/dasar'

/**
 * Perusahaan — the companies under this installation.
 *
 * Adding one used to live inside the transfer screen, appearing only when
 * there was nowhere to transfer to and finishing with a full page reload.
 * Creating a company is a top-level structural act — it decides which books a
 * sale lands in, whether PPN is charged, and which omzet clock runs — so it
 * belongs beside the other master data.
 *
 * Owner only. The server refuses anyone else; the menu simply does not offer
 * it.
 *
 * # On the second company
 *
 * Two entities, one PKP and one not, transferring at cost, is what the family
 * asked for and is legitimate. It is not a way to manage the PKP threshold,
 * and nothing on this screen frames it as one. What the screen does say is the
 * consequence nobody sees coming: stock moving from the non-PKP company into
 * the PKP one loses its input PPN credit permanently (R4.5), because a
 * non-PKP company cannot issue the faktur that would carry it.
 */
export function PerusahaanPage({
  entityId,
  entities,
  muatUlang,
}: {
  entityId: string
  entities: Entity[]
  /** Re-reads the company list so the sidebar switcher shows the new one. */
  muatUlang: () => void
}) {
  const [kode, setKode] = useState('')
  const [nama, setNama] = useState('')
  const [npwp, setNpwp] = useState('')
  const [pkp, setPkp] = useState(false)
  const [bulan, setBulan] = useState('1')
  const [galat, setGalat] = useState<string | null>(null)
  const [pesan, setPesan] = useState<string | null>(null)
  const [sibuk, setSibuk] = useState(false)

  async function simpan(e: FormEvent) {
    e.preventDefault()
    setSibuk(true)
    setPesan(null)
    try {
      const dibuat = await createEntity(entityId, {
        code: kode.trim(),
        name: nama.trim(),
        is_pkp: pkp,
        npwp: npwp.trim(),
        timezone: 'Asia/Jakarta',
        book_year_start_month: Number(bulan) || 1,
      })
      setKode('')
      setNama('')
      setNpwp('')
      setPkp(false)
      setGalat(null)
      setPesan(
        `${dibuat.name} dibuat. Pilih perusahaan dari daftar di sidebar untuk mulai memakainya.`,
      )
      // The company list came down with the session, so it has to be re-read
      // before the sidebar switcher can offer the new one.
      muatUlang()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menambah perusahaan')
    } finally {
      setSibuk(false)
    }
  }

  return (
    <Halaman
      judul="Perusahaan"
      keterangan="Badan usaha yang dikelola di sistem ini. Setiap penjualan, pembelian dan stok tercatat di bawah salah satunya."
      galat={galat}
      labelTambah="Tambah perusahaan"
      form={
        <form onSubmit={simpan}>
          <div className="baris">
            <Teks label="Kode" nilai={kode} ubah={setKode} wajib petunjuk="mis. PKP atau CV" />
            <Teks label="Nama perusahaan" nilai={nama} ubah={setNama} wajib />
          </div>
          <div className="baris">
            <Teks label="NPWP" nilai={npwp} ubah={setNpwp} />
            <Teks
              label="Bulan awal tahun buku"
              nilai={bulan}
              ubah={setBulan}
              tipe="number"
              petunjuk="1 = Januari. Batas omzet PKP dihitung per tahun buku."
            />
          </div>

          <Centang
            label="Perusahaan ini PKP (wajib memungut PPN)"
            nilai={pkp}
            ubah={setPkp}
            petunjuk="PKP wajib memungut PPN keluaran pada setiap penjualan kena pajak, baik pembeli meminta faktur maupun tidak, dan dapat mengkreditkan PPN masukan bila fakturnya ada."
          />

          <div className="disclaimer">
            Memindahkan stok dari perusahaan non-PKP ke perusahaan PKP menghapus kredit PPN
            masukan atas stok itu secara permanen — perusahaan non-PKP tidak bisa menerbitkan
            faktur pajak, sehingga rantai kreditnya putus dan tidak bisa disambung kembali (R4.5).
            Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
          </div>

          <button type="submit" disabled={sibuk || kode.trim() === '' || nama.trim() === ''}>
            {sibuk ? 'Menyimpan…' : 'Tambah perusahaan'}
          </button>
        </form>
      }
    >
      <Berhasil pesan={pesan} />
      {entities.length === 0 ? (
        <p className="kosong">Belum ada perusahaan.</p>
      ) : (
        <Tabel label="Daftar perusahaan">
          <thead>
            <tr>
              <th>Kode</th>
              <th>Nama</th>
              <th>Status PPN</th>
              <th>NPWP</th>
              <th>Tahun buku mulai</th>
              <th>Zona waktu</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {entities.map((e) => (
              <tr key={e.id}>
                <td>{e.code}</td>
                <td>
                  {e.name}
                  {e.id === entityId && (
                    <>
                      {' '}
                      <span className="lencana">sedang dipakai</span>
                    </>
                  )}
                </td>
                <td>
                  {e.is_pkp ? (
                    <span className="lencana">PKP</span>
                  ) : (
                    <span className="teks-redup">non-PKP</span>
                  )}
                </td>
                <td>{e.npwp || '—'}</td>
                <td>{namaBulan[e.book_year_start_month] ?? e.book_year_start_month}</td>
                <td>{e.timezone}</td>
                <td>{e.is_active ? 'Aktif' : 'Nonaktif'}</td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}
    </Halaman>
  )
}

const namaBulan: Record<number, string> = {
  1: 'Januari',
  2: 'Februari',
  3: 'Maret',
  4: 'April',
  5: 'Mei',
  6: 'Juni',
  7: 'Juli',
  8: 'Agustus',
  9: 'September',
  10: 'Oktober',
  11: 'November',
  12: 'Desember',
}

import { useCallback, useEffect, useState } from 'react'
import {
  countSheet,
  listOpnames,
  startOpname,
  getOpname,
  saveOpnameLine,
  postOpname,
  KODE_ALASAN,
} from '../api/pembelian'
import type { OnHand, Opname, OpnameLine } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR } from '../money'
import { Cari, Galat, Tabel, useCari } from '../components/dasar'
import { Konfirmasi } from '../components/Modal'

/**
 * Stock opname — physical count, variance, posting (TASKS 1.10, R12.4-5).
 *
 * The count is taken per owner, not per product. Budi's three boxes and Sari's
 * forty of the same item sit on one shelf but are counted and adjusted
 * separately, because a variance has to land in exactly one person's bucket
 * (INV-8) — and posting one person's miscount against another's goods would be
 * a transfer of money between family members labelled as a correction.
 *
 * Counting and posting are separate steps. Counting a shop takes hours; posting
 * is the moment stock actually moves, and it cannot be undone — so it goes
 * through the same kind of confirmation as any other irreversible act here,
 * showing what is about to move before it moves. It used to be guarded by a
 * required text field and nothing else.
 */
export function OpnamePage({ entityId }: { entityId: string }) {
  const [daftar, setDaftar] = useState<Opname[]>([])
  const [aktif, setAktif] = useState<Opname | null>(null)
  const [lines, setLines] = useState<OpnameLine[]>([])
  const [stok, setStok] = useState<OnHand[]>([])
  const [galat, setGalat] = useState<string | null>(null)
  const [konfirmasi, setKonfirmasi] = useState(false)
  const [sedang, setSedang] = useState(false)

  const muat = useCallback(async () => {
    try {
      const [d, s] = await Promise.all([listOpnames(entityId), countSheet(entityId)])
      setDaftar(d)
      setStok(s)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data')
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  const buka = useCallback(
    async (id: string) => {
      try {
        const got = await getOpname(entityId, id)
        setAktif(got.opname)
        setLines(got.lines)
        setGalat(null)
      } catch (err) {
        setGalat(err instanceof ApiError ? err.message : 'Gagal membuka opname')
      }
    },
    [entityId],
  )

  async function mulai() {
    setSedang(true)
    try {
      const got = await startOpname(entityId, {})
      await muat()
      await buka(got.id)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memulai opname')
    } finally {
      setSedang(false)
    }
  }

  async function simpanBaris(row: OnHand, counted: string, kode: string, catatan: string) {
    if (!aktif) return
    const jumlah = Number(counted)
    if (!Number.isInteger(jumlah) || jumlah < 0) {
      setGalat('Jumlah hitung harus bilangan bulat dan tidak negatif')
      return
    }
    try {
      await saveOpnameLine(entityId, aktif.id, {
        product_id: row.product_id,
        owner_id: row.owner_id ?? '',
        counted_qty: jumlah,
        reason_code: kode,
        reason_note: catatan,
      })
      await buka(aktif.id)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan baris')
    }
  }

  async function posting(alasan: string) {
    if (!aktif) return
    setSedang(true)
    try {
      await postOpname(entityId, aktif.id, alasan)
      setKonfirmasi(false)
      await muat()
      await buka(aktif.id)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memposting opname')
    } finally {
      setSedang(false)
    }
  }

  const terkunci = aktif?.status === 'POSTED'
  const selisih = lines.filter((l) => l.variance !== 0)
  const tanpaAlasan = selisih.filter((l) => !l.reason_code)
  const { q, setQ, hasil } = useCari(
    stok,
    (r) => `${r.product_name} ${r.owner_name ?? 'Perusahaan'}`,
  )

  return (
    <>
      <h2>Opname stok</h2>
      <p className="sub-judul">
        Dihitung per pemilik, bukan per produk. Selisih harus masuk ke bucket satu orang.
      </p>
      <Galat pesan={galat} />

      <div className="tumpuk">
        <div className="card">
          <div className="card-kepala">
            <h3>Opname</h3>
            <button onClick={() => void mulai()} disabled={sedang}>
              Mulai opname baru
            </button>
          </div>
          {daftar.length === 0 ? (
            <p className="kosong">Belum pernah ada opname.</p>
          ) : (
            <Tabel label="Riwayat opname">
              <thead>
                <tr>
                  <th>Tanggal</th>
                  <th>Status</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {daftar.map((o) => (
                  <tr key={o.id}>
                    <td>{o.business_date}</td>
                    <td>
                      {o.status === 'POSTED' ? (
                        <span className="lencana lencana-aman">Sudah diposting</span>
                      ) : (
                        <span className="lencana lencana-hati">Draf</span>
                      )}
                    </td>
                    <td>
                      <button
                        className="dalam-baris"
                        onClick={() => void buka(o.id)}
                        aria-label={`Buka opname ${o.business_date}`}
                      >
                        Buka
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>

        {aktif && (
          <>
            <div className="card">
              <div className="card-kepala">
                <h3>Hitung fisik — {aktif.business_date}</h3>
                {terkunci ? (
                  <span className="lencana lencana-aman">Sudah diposting, tidak bisa diubah</span>
                ) : (
                  <span className="lencana lencana-hati">Draf</span>
                )}
              </div>

              {stok.length === 0 ? (
                <p className="kosong">Belum ada stok untuk dihitung.</p>
              ) : (
                <>
                  <div className="alat">
                    <Cari nilai={q} ubah={setQ} petunjuk="Nama produk atau pemilik" />
                    <span className="hitung">
                      {hasil.length} dari {stok.length} baris
                    </span>
                  </div>
                  <Tabel label="Lembar hitung fisik">
                    <thead>
                      <tr>
                        <th>Produk</th>
                        <th>Pemilik</th>
                        <th className="angka">Sistem</th>
                        <th style={{ width: 110 }}>Hitung</th>
                        <th style={{ width: 190 }}>Alasan selisih</th>
                        <th style={{ width: 180 }}>Catatan</th>
                      </tr>
                    </thead>
                    <tbody>
                      {hasil.map((row) => (
                        <BarisHitung
                          key={`${row.product_id}:${row.owner_id ?? 'company'}`}
                          row={row}
                          tersimpan={lines.find(
                            (l) => l.product_id === row.product_id && l.owner_id === row.owner_id,
                          )}
                          terkunci={terkunci}
                          simpan={simpanBaris}
                        />
                      ))}
                    </tbody>
                  </Tabel>
                </>
              )}
            </div>

            <div className="card">
              <h3>Selisih</h3>
              {selisih.length === 0 ? (
                <p className="kosong">Belum ada selisih tercatat.</p>
              ) : (
                <Tabel label="Selisih hasil opname">
                  <thead>
                    <tr>
                      <th>Produk</th>
                      <th>Pemilik</th>
                      <th className="angka">Sistem</th>
                      <th className="angka">Hitung</th>
                      <th className="angka">Selisih</th>
                      <th>Alasan</th>
                      <th className="angka">Harga satuan</th>
                    </tr>
                  </thead>
                  <tbody>
                    {selisih.map((l) => (
                      <tr key={l.id}>
                        <td>{l.product_name}</td>
                        <td>{l.owner_name ?? 'Perusahaan'}</td>
                        <td className="angka">{l.system_qty}</td>
                        <td className="angka">{l.counted_qty}</td>
                        <td className={l.variance < 0 ? 'angka teks-bahaya' : 'angka'}>
                          {l.variance > 0 ? `+${l.variance}` : l.variance}
                        </td>
                        <td>
                          {l.reason_code ?? (
                            <span className="teks-bahaya">belum diisi</span>
                          )}
                        </td>
                        <td className="angka">
                          {l.unit_cost_idr === null ? '—' : formatIDR(l.unit_cost_idr)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </Tabel>
              )}

              {!terkunci && (
                <>
                  {tanpaAlasan.length > 0 && (
                    <div className="disclaimer">
                      <strong>{tanpaAlasan.length} selisih belum punya alasan.</strong> Isi alasannya
                      sebelum posting — sesudah stok bergerak, tidak ada lagi yang bisa menjelaskan
                      ke mana barangnya pergi.
                    </div>
                  )}
                  <div className="aksi">
                    <button onClick={() => setKonfirmasi(true)} disabled={sedang}>
                      Posting penyesuaian
                    </button>
                  </div>
                </>
              )}
            </div>
          </>
        )}
      </div>

      {konfirmasi && aktif && (
        <Konfirmasi
          judul="Posting penyesuaian stok"
          gawat
          sibuk={sedang}
          labelJalankan="Posting, stok bergerak"
          labelAlasan="Alasan posting"
          petunjukAlasan="Mis. Opname bulanan Oktober"
          tutup={() => setKonfirmasi(false)}
          jalankan={(alasan) => void posting(alasan)}
        >
          <p>
            <strong>{selisih.length} baris</strong> akan disesuaikan pada opname{' '}
            {aktif.business_date}. Stok benar-benar bergerak, masing-masing ke bucket pemiliknya
            sendiri.
          </p>
          {selisih.length > 0 && (
            <ul>
              {selisih.slice(0, 6).map((l) => (
                <li key={l.id}>
                  {l.product_name} · {l.owner_name ?? 'Perusahaan'}:{' '}
                  <strong>
                    {l.variance > 0 ? `+${l.variance}` : l.variance}
                  </strong>{' '}
                  {l.reason_code ? `(${l.reason_code})` : '(tanpa alasan)'}
                </li>
              ))}
              {selisih.length > 6 && <li>…dan {selisih.length - 6} baris lain.</li>}
            </ul>
          )}
          <p className="catatan">
            Setelah diposting, opname ini tidak bisa diubah lagi. Koreksi berikutnya berarti hitung
            ulang. Perubahan dicatat di log audit (INV-10).
          </p>
        </Konfirmasi>
      )}
    </>
  )
}

function BarisHitung({
  row,
  tersimpan,
  terkunci,
  simpan,
}: {
  row: OnHand
  tersimpan: OpnameLine | undefined
  terkunci: boolean
  simpan: (row: OnHand, counted: string, kode: string, catatan: string) => Promise<void>
}) {
  const [hitung, setHitung] = useState(String(tersimpan?.counted_qty ?? row.qty_on_hand))
  const [kode, setKode] = useState(tersimpan?.reason_code ?? '')
  const [catatan, setCatatan] = useState(tersimpan?.reason_note ?? '')

  const selisih = Number(hitung) - row.qty_on_hand

  return (
    <tr>
      <td>{row.product_name}</td>
      <td>{row.owner_name ?? 'Perusahaan'}</td>
      <td className="angka">{row.qty_on_hand}</td>
      <td>
        <input
          type="number"
          min={0}
          value={hitung}
          disabled={terkunci}
          aria-label={`Hitung fisik ${row.product_name}, ${row.owner_name ?? 'Perusahaan'}`}
          onChange={(e) => setHitung(e.target.value)}
          onBlur={() => void simpan(row, hitung, kode, catatan)}
        />
      </td>
      <td>
        <select
          value={kode}
          disabled={terkunci || selisih === 0}
          aria-label={`Alasan selisih ${row.product_name}`}
          onChange={(e) => {
            setKode(e.target.value)
            void simpan(row, hitung, e.target.value, catatan)
          }}
        >
          <option value="">{selisih === 0 ? 'Cocok' : 'Pilih alasan'}</option>
          {KODE_ALASAN.map(([nilai, label]) => (
            <option key={nilai} value={nilai}>
              {label}
            </option>
          ))}
        </select>
      </td>
      <td>
        <input
          value={catatan}
          disabled={terkunci}
          aria-label={`Catatan ${row.product_name}`}
          onChange={(e) => setCatatan(e.target.value)}
          onBlur={() => void simpan(row, hitung, kode, catatan)}
        />
      </td>
    </tr>
  )
}

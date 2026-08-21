import { useCallback, useEffect, useState } from 'react'
import { countSheet, listOpnames, startOpname, getOpname, saveOpnameLine, postOpname, KODE_ALASAN } from '../api/pembelian'
import type { OnHand, Opname, OpnameLine } from '../api/pembelian'
import { ApiError } from '../api/client'
import { formatIDR } from '../money'
import { Galat } from '../components/dasar'

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
 * is the moment stock actually moves.
 */
export function OpnamePage({ entityId }: { entityId: string }) {
  const [daftar, setDaftar] = useState<Opname[]>([])
  const [aktif, setAktif] = useState<Opname | null>(null)
  const [lines, setLines] = useState<OpnameLine[]>([])
  const [stok, setStok] = useState<OnHand[]>([])
  const [galat, setGalat] = useState<string | null>(null)
  const [alasanPosting, setAlasanPosting] = useState('')
  const [sedang, setSedang] = useState(false)

  const muat = useCallback(async () => {
    try {
      setDaftar(await listOpnames(entityId))
      setStok(await countSheet(entityId))
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data')
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  async function buka(id: string) {
    try {
      const got = await getOpname(entityId, id)
      setAktif(got.opname)
      setLines(got.lines)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal membuka opname')
    }
  }

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

  async function posting() {
    if (!aktif) return
    setSedang(true)
    try {
      await postOpname(entityId, aktif.id, alasanPosting)
      setAlasanPosting('')
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

  return (
    <>
      <h2>Opname stok</h2>
      <p style={{ color: 'var(--muted)', marginTop: -8 }}>
        Dihitung per pemilik, bukan per produk. Selisih harus masuk ke bucket satu orang.
      </p>
      <Galat pesan={galat} />

      <div className="card" style={{ marginBottom: 20 }}>
        <button onClick={() => void mulai()} disabled={sedang}>
          Mulai opname baru
        </button>
        {daftar.length > 0 && (
          <table style={{ marginTop: 12 }}>
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
                  <td>{o.status === 'POSTED' ? 'Sudah diposting' : 'Draf'}</td>
                  <td>
                    <button className="sekunder" onClick={() => void buka(o.id)}>
                      Buka
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {aktif && (
        <>
          <div className="card" style={{ marginBottom: 20 }}>
            <h3>
              Hitung fisik — {aktif.business_date}{' '}
              {terkunci && <span style={{ color: 'var(--muted)' }}>(sudah diposting, tidak bisa diubah)</span>}
            </h3>
            {stok.length === 0 ? (
              <p className="kosong">Belum ada stok untuk dihitung.</p>
            ) : (
              <table>
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
                  {stok.map((row) => (
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
              </table>
            )}
          </div>

          <div className="card">
            <h3>Selisih</h3>
            {selisih.length === 0 ? (
              <p className="kosong">Belum ada selisih tercatat.</p>
            ) : (
              <table>
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
                      <td className="angka" style={{ color: l.variance < 0 ? 'var(--danger)' : undefined }}>
                        {l.variance > 0 ? `+${l.variance}` : l.variance}
                      </td>
                      <td>{l.reason_code ?? '—'}</td>
                      <td className="angka">
                        {l.unit_cost_idr === null ? '—' : formatIDR(l.unit_cost_idr)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            {!terkunci && (
              <div style={{ marginTop: 16 }}>
                <label>
                  <span>Alasan posting *</span>
                  <input
                    value={alasanPosting}
                    onChange={(e) => setAlasanPosting(e.target.value)}
                    placeholder="mis. Opname bulanan Oktober"
                  />
                </label>
                <p style={{ color: 'var(--muted)', fontSize: 14 }}>
                  Setelah diposting, stok benar-benar bergerak dan opname ini tidak bisa diubah lagi.
                  Koreksi berikutnya berarti hitung ulang.
                </p>
                <button onClick={() => void posting()} disabled={sedang || alasanPosting.trim() === ''}>
                  {sedang ? 'Memposting…' : 'Posting penyesuaian'}
                </button>
              </div>
            )}
          </div>
        </>
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
          onChange={(e) => setHitung(e.target.value)}
          onBlur={() => void simpan(row, hitung, kode, catatan)}
        />
      </td>
      <td>
        <select
          value={kode}
          disabled={terkunci || selisih === 0}
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
          onChange={(e) => setCatatan(e.target.value)}
          onBlur={() => void simpan(row, hitung, kode, catatan)}
        />
      </td>
    </tr>
  )
}
